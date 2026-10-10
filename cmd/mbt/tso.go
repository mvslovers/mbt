package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/deploy"
	"github.com/mvslovers/mbt/internal/ext"
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
func (t *tsoSession) Key(name string) error {
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
	if err := e.Before("test"); err != nil {
		return fail(err)
	}
	info := map[string]any{"user": tg.TN3270.User, "testlib": testLibrary(p, tg.HLQ)}
	fmt.Printf("[mbt] %d interactive test(s) on %s (%s as %s)\n", len(tests), tg.Name, tg.TN3270.Addr(), tg.TN3270.User)
	failed := 0
	var rows []string
	for _, t := range tests {
		opt := tn3270.Options{}
		if *verbose {
			opt.Trace = os.Stderr
		}
		s, err := tn3270.Dial(context.Background(), tg.TN3270.Addr(), opt)
		if err == nil {
			err = e.TSOTest(t[1], &tsoSession{s: s, user: tg.TN3270.User, pass: pw}, info)
			s.Close()
		}
		res := "ok"
		if err != nil {
			failed++
			res = "FAIL"
			fmt.Fprintf(os.Stderr, "[mbt] %s: %v\n", t[0], err)
		}
		rows = append(rows, fmt.Sprintf("  %-10s %s", t[0], res))
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

// testLibrary is the library mbt test --mvs deploys to: [deploy]
// test_target, or the versioned default (mvstest.Run).
func testLibrary(p *project.Project, hlq string) string {
	if t, _ := rawTable(p.Raw, "test_deploy")["target"].(string); t != "" {
		return t
	}
	v, _ := version.Parse(p.Version)
	return fmt.Sprintf("%s.%s.%s.TESTLIB", hlq, deploy.MVSQualifier(p.Name), v.VRM())
}
