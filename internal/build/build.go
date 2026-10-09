// Package build compiles, assembles, archives and links a project with the
// cc370 toolchain.
//
// The command lines are mbt v2's (mk/mbt.mk at v2.2.0), argument for argument:
// the differential comparison requires every object deck, archive and load
// module to be byte-identical.  What changes is everything around them -- one
// process instead of make plus Python, steps run in parallel, and a step is
// redone when its command line changed, not only when an input got newer.
package build

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// Options select what to build and how loudly.
type Options struct {
	BuildDir   string   // "build"
	IncludeDir string   // mbt's own headers (mbtcheck.h)
	Modules    bool     // production load modules
	Tests      bool     // test load modules
	Lib        bool     // the [lib] archive
	Only       []string // unit names; empty = all selected kinds
	Jobs       int
	Verbose    bool
	Out        io.Writer
	// PreLink runs before the first link step that has work to do, with the
	// compile flags; an error stops the build (the module-data check).
	PreLink func(cflags []string) error
}

// Builder runs one build of one project.
type Builder struct {
	P    *project.Project
	T    *toolchain.Toolchain
	O    Options
	mu   sync.Mutex
	logs io.Writer
}

// Error is a failed step; Code follows the exit-code convention (1 = build).
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func New(p *project.Project, t *toolchain.Toolchain, o Options) *Builder {
	if o.BuildDir == "" {
		o.BuildDir = "build"
	}
	if o.Jobs <= 0 {
		o.Jobs = runtime.NumCPU()
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	return &Builder{P: p, T: t, O: o, logs: o.Out}
}

func (b *Builder) say(format string, a ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fmt.Fprintf(b.logs, format+"\n", a...)
}

// abs makes a project-relative path absolute for the file system; commands
// still get the relative spelling, as make passed it.
func (b *Builder) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(b.P.Root, p)
}

// -- flags ----------------------------------------------------------------

// depIncludes and depLibs are the staged dependencies, in the order make's
// $(wildcard) returned them (sorted).
func (b *Builder) depIncludes() []string {
	m, _ := filepath.Glob(filepath.Join(b.P.Root, ".mbt/deps/*/include"))
	return relSorted(b.P.Root, m)
}

func (b *Builder) depLibs() []string {
	m, _ := filepath.Glob(filepath.Join(b.P.Root, ".mbt/deps/*/lib/*.a"))
	return relSorted(b.P.Root, m)
}

func relSorted(root string, abs []string) []string {
	var out []string
	for _, a := range abs {
		r, err := filepath.Rel(root, a)
		if err == nil {
			out = append(out, filepath.ToSlash(r))
		}
	}
	sort.Strings(out)
	return out
}

// CFlags is the compile flags, for checks that preprocess as the build does.
func (b *Builder) CFlags() []string { return b.cflags() }

// cflags is CFLAGS as mk/mbt.mk builds it: the defaults, the project's
// [build] cflags, every dependency's include dir, mbt's headers, .mbt.
func (b *Builder) cflags() []string {
	f := append([]string{}, project.DefaultCFlags...)
	if b.P.File == project.FileV3 {
		// after -O1, before the warnings and the project's own flags
		f = append([]string{f[0], project.DefaultStd}, f[1:]...)
	}
	f = append(f, strings.Fields(strings.Join(b.P.CFlags, " "))...)
	for _, d := range b.depIncludes() {
		f = append(f, "-I", d)
	}
	f = append(f, "-I", b.O.IncludeDir, "-I", ".mbt")
	return f
}

func (b *Builder) asflags() []string {
	return strings.Fields(strings.Join(b.P.ASFlags, " "))
}

// -- steps ----------------------------------------------------------------

type step struct {
	out    string   // project-relative output
	inputs []string // project-relative or absolute
	argv   []string
	label  string
	kind   string // cc, as, ar, ld
}

