package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/deploy"
	"github.com/mvslovers/mbt/internal/ext"
	"github.com/mvslovers/mbt/internal/mvsmf"
	"github.com/mvslovers/mbt/internal/mvstest"
	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/tn3270"
	"github.com/mvslovers/mbt/internal/version"
)

// tsoSession puts a TN3270 session behind the interface the Lua side of
// mbt test --tso drives.
type tsoSession struct {
	s          *tn3270.Session
	user, pass string
}

func (t *tsoSession) Logon() error  { return t.s.LogonTSO(context.Background(), t.user, t.pass) }
func (t *tsoSession) Logoff() error { return t.s.LogoffTSO() }
func (t *tsoSession) Type(s string) error {
	return t.s.Type(s)
}
func (t *tsoSession) Key(name string) error { return t.key(name, true) }

// Press sends the key without waiting for the keyboard to be restored
// (mbt#274): a running program keeps it locked until READY.
func (t *tsoSession) Press(name string) error { return t.key(name, false) }

func (t *tsoSession) key(name string, wait bool) error {
	var k tn3270.Key
	switch {
	case name == "enter":
		k = tn3270.Enter
	case name == "clear":
		k = tn3270.Clear
	case name == "tab":
		return t.s.Tab()
	case name == "pa1":
		k = tn3270.PA1
	case name == "pa2":
		k = tn3270.PA2
	case name == "pa3":
		k = tn3270.PA3
	case strings.HasPrefix(name, "pf"):
		n, err := strconv.Atoi(name[2:])
		if err != nil {
			return fmt.Errorf("no key %q", name)
		}
		if k, err = tn3270.PF(n); err != nil {
			return err
		}
	default:
		return fmt.Errorf("no key %q (enter, clear, tab, pf1-pf24, pa1-pa3)", name)
	}
	if !wait {
		return t.s.SendKey(k)
	}
	return t.s.Key(k)
}
func (t *tsoSession) Expect(text string, timeout time.Duration) error {
	return t.s.Expect(text, timeout)
}
func (t *tsoSession) Screen() []string   { return t.s.Screen() }
func (t *tsoSession) Cursor() (int, int) { r, c := t.s.Cursor(); return r + 1, c + 1 }

// cmdTestTSO: mbt test --tso -- the interactive tests in test/tso/*.lua,
// one TSO logon each, through the target's [tn3270].
func cmdTestTSO(args []string) int {
	fl := flag.NewFlagSet("test --tso", flag.ContinueOnError)
	var only multiFlag
	fl.Var(&only, "only", "run only this test (repeatable)")
	targetName := fl.String("target", "", "the MVS system (a name in ~/.mbt/targets.toml)")
	verbose := fl.Bool("v", false, "trace the 3270 data stream (passwords are never traced)")
	if err := parseArgs(fl, args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	tests, err := ext.TSOTests(root)
	if err != nil {
		return fail(err)
	}
	if len(only) > 0 {
		var keep [][2]string
		for _, t := range tests {
			for _, o := range only {
				if strings.EqualFold(o, t[0]) {
					keep = append(keep, t)
				}
			}
		}
		tests = keep
	}
	if len(tests) == 0 {
		fmt.Println("[mbt] no interactive tests (test/tso/*.lua)")
		return exitOK
	}
	selectedTarget = *targetName
	tg, err := chooseTarget(root, *targetName)
	if err != nil {
		return fail(err)
	}
	if tg.TN3270 == nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: target %s has no [target.%s.tn3270] -- interactive tests need a TN3270 user\n", tg.Name, tg.Name)
		return exitConfig
	}
	pw, err := tg.TN3270.Password.Resolve()
	if err != nil {
		return fail(err)
	}
	e, err := exts(p, *verbose, false)
	if err != nil {
		return fail(err)
	}
	if err := e.BeforeKind("test", "tso"); err != nil {
		return fail(err)
	}
	// after the hooks: a task may write the files a fixture names
	fixtures, code := stageTSOFixtures(p, tests)
	if code != exitOK {
		return code
	}
	info := map[string]any{"user": tg.TN3270.User, "testlib": testLibrary(p, tg.HLQ)}
	// the terminal: the target's size, a test's [tso.NAME] size ahead of it
	sizes := map[string]tn3270.Size{}
	decl := rawTable(p.Raw, "tso")
	for _, t := range tests {
		text := tg.TN3270.Size
		where := fmt.Sprintf("[target.%s.tn3270] size", tg.Name)
		if d, _ := decl[t[0]].(map[string]any); d != nil {
			if z, _ := d["size"].(string); z != "" {
				text, where = z, fmt.Sprintf("[tso.%s] size", strings.ToLower(t[0]))
			}
		}
		z := tn3270.Size{Cols: tn3270.Cols, Rows: tn3270.Rows}
		if text != "" {
			var err error
			if z, err = tn3270.ParseSize(text); err != nil {
				fmt.Fprintf(os.Stderr, "[mbt] ERROR: %s: %v\n", where, err)
				return exitConfig
			}
		}
		sizes[t[0]] = z
	}
	fmt.Printf("[mbt] %d interactive test(s) on %s (%s as %s)\n", len(tests), tg.Name, tg.TN3270.Addr(), tg.TN3270.User)
	failed := 0
	var rows []string
	for _, t := range tests {
		opt := tn3270.Options{Size: sizes[t[0]]}
		if *verbose {
			opt.Trace = os.Stderr
		}
		s, err := tn3270.Dial(context.Background(), tg.TN3270.Addr(), opt)
		if err == nil {
			ti := map[string]any{}
			for k, v := range info {
				ti[k] = v
			}
			ti["fixtures"] = fixtures[t[0]]
			ti["size"] = sizes[t[0]].String()
			err = e.TSOTest(t[1], &tsoSession{s: s, user: tg.TN3270.User, pass: pw}, ti)
			s.Close()
		}
		res := "ok"
		if err != nil {
			failed++
			res = "FAIL"
			fmt.Fprintf(os.Stderr, "[mbt] %s: %v\n", t[0], err)
		}
		rows = append(rows, fmt.Sprintf("  %-10s %s", t[0], res))
		// a user left logged on fails every logon after it: stop here
		// rather than report the rest as failures of their own
		var le *ext.LogoffError
		if errors.As(err, &le) && len(rows) < len(tests) {
			fmt.Fprintf(os.Stderr, "[mbt] ERROR: %d test(s) not run: %s could not be logged off\n", len(tests)-len(rows), le.User)
			for _, rest := range tests[len(rows):] {
				rows = append(rows, fmt.Sprintf("  %-10s %s", rest[0], "not run"))
				failed++
			}
			break
		}
	}
	fmt.Printf("\n  %-10s %s\n  ---------- ----\n%s\n\n", "TEST", "TSO", strings.Join(rows, "\n"))
	if code := after(p, "test", map[string]any{"kind": "tso", "passed": failed == 0}); code != exitOK {
		return code
	}
	if failed > 0 {
		fmt.Printf("[mbt] %d of %d interactive test(s) FAILED\n", failed, len(tests))
		return exitBuild
	}
	fmt.Printf("[mbt] all %d interactive test(s) passed\n", len(tests))
	return exitOK
}

