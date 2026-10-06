// Command mbt is the MVS Build Tool, version 3.
//
// Phase 3 of the mbt 3 proposal (internals/mbt-3-design.md): the cc370 host
// path.  It reads an mbt.toml (schema 3) or an mbt v2 project.toml; from the
// latter its output is compared byte for byte with mbt v2's.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mvslovers/mbt/include"
	"github.com/mvslovers/mbt/internal/build"
	"github.com/mvslovers/mbt/internal/compiledb"
	"github.com/mvslovers/mbt/internal/config"
	"github.com/mvslovers/mbt/internal/deploy"
	"github.com/mvslovers/mbt/internal/deps"
	"github.com/mvslovers/mbt/internal/dist"
	"github.com/mvslovers/mbt/internal/hosttest"
	"github.com/mvslovers/mbt/internal/migrate"
	"github.com/mvslovers/mbt/internal/moddata"
	"github.com/mvslovers/mbt/internal/mvsmf"
	"github.com/mvslovers/mbt/internal/mvstest"
	"github.com/mvslovers/mbt/internal/pkg"
	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/stamp"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// Exit codes (spec section 11.1).
const (
	exitOK       = 0
	exitBuild    = 1
	exitConfig   = 2
	exitInternal = 99
)

var version = "3.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: mbt <command> [options]

commands:
  build [--all] [--tests] [NAME...]   build the primary deliverable, or more
  deps [--update]                     stage dependencies as pinned in mbt.lock
  module-data [--all]                 check for writable data in RENT/AC(1) modules
  package                             build, then write the release artifacts to dist/
  dist                                re-render the SMP install package alone
  test [--only NAME]... [-v]          build and run the dual tests on the host
  test --mvs [--only NAME]... [--no-deploy] [--target DSN] [-v]
                                      build the test modules and run them on MVS
  check                               every test suite: the host first, then MVS
  migrate [--dry-run]                 convert project.toml into mbt.toml (schema 3)
  deploy [--target DSN] [--module M]... [--dry-run] [-v]
                                      pack the built modules and RECEIVE them on MVS
  compiledb                           write compile_commands.json for clangd
  doctor                              check the toolchain, the sysroot and the MVS connection
  version                             print mbt's version