// Run builds what Options select.
func (b *Builder) Run() error {
	if err := CheckStaged(b.P.Root, b.P.Raw); err != nil {
		return err
	}
	if err := os.MkdirAll(b.abs(b.O.BuildDir), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(b.abs(".mbt/cmd"), 0o755); err != nil {
		return err
	}
	units := b.selectedUnits()
	if b.O.Modules || len(b.O.Only) > 0 {
		if err := b.checkDepStartup(); err != nil {
			return err
		}
	}

	// every source of the project must map to an object of its own, whatever
	// is built this time: build/<stem>.o drops the directory, and two sources
	// sharing a stem would silently share one object (mbt#200)
	if err := CheckObjectNames(b.P, b.O.BuildDir); err != nil {
		return err
	}

	// objects needed
	need := map[string]string{} // object -> source
	var order []string
	add := func(srcs []string) {
		for _, s := range srcs {
			o := project.ObjectPath(b.O.BuildDir, s)
			if _, ok := need[o]; !ok {
				order = append(order, o)
			}
			need[o] = s
		}
	}
	for _, u := range units {
		add(u.Sources)
	}
	if len(units) > 0 && b.P.HasInternal {
		add(b.P.Internal)
	}
	if b.O.Lib && b.P.Lib != nil {
		add(b.P.Lib.Sources)
	}

	var compiles []step
	for _, o := range order {
		compiles = append(compiles, b.compileStep(need[o], o))
	}
	if err := b.runAll(compiles); err != nil {
		return err
	}

	var archives []step
	if len(units) > 0 && b.P.HasInternal {
		archives = append(archives, b.archiveStep(b.P.InternalArchive(b.O.BuildDir), objs(b.O.BuildDir, b.P.Internal)))
	}
	if b.O.Lib && b.P.Lib != nil && len(b.P.Lib.Sources) > 0 {
		archives = append(archives, b.archiveStep(path.Join(b.O.BuildDir, b.P.Lib.Name+".a"), objs(b.O.BuildDir, b.P.Lib.Sources)))
	}
	if err := b.runAll(archives); err != nil {
		return err
	}

	var links []step
	work := false
	for _, u := range units {
		s, err := b.linkStep(u)
		if err != nil {
			return err
		}
		links = append(links, s)
		work = work || b.stale(s)
	}
	if work && b.O.PreLink != nil {
		if err := b.O.PreLink(b.cflags()); err != nil {
			return err
		}
	}
	return b.runAll(links)
}

// CheckObjectNames refuses two different sources that map to the same
// object, naming both. mbt 2 let the first one win, so the other was never
// compiled: a link failure at best, a wrong object linked at worst.
func CheckObjectNames(p *project.Project, builddir string) error {
	from := map[string]string{} // object -> source
	var clash []string
	check := func(srcs []string) {
		for _, s := range srcs {
			o := project.ObjectPath(builddir, s)
			if prev, ok := from[o]; ok && prev != s {
				clash = append(clash, fmt.Sprintf("%s and %s both compile to %s", prev, s, o))
				continue
			}
			from[o] = s
		}
	}
	for _, u := range append(append([]*project.Unit{}, p.Modules...), p.Tests...) {
		check(u.Sources)
	}
	check(p.Internal)
	if p.Lib != nil {
		check(p.Lib.Sources)
	}
	if len(clash) == 0 {
		return nil
	}
	return &Error{Code: 2, Msg: "sources sharing a name: " + strings.Join(clash, "; ") + " -- rename one; objects are named after the file alone"}
}

func objs(builddir string, srcs []string) []string {
	var out []string
	for _, s := range srcs {
		out = append(out, project.ObjectPath(builddir, s))
	}
	return out
}

func (b *Builder) selectedUnits() []*project.Unit {
	var units []*project.Unit
	if b.O.Modules {
		units = append(units, b.P.Modules...)
	}
	if b.O.Tests {
		units = append(units, b.P.Tests...)
	}
	if len(b.O.Only) > 0 {
		want := map[string]bool{}
		for _, n := range b.O.Only {
			want[strings.ToUpper(n)] = true
		}
		units = nil
		for _, u := range b.P.Units() {
			if want[u.Name] {
				units = append(units, u)
			}
		}
	}
	return units
}

func (b *Builder) compileStep(src, obj string) step {
	ext := strings.ToLower(path.Ext(src))
	if ext == ".asm" || ext == ".s" {
		argv := append([]string{b.T.AS}, b.asflags()...)
		argv = append(argv, "-o", obj, src)
		return step{out: obj, inputs: []string{src}, argv: argv, label: "[as370] " + src, kind: "as"}
	}
	argv := append([]string{b.T.CC}, b.cflags()...)
	argv = append(argv, "-MMD", "-MP", "-c", src, "-o", obj)
	return step{out: obj, inputs: []string{src}, argv: argv, label: "[cc370] " + src, kind: "cc"}
}

func (b *Builder) archiveStep(out string, members []string) step {
	argv := append([]string{b.T.AR, "rc", out}, members...)
	return step{out: out, inputs: members, argv: argv,
		label: fmt.Sprintf("[ar370] %s (%d objects)", path.Base(out), len(members)), kind: "ar"}
}

func (b *Builder) linkStep(u *project.Unit) (step, error) {
	t := b.T
	out := path.Join(b.O.BuildDir, u.Name)
	argv := []string{t.LD}
	argv = append(argv, t.LibDirs()...)
	argv = append(argv, "-e", u.Entry)
	desc := ""
	switch u.Startup {
	case project.StartupC:
		if t.CRTInLibc {
			if !t.EntryAutocall {
				return step{}, &Error{1, fmt.Sprintf("%s: libc370 has its CRT in libc.a, which ld370 only finds by the entry name from cc370 1.2.0 on -- upgrade cc370", u.Name)}
			}
		} else {
			argv = append(argv, filepath.Join(t.LibcDir, u.Startfile+".o"))
			desc = ", " + u.Startfile
		}
	case project.StartupCRTM:
		argv = append(argv, filepath.Join(t.LibcDir, "crtm.o"))
		desc = ", crtm"
	case project.StartupNone:
		desc = ", no crt"
	}
	uobjs := objs(b.O.BuildDir, u.Sources)
	argv = append(argv, uobjs...)
	inputs := append([]string{}, uobjs...)
	if ia := b.P.InternalArchive(b.O.BuildDir); ia != "" {
		argv = append(argv, ia)
		inputs = append(inputs, ia)
	}
	if t.CC370RT {
		argv = append(argv, "-lcc370rt")
	}
	if !u.DepStartup {
		argv = append(argv, "-lc")
	}
	libs := b.depLibs()
	argv = append(argv, libs...)
	argv = append(argv, "-lc")
	if u.AC != 0 {
		argv = append(argv, "--ac", fmt.Sprint(u.AC))
	}
	argv = append(argv, u.Attrs...)
	for _, a := range u.Aliases {
		argv = append(argv, "--alias", a)
	}
	argv = append(argv, "-iebcopy", "-o", out)
	inputs = append(inputs, libs...)
	inputs = append(inputs, t.LinkInputs()...)
	dep := ""
	if u.DepStartup {
		dep = " [dep startup]"
	}
	return step{out: out + ".iebcopy", inputs: inputs, argv: argv,
		label: fmt.Sprintf("[ld370] %s (entry=%s%s)%s", u.Name, u.Entry, desc, dep), kind: "ld"}, nil
}

// -- execution ------------------------------------------------------------

func (b *Builder) runAll(steps []step) error {
	var todo []step
	for _, s := range steps {
		if b.stale(s) {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	work := make(chan step)
	errs := make(chan error, len(todo))
	var wg sync.WaitGroup
	for i := 0; i < b.O.Jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range work {
				errs <- b.exec(s)
			}
		}()
	}
	// the label is printed as the step is handed out, so the log follows
	// the order of the steps however the workers interleave
	for _, s := range todo {
		b.announce(s)
		work <- s
	}
	close(work)
	wg.Wait()
	close(errs)
	var first error
	for e := range errs {
		if e != nil && first == nil {
			first = e
		}
	}
	return first
}

func (b *Builder) announce(s step) {
	if b.O.Verbose {
		b.say("%s", strings.Join(s.argv, " "))
	} else {
		b.say("%s", s.label)
	}
}

func (b *Builder) exec(s step) error {
	if s.kind == "ar" {
		os.Remove(b.abs(s.out)) // `ar rc` would keep members of an older build
	}
	cmd := exec.Command(s.argv[0], s.argv[1:]...)
	cmd.Dir = b.P.Root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	rc := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		rc = ee.ExitCode()
	} else if err != nil {
		return &Error{1, fmt.Sprintf("%s: %v", s.argv[0], err)}
	}
	if out.Len() > 0 {
		b.mu.Lock()
		b.logs.Write(out.Bytes())
		b.mu.Unlock()
	}
	// as370 returns IFOX00's severity: a warned assembly (4) still punches
	// a usable deck, as COND=(8,LT) let it on MVS.
	failed := rc != 0
	if s.kind == "as" && rc < 8 {
		failed = false
	}
	if s.kind == "cc" {
		// Whatever the compile returned: cc370 writes the .d before as370 runs.
		escapeDepfile(b.abs(strings.TrimSuffix(s.out, ".o") + ".d"))
	}
	if failed {
		// A failed step leaves no output behind: as370 writes its deck even
		// at RC 8, and a later build would take it for up to date.
		os.Remove(b.abs(s.out))
		os.Remove(b.cmdFile(s.out))
		return &Error{1, fmt.Sprintf("%s failed (rc=%d)", s.label, rc)}
	}
	return os.WriteFile(b.cmdFile(s.out), []byte(signature(s.argv)), 0o644)
}