// stageTSOFixtures loads the [tso.NAME] fixtures of the selected tests
// (mbt#243): one PDS per key, recreated, filled by a job of IEBGENER steps,
// the same load as mbt test --mvs. It returns per test key -> data set name.
func stageTSOFixtures(p *project.Project, tests [][2]string) (map[string]map[string]any, int) {
	out := map[string]map[string]any{}
	decl := rawTable(p.Raw, "tso")
	files := map[string]bool{}
	for _, t := range tests {
		files[t[0]] = true
	}
	var names []string
	for n := range decl {
		if !files[n] {
			if _, err := os.Stat(filepath.Join(p.Root, "test", "tso", strings.ToLower(n)+".lua")); err != nil {
				fmt.Fprintf(os.Stderr, "[mbt] ERROR: [tso.%s] names no test: there is no test/tso/%s.lua\n", n, strings.ToLower(n))
				return nil, exitConfig
			}
			continue // a test --only left out
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, t := range tests {
		out[t[0]] = map[string]any{}
	}
	if len(names) == 0 {
		return out, exitOK
	}
	cfg, c, code := connect(p.Root, selectedTarget, true)
	if code != exitOK {
		return nil, code
	}
	hlq, name := cfg.HLQ(), deploy.MVSQualifier(p.Name)
	fx := map[string][]mvstest.Fixture{}
	var withFx []string
	for _, n := range names {
		t, _ := decl[n].(map[string]any)
		keys, _ := t["fixtures"].(map[string]any)
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var decls []mvstest.FixtureDecl
		for _, k := range ks {
			decls = append(decls, mvstest.FixtureDecl{DD: k, Members: anyStrs(keys[k])})
		}
		if len(decls) == 0 {
			continue
		}
		blocks, err := mvstest.BuildFixtures(p.Root, hlq, name, n, decls)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mbt] ERROR: fixture: %v\n", err)
			return nil, exitConfig
		}
		fx[n] = blocks
		withFx = append(withFx, n)
		for _, b := range blocks {
			out[n][b.DD] = b.PDS
		}
	}
	if len(withFx) == 0 {
		return out, exitOK
	}
	logf := func(f string, a ...any) { fmt.Printf("[mbt] "+f+"\n", a...) }
	if t, err := mvstest.AllocFixtures(c, hlq, name, withFx, fx, logf); err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: fixture alloc failed for %s: %v\n", t, err)
		return nil, exitMVS
	}
	jcl, steps, order := mvstest.GenFixtureJob(mvsmf.Jobcard("MBTTSOFX", cfg.JobClass(), cfg.MsgClass(), "MBT TSO FIXTURES"), withFx, fx)
	res, err := c.Submit(jcl, mvsmf.SubmitOptions{Timeout: time.Duration(120+10*len(steps)) * time.Second})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: fixture load job: %v\n", err)
		return nil, exitMVS
	}
	job := res.JobName
	if job == "" {
		job = "MBTTSOFX"
	}
	if h, d := mvstest.FixtureFailures(res.Spool, job, steps, order, res.SpoolErrors); h != "" {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %s %s: %s\n", job, res.JobID, h)
		for _, l := range d {
			fmt.Fprintf(os.Stderr, "[mbt]        %s\n", l)
		}
		return nil, exitDataset
	}
	logf("Fixtures loaded: %s %s, %d member(s)", job, res.JobID, len(steps))
	return out, exitOK
}

func anyStrs(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		var out []string
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// testLibrary is the library mbt test --mvs deploys to: [deploy]
// test_target, or the versioned default (mvstest.Run).
func testLibrary(p *project.Project, hlq string) string {
	if t, _ := rawTable(p.Raw, "test_deploy")["target"].(string); t != "" {
		return t
	}
	v, _ := version.Parse(p.Version)
	return fmt.Sprintf("%s.%s.%s.TESTLIB", hlq, deploy.MVSQualifier(p.Name), v.VRM())
}