`)
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitConfig
	}
	switch args[0] {
	case "build":
		return cmdBuild(args[1:])
	case "deps":
		return cmdDeps(args[1:])
	case "module-data":
		return cmdModdata(args[1:])
	case "package":
		return cmdPackage(args[1:])
	case "dist":
		return cmdDist()
	case "migrate":
		return cmdMigrate(args[1:])
	case "check":
		if code := cmdTest(nil); code != exitOK {
			return code
		}
		return cmdTest([]string{"--mvs"})
	case "test":
		return cmdTest(args[1:])
	case "deploy":
		return cmdDeploy(args[1:])
	case "compiledb":
		return cmdCompiledb()
	case "doctor":
		return cmdDoctor()
	case "version", "--version":
		fmt.Println("mbt", version)
		return exitOK
	case "help", "-h", "--help":
		usage()
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "[mbt] ERROR: unknown command %q\n", args[0])
	usage()
	return exitConfig
}

func cmdBuild(args []string) int {
	fl := flag.NewFlagSet("build", flag.ContinueOnError)
	all := fl.Bool("all", false, "modules and the library archive")
	tests := fl.Bool("tests", false, "also the test load modules")
	jobs := fl.Int("j", 0, "parallel steps (default: number of CPUs)")
	verbose := fl.Bool("v", false, "print every command line")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	_, code := doBuild(*all, *tests, *jobs, *verbose, fl.Args())
	if code == exitOK {
		fmt.Println("[mbt] Build complete")
	}
	return code
}

// doBuild loads the project and builds what the flags select.
func doBuild(all, tests bool, jobs int, verbose bool, only []string) (*project.Project, int) {
	root, _ := os.Getwd()

	p, err := project.Load(root)
	if err != nil {
		return nil, fail(err)
	}
	for _, w := range p.Warnings {
		fmt.Printf("[mbt] WARNING: %s\n", w)
	}
	t, err := toolchain.Find()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
		return nil, exitConfig
	}
	for _, w := range t.Warnings {
		fmt.Printf("[mbt] WARNING: %s\n", w)
	}
	if _, err := stamp.Write(root, p.Name, p.Version); err != nil {
		return nil, fail(err)
	}
	incDir := ".mbt/include"
	if err := writeHeaders(filepath.Join(root, incDir)); err != nil {
		return nil, fail(err)
	}

	if code := checkLibc(p, t); code != exitOK {
		return nil, code
	}
	o := build.Options{IncludeDir: incDir, Jobs: jobs, Verbose: verbose, Only: only}
	o.PreLink = func(cflags []string) error {
		in := moddata.FromRaw(root, p.Raw)
		in.CC, in.CFlags, in.Jobs = t.CC, cflags, jobs
		errs, warns := moddata.Check(in)
		if moddata.Print(os.Stderr, errs, warns, false, "mbt module-data --all") {
			return &build.Error{Code: exitBuild, Msg: "writable data in a rent = true module"}
		}
		return nil
	}
	switch {
	case len(o.Only) > 0:
	case p.Type == "library":
		o.Lib = true
	default:
		o.Modules = true
	}
	if all {
		o.Modules, o.Lib = p.Type != "library" || len(p.Modules) > 0, true
	}
	o.Tests = tests
	if err := build.New(p, t, o).Run(); err != nil {
		return nil, fail(err)
	}
	return p, exitOK
}

// runDist builds the SMP installation package when the project declares a
// [distribution]; the load XMIT must already be in dist/.
func runDist(p *project.Project, mtime time.Time) int {
	d, ok := p.Raw["distribution"].(map[string]any)
	if !ok || len(d) == 0 {
		return exitOK
	}
	if p.DistError != "" {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %s\n", p.DistError)
		return exitConfig
	}
	var mods []dist.Module
	for _, m := range p.Modules {
		mods = append(mods, dist.Module{Name: m.Name, Aliases: m.Aliases})
	}
	err := dist.Build(p.Raw, p.Name, p.Version, mods, dist.Options{
		Root: p.Root, DistDir: "dist", BuildDir: "build", Mtime: mtime,
		Log: func(s string) { fmt.Printf("[mbt] %s\n", s) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
		return exitConfig
	}
	return exitOK
}

// cmdDist: make dist -- re-render the SMP package alone, from the load XMIT
// a previous package left in dist/ (the inner loop for a samplib or JCL edit).
func cmdDist() int {
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	if d, ok := p.Raw["distribution"].(map[string]any); !ok || len(d) == 0 {
		fmt.Printf("[mbt] No [distribution] section in %s -- nothing to build\n", p.File)
		return exitOK
	}
	var mtime time.Time
	if e := os.Getenv("SOURCE_DATE_EPOCH"); e != "" {
		if sec, err := strconv.ParseInt(e, 10, 64); err == nil {
			mtime = time.Unix(sec, 0).UTC()
		}
	}
	return runDist(p, mtime)
}

// cmdMigrate converts project.toml into mbt.toml.  The converted file is
// loaded and compared with the original before anything is written; a
// difference beyond the intended ones writes nothing.
func cmdMigrate(args []string) int {
	fl := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dry := fl.Bool("dry-run", false, "print mbt.toml instead of writing it")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	if f, err := project.Find(root); err != nil {
		return fail(err)
	} else if f == project.FileV3 {
		fmt.Println("[mbt] mbt.toml is here already -- nothing to migrate")
		return exitOK
	}
	res, err := migrate.Convert(root)
	if err != nil {
		return fail(err)
	}
	diffs, err := migrate.Check(root, res.Text)
	if err != nil {
		return fail(err)
	}
	for _, n := range res.Notices {
		fmt.Printf("[mbt] NOTE: %s\n", n)
	}
	if len(diffs) > 0 {
		for _, d := range diffs {
			fmt.Fprintf(os.Stderr, "[mbt] ERROR: mbt.toml would differ: %s\n", d)
		}
		fmt.Fprintln(os.Stderr, "[mbt] ERROR: nothing written")
		return exitInternal
	}
	if *dry {
		fmt.Print(res.Text)
		return exitOK
	}
	if err := os.WriteFile(filepath.Join(root, project.FileV3), []byte(res.Text), 0o644); err != nil {
		return fail(err)
	}
	os.Remove(filepath.Join(root, project.FileV2))
	removed := "project.toml"
	if _, err := os.Stat(filepath.Join(root, "VERSION")); err == nil {
		os.Remove(filepath.Join(root, "VERSION"))
		removed += " and VERSION"
	}
	fmt.Printf("[mbt] Wrote mbt.toml (checked against project.toml: same modules, tests, flags and package); removed %s\n", removed)
	fmt.Println("[mbt] Left to the migrating PR: the Makefile, the mbt submodule (.gitmodules), and CI workflows that call make")
	return exitOK
}

// cmdPackage: build the modules and the library, then write dist/ (v2's
// `make package`).  SOURCE_DATE_EPOCH, when set, stamps the tarball.
func cmdPackage(args []string) int {
	fl := flag.NewFlagSet("package", flag.ContinueOnError)
	jobs := fl.Int("j", 0, "parallel steps")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	p, code := doBuild(true, false, *jobs, false, nil)
	if code != exitOK {
		return code
	}
	t, _ := toolchain.Find()
	o := pkg.Options{BuildDir: "build", DistDir: "dist", LD: t.LD, Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }}
	if e := os.Getenv("SOURCE_DATE_EPOCH"); e != "" {
		if sec, err := strconv.ParseInt(e, 10, 64); err == nil {
			o.Mtime = time.Unix(sec, 0).UTC()
		}
	}
	if err := pkg.Run(p, o); err != nil {
		return fail(err)
	}
	if code := runDist(p, o.Mtime); code != exitOK {
		return code
	}
	fmt.Println("[mbt] Package complete -> dist/")
	return exitOK
}

// writeHeaders puts mbt's own headers where -I points, rewriting a file only
// when it changed so an unchanged header recompiles nothing.
func writeHeaders(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(include.Headers, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := include.Headers.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, p)
		if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
			return nil
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

// checkLibc compares the installed libc370 with [toolchain] libc370 (v2's
// mbttoolchain.py --check): older fails the build, an unreadable stamp warns.
// The "ok" line is printed when it changes, not on every build.
func checkLibc(p *project.Project, t *toolchain.Toolchain) int {
	want := ""
	if tc, ok := p.Raw["toolchain"].(map[string]any); ok {
		if s, ok := tc["libc370"].(string); ok {
			want = strings.TrimSpace(s)
		}
	}
	st, msg := t.CheckLibc370(want, false)
	switch st {
	case toolchain.CheckFail:
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %s\n", msg)
		return exitBuild
	case toolchain.CheckUnknown, toolchain.CheckDrift:
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: %s\n", msg)
	case toolchain.CheckOK:
		f := filepath.Join(p.Root, ".mbt", "libc370-checked")
		if old, _ := os.ReadFile(f); string(old) != msg {
			fmt.Printf("[mbt] %s\n", msg)
			os.WriteFile(f, []byte(msg), 0o644)
		}
	}
	return exitOK
}

func cmdModdata(args []string) int {
	fl := flag.NewFlagSet("module-data", flag.ContinueOnError)
	all := fl.Bool("all", false, "list every warning, not three per module")
	raw := fl.Bool("raw", false, "scan the sources as written, without cc370 -E")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	t, err := toolchain.Find()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
		return exitConfig
	}
	stamp.Write(root, p.Name, p.Version)
	writeHeaders(filepath.Join(root, ".mbt/include"))
	in := moddata.FromRaw(root, p.Raw)
	in.CC = t.CC
	if !*raw {
		in.CFlags = build.New(p, t, build.Options{IncludeDir: ".mbt/include"}).CFlags()
	}
	errs, warns := moddata.Check(in)
	if moddata.Print(os.Stderr, errs, warns, *all, "mbt module-data --all") {
		return exitBuild
	}
	return exitOK
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// cmdTest: make test-host.  No MVS; tests with assembler sources or
// host = false are skipped.
func cmdTest(args []string) int {
	fl := flag.NewFlagSet("test", flag.ContinueOnError)
	var only multiFlag
	fl.Var(&only, "only", "run only this test (repeatable)")
	verbose := fl.Bool("v", false, "print the compile commands and errors")
	mvs := fl.Bool("mvs", false, "run the test modules on MVS (make test-mvs)")
	noDeploy := fl.Bool("no-deploy", false, "with --mvs: reuse the TESTLIB already there")
	target := fl.String("target", "", "with --mvs: the runtime production LINKLIB")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	if *mvs {
		return cmdTestMVS(only, *noDeploy, *target, *verbose)
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	stamp.Write(root, p.Name, p.Version)
	writeHeaders(filepath.Join(root, ".mbt/include"))
	c := hosttest.Config{CFlags: project.RawStrs(rawTable(p.Raw, "build"), "cflags"), Resolve: p.Resolve, Replace: map[string]string{}}
	host := rawTable(p.Raw, "host")
	c.HostCC, _ = host["cc"].(string)
	c.HostCFlags = project.RawStrs(host, "cflags")
	c.HostSources = project.RawStrs(host, "sources")
	if r, ok := host["replace"].(map[string]any); ok {
		for k, v := range r {
			if s, ok := v.(string); ok {
				c.Replace[k] = s
			}
		}
	}
	for _, t := range p.RawTables("test") {
		name, _ := t["name"].(string)
		h, set := t["host"].(bool)
		c.Tests = append(c.Tests, hosttest.Test{Name: name, Host: !set || h,
			Sources: project.RawStrs(t, "sources"), Excludes: project.RawStrs(t, "exclude")})
	}
	ok, err := hosttest.Run(c, hosttest.Options{Root: root, BuildDir: "build", IncludeDir: ".mbt/include",
		Only: only, Verbose: *verbose, Out: os.Stdout})
	if err != nil {
		return fail(err)
	}
	if !ok {
		return exitBuild
	}
	return exitOK
}

// cmdTestMVS: make test-mvs -- build the tests, then run them on MVS.
func cmdTestMVS(only []string, noDeploy bool, target string, verbose bool) int {
	p, code := doBuild(false, true, 0, false, nil)
	if code != exitOK {
		return code
	}
	t, _ := toolchain.Find()
	var tests []mvstest.TestDecl
	opt := func(m map[string]any, k string) *string {
		if s, ok := m[k].(string); ok {
			return &s
		}
		return nil
	}
	for _, tt := range p.RawTables("test") {
		d := mvstest.TestDecl{Parm: opt(tt, "parm"), ParmBatch: opt(tt, "parm_batch"), TSO: opt(tt, "parm_tso")}
		d.Name, _ = tt["name"].(string)
		for _, fx := range anyTables(tt["fixture"]) {
			dd, _ := fx["dd"].(string)
			d.Fixtures = append(d.Fixtures, mvstest.FixtureDecl{DD: dd, Members: project.RawStrs(fx, "members")})
		}
		tests = append(tests, d)
	}
	tt, _ := rawTable(p.Raw, "test_deploy")["target"].(string)
	pt, _ := rawTable(p.Raw, "deploy")["target"].(string)
	return mvstest.Run(mvstest.Options{Root: p.Root, BuildDir: "build", LD: t.LD, Project: p.Name, Version: p.Version,
		Tests: tests, TestTarget: tt, LinkTarget: target, ProjectTarget: pt, Only: only, NoDeploy: noDeploy,
		Verbose: verbose, Out: os.Stdout, Err: os.Stderr, Config: config.Load(p.Root)})
}

func anyTables(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		var out []map[string]any
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// cmdDoctor: make doctor.  One deliberate difference: the configuration
// table masks MVS_PASS, which mbt 2 printed in clear.
func cmdDoctor() int {
	fmt.Println("[mbt] Running environment checks (mbt 3 / cc370)...")
	failed := 0
	bad := func(f string, a ...any) { fmt.Fprintf(os.Stderr, "[mbt] ERROR: "+f+"\n", a...); failed++ }
	for _, tool := range []string{"cc370", "as370", "ld370", "ar370", "xmit370"} {
		if p, err := exec.LookPath(tool); err == nil {
			fmt.Printf("[mbt] %s: %s\n", tool, p)
		} else {
			bad("%s not found on PATH", tool)
		}
	}
	root, _ := os.Getwd()
	t, err := toolchain.Find()
	if err != nil {
		bad("%v", err)
	} else {
		var missing []string
		for _, f := range []string{"libc.a", "crtm.o"} {
			if _, err := os.Stat(filepath.Join(t.LibcDir, f)); err != nil {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			bad("sysroot %s incomplete, missing: %s", t.Sysroot, strings.Join(missing, ", "))
		} else {
			fmt.Printf("[mbt] sysroot: %s (libc.a + crtm.o OK)\n", t.Sysroot)
		}
	}
	p, perr := project.Load(root)
	if t != nil {
		want := ""
		if perr == nil {
			if tc, ok := p.Raw["toolchain"].(map[string]any); ok {
				want, _ = tc["libc370"].(string)
			}
		}
		switch st, msg := t.CheckLibc370(strings.TrimSpace(want), false); st {
		case toolchain.CheckFail:
			bad("%s", msg)
		case toolchain.CheckUnknown:
			fmt.Fprintf(os.Stderr, "[mbt] WARNING: %s\n", msg)
		default:
			fmt.Printf("[mbt] %s\n", msg)
		}
	}
	if perr != nil {
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: project file not loaded, skipping MVS checks (%v)\n", perr)
		failed++
	} else {
		cfg := config.Load(root)
		port, _ := cfg.Port()
		c := mvsmf.New(cfg.Host(), port, cfg.User(), cfg.Pass())
		if code, err := c.Status("/info"); code == 0 {
			fmt.Fprintf(os.Stderr, "[mbt] WARNING: MVS host not reachable: %s:%d -- %v (only needed for 'mbt deploy')\n", cfg.Host(), port, err)
			failed++
		} else {
			fmt.Printf("[mbt] MVS host reachable: %s:%d (HTTP %d)\n", cfg.Host(), port, code)
			switch code, _ := c.Status("/restjobs/jobs"); code {
			case 200:
				fmt.Printf("[mbt] MVS credentials valid: %s\n", cfg.User())
			case 401:
				bad("MVS credentials invalid for %s (HTTP 401)", cfg.User())
			default:
				fmt.Printf("[mbt] MVS credentials check: HTTP %d for %s\n", code, cfg.User())
			}
		}
		fmt.Printf("[mbt] %s valid: %s v%s\n", p.File, p.Name, p.Version)
		fmt.Println("[mbt] Configuration:")
		keys := []struct{ name, key string }{{"MVS_HOST", "mvs.host"}, {"MVS_PORT", "mvs.port"}, {"MVS_USER", "mvs.user"},
			{"MVS_PASS", "mvs.pass"}, {"MVS_HLQ", "mvs.hlq"}, {"MVS_DEPS_HLQ", "mvs.deps_hlq"}, {"MVS_DEPS_VOLUME", "mvs.deps_volume"},
			{"JES_JOBCLASS", "jes.jobclass"}, {"JES_MSGCLASS", "jes.msgclass"}, {"BUILD_ID", "build.id"}}
		for _, k := range keys {
			v, src := cfg.Source(k.key)
			if k.name == "MVS_PASS" && v != "" {
				v = "********"
			}
			fmt.Printf("  %-15s = %-20s [%s]\n", k.name, v, src)
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "[mbt] %d check(s) failed\n", failed)
		return exitConfig
	}
	fmt.Println("[mbt] All checks passed")
	return exitOK
}

// cmdCompiledb: make compiledb.
func cmdCompiledb() int {
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	writeHeaders(filepath.Join(root, ".mbt/include"))
	var srcs []string
	for _, kind := range []string{"module", "test"} {
		for _, m := range p.RawTables(kind) {
			srcs = append(srcs, p.Resolve(project.RawStrs(m, "sources"), project.RawStrs(m, "exclude"))...)
		}
	}
	if l := rawTable(p.Raw, "lib"); len(l) > 0 {
		srcs = append(srcs, p.Resolve(project.RawStrs(l, "sources"), nil)...)
	}
	if in := rawTable(p.Raw, "internal"); len(in) > 0 {
		srcs = append(srcs, p.Resolve(project.RawStrs(in, "sources"), project.RawStrs(in, "exclude"))...)
	}
	n, err := compiledb.Write(root, project.RawStrs(rawTable(p.Raw, "build"), "cflags"), srcs, filepath.Join(root, ".mbt", "include"))
	if err != nil {
		return fail(err)
	}
	fmt.Printf("[mbt] Generated compile_commands.json (%d entries)\n", n)
	return exitOK
}

// cmdDeploy: make deploy.  Packs what is built; builds nothing.
func cmdDeploy(args []string) int {
	fl := flag.NewFlagSet("deploy", flag.ContinueOnError)
	target := fl.String("target", "", "override the target LINKLIB")
	var mods multiFlag
	fl.Var(&mods, "module", "deploy only this module (repeatable)")
	dry := fl.Bool("dry-run", false, "pack locally and report, touch no MVS")
	verbose := fl.Bool("v", false, "echo the ld370/RECEIVE commands")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	t, err := toolchain.Find()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
		return exitConfig
	}
	var names []string
	for _, m := range p.Modules {
		names = append(names, m.Name)
	}
	pt, _ := rawTable(p.Raw, "deploy")["target"].(string)
	return deploy.Run(deploy.Options{Root: root, BuildDir: "build", LD: t.LD, Project: p.Name, Version: p.Version,
		Modules: names, Target: *target, ProjectTarget: pt, Only: mods, DryRun: *dry, Verbose: *verbose,
		Out: os.Stdout, Err: os.Stderr, Config: config.Load(root)})
}

func rawTable(m map[string]any, key string) map[string]any {
	t, _ := m[key].(map[string]any)
	return t
}

func cmdDeps(args []string) int {
	fl := flag.NewFlagSet("deps", flag.ContinueOnError)
	update := fl.Bool("update", false, "re-resolve every range and rewrite mbt.lock")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	file, err := project.Find(root)
	if err != nil {
		return fail(err)
	}
	err = deps.Run(root, file, deps.Options{
		Update: *update,
		Log:    func(s string) { fmt.Printf("[mbt] %s\n", s) },
		Warn:   func(s string) { fmt.Fprintf(os.Stderr, "[mbt] WARNING: %s\n", s) },
	})
	if err != nil {
		return fail(err)
	}
	return exitOK
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
	var ce *project.ConfigError
	var be *build.Error
	var de *deps.Error
	switch {
	case errors.As(err, &ce):
		return exitConfig
	case errors.As(err, &be):
		return be.Code
	case errors.As(err, &de):
		return de.Code
	}
	return exitInternal
}