// -- staleness ------------------------------------------------------------

func (b *Builder) cmdFile(out string) string {
	h := sha256.Sum256([]byte(out))
	return b.abs(filepath.Join(".mbt/cmd", hex.EncodeToString(h[:8])))
}

func signature(argv []string) string { return strings.Join(argv, "\x00") }

// stale: the output is missing, its command line changed, or an input --
// including every header the compiler recorded in the .d -- is newer.
func (b *Builder) stale(s step) bool {
	st, err := os.Stat(b.abs(s.out))
	if err != nil {
		return true
	}
	if prev, err := os.ReadFile(b.cmdFile(s.out)); err != nil || string(prev) != signature(s.argv) {
		return true
	}
	inputs := s.inputs
	if s.kind == "cc" {
		inputs = append(inputs, readDepfile(b.abs(strings.TrimSuffix(s.out, ".o")+".d"))...)
	}
	for _, in := range inputs {
		is, err := os.Stat(b.abs(in))
		if err != nil || is.ModTime().After(st.ModTime()) {
			return true
		}
	}
	return false
}

// escapeDepfile escapes every '#' in a .d file, as mbt v2 did: cc370 does
// not, and make reads an unescaped '#' (an MVS member name like ufsd#cmd) as a
// comment and stops with "missing separator".  v2 and v3 share build/ while a
// project migrates, so v3's .d files have to stay readable for v2's make.  An
// already escaped '\#' stays single.
func escapeDepfile(p string) {
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	s := strings.ReplaceAll(string(data), `\#`, "#")
	s = strings.ReplaceAll(s, "#", `\#`)
	if s != string(data) {
		os.WriteFile(p, []byte(s), 0o644)
	}
}

// readDepfile returns the prerequisites of a .d file written by cc370 -MMD
// (with '#' escaped by escapeDepfile; a space in a name is "\ ").
func readDepfile(p string) []string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		all.WriteString(strings.TrimSuffix(line, "\\"))
		all.WriteString(" ")
	}
	text := all.String()
	// only the first rule (the object's); -MP adds empty rules per header
	if i := strings.Index(text, ": "); i >= 0 {
		text = text[i+2:]
	}
	if j := strings.Index(text, ":"); j >= 0 {
		// cut at the next "header:" rule
		k := strings.LastIndex(text[:j], " ")
		if k >= 0 {
			text = text[:k]
		}
	}
	text = strings.ReplaceAll(text, `\#`, "#")
	text = strings.ReplaceAll(text, `\ `, "\x00")
	var deps []string
	for _, w := range strings.Fields(text) {
		deps = append(deps, strings.ReplaceAll(w, "\x00", " "))
	}
	return deps
}

// -- dep_startup (#62) ----------------------------------------------------

// checkDepStartup: when a dependency archive defines @@START, every module
// (not startup = false) must say whether it wants it.  Left alone it would get
// libc370's and build green -- a CGI module without its HTTP header.
func (b *Builder) checkDepStartup() error {
	var defines []string
	for _, a := range b.depLibs() {
		out, err := exec.Command(b.T.AR, "t", b.abs(a)).Output()
		if err != nil {
			return &Error{2, fmt.Sprintf("cannot list %s: %v", a, err)}
		}
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && strings.HasSuffix(f[1], ".o") && f[0] == "@@START" {
				defines = append(defines, a)
				break
			}
		}
	}
	if len(defines) == 0 {
		return nil
	}
	var msgs []string
	for _, u := range b.P.Modules {
		if u.Startup == project.StartupNone || u.DepStartupSet {
			continue
		}
		msgs = append(msgs, fmt.Sprintf("[[module]] %s: %s define(s) @@START; set dep_startup = true to use it (CGI module) or dep_startup = false for libc370's (#62)",
			u.Name, strings.Join(defines, ", ")))
	}
	if len(msgs) > 0 {
		return &Error{2, strings.Join(msgs, "\n")}
	}
	return nil
}
