// Command mbt is the MVS Build Tool, version 3.
//
// Phase 3 of the mbt 3 proposal (internals/mbt-3-design.md): the cc370 host
// path.  It reads an mbt.toml (schema 3) or an mbt v2 project.toml; from the
// latter its output is compared byte for byte with mbt v2's.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mvslovers/mbt/include"
	"github.com/mvslovers/mbt/internal/build"
	"github.com/mvslovers/mbt/internal/compiledb"
	"github.com/mvslovers/mbt/internal/config"
	"github.com/mvslovers/mbt/internal/console"
	"github.com/mvslovers/mbt/internal/deploy"
	"github.com/mvslovers/mbt/internal/deps"
	"github.com/mvslovers/mbt/internal/dist"
	"github.com/mvslovers/mbt/internal/ext"
	"github.com/mvslovers/mbt/internal/hosttest"
	"github.com/mvslovers/mbt/internal/launch"
	"github.com/mvslovers/mbt/internal/migrate"
	"github.com/mvslovers/mbt/internal/moddata"
	"github.com/mvslovers/mbt/internal/mvsmf"
	"github.com/mvslovers/mbt/internal/mvstest"
	"github.com/mvslovers/mbt/internal/pkg"
	"github.com/mvslovers/mbt/internal/plugins"
	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/release"
	"github.com/mvslovers/mbt/internal/stamp"
	"github.com/mvslovers/mbt/internal/target"
	"github.com/mvslovers/mbt/internal/toolchain"
	"github.com/mvslovers/mbt/internal/tools"
	"github.com/mvslovers/mbt/internal/version"
)

// Exit codes (spec section 11.1).
const (
	exitOK       = 0
	exitBuild    = 1
	exitConfig   = 2
	exitMVS      = 4
	exitInternal = 99
)

// buildInfo is the commit and date the binary was built from, as Go records
// them from git (" (d762ac6, 2026-10-07)"; "+dirty" for uncommitted
// changes): a prerelease keeps its version while its tag moves, so the
// version alone does not say whether a fix is in.
func buildInfo() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev, at, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if len(at) >= 10 {
		at = ", " + at[:10]
	}
	return " (" + rev + dirty + at + ")"
}

// mbtVersion is set by the release build: -ldflags "-X main.mbtVersion=3.0.1".
var mbtVersion = "3.0.0-dev"

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
  test --mvs [--only NAME]... [--no-deploy] [--target NAME] [--linklib DSN] [-v]
                                      build the test modules and run them on MVS
  test --tso [--only NAME]... [--target NAME] [-v]
                                      run test/tso/*.lua interactively over TN3270, one TSO logon each
  check                               every test suite: the host first, then MVS
  migrate [--dry-run]                 convert project.toml into mbt.toml (schema 3)
  deploy [--target NAME] [--linklib DSN] [--module M]... [--dry-run] [-v]
                                      pack the built modules and RECEIVE them on MVS
  compiledb                           write compile_commands.json for clangd
  doctor                              check the toolchain, the sysroot and the MVS connection
  release VERSION [--next V]          release VERSION-dev as VERSION: bump, tag, push, then bump to V
  prerelease                          (re)tag the current -dev version and push the tag
  run [NAME] [-v] [--dry-run] [-- ARGS...]
                                      run a command or task of mbt/init.lua; without NAME: list them
  clean                               remove build/ and dist/ (keeps staged deps)
  distclean                           clean, and remove .mbt/ (deps, tools, state; keeps mbt.lock)
  target list | ping [NAME] [--wait SEC] | info [NAME] [--wait SEC] | console [NAME] -- CMD | import .env --name NAME
                                      the MVS systems in ~/.mbt/targets.toml
  version                             print mbt's version

mbt.toml may pin the mbt to run: [toolchain] mbt = "3.0" (any 3.0.x) or "3.0.2".
Another version is fetched into ~/.mbt/versions/ and run; MBT_NO_SWITCH=1 keeps this one.
`)
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitConfig
	}
	switch args[0] {
	case "help", "-h", "--help":
	default:
		// the mbt the project pins, if this is not it (design §5)
		if handled, code := launch.Switch(mbtVersion, args, launch.Options{}); handled {
			return code
		}
	}
	stop := onSignal()
	defer stop()
	defer closeSession()
	code := dispatch(args)
	if code != exitOK && engine != nil {
		failure := lastErr
		if failure == nil {
			failure = fmt.Errorf("exit code %d", code)
		}
		engine.Failure(args[0], failure)
	}
	return code
}

// noArgs are the commands that take no arguments: anything after them is
// refused rather than ignored -- "mbt doctor --help" must not run doctor
// (which logs on to MVS) instead of printing help.
var noArgs = map[string]bool{"clean": true, "distclean": true, "ci-info": true, "dist": true,
	"check": true, "compiledb": true, "doctor": true, "version": true, "--version": true}

func dispatch(args []string) int {
	if noArgs[args[0]] && len(args) > 1 {
		switch args[1] {
		case "-h", "--help", "help":
			usage()
			return exitOK
		}
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: mbt %s takes no arguments (got %s)\n", args[0], strings.Join(args[1:], " "))
		return exitConfig
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "target":
		return cmdTarget(args[1:])
	case "clean":
		return cmdClean(false)
	case "distclean":
		return cmdClean(true)
	case "release":
		return cmdRelease(args[1:], false)
	case "prerelease":
		return cmdRelease(args[1:], true)
	case "ci-info":
		return cmdCIInfo()
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
		fmt.Println("mbt", mbtVersion+buildInfo())
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
	if code := before(p, "build", verbose, false); code != exitOK {
		return nil, code
	}
	if engine.Active() {
		// a task may have written sources the patterns now match
		if p, err = project.Load(root); err != nil {
			return nil, fail(err)
		}
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
	if code := after(p, "build", map[string]any{"modules": unitNames(p.Modules), "tests": unitNames(p.Tests)}); code != exitOK {
		return nil, code
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
	if code := before(p, "dist", false, false); code != exitOK {
		return code
	}
	if p.DistError != "" {
		if v, err := version.Parse(p.Version); err == nil && v.IsPre() {
			// a development level after a release: nothing to install from it
			fmt.Fprintf(os.Stderr, "[mbt] WARNING: no SMP package for %s: %s\n", p.Version, p.DistError)
			return exitOK
		}
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
	return after(p, "dist", map[string]any{"artifacts": distFiles(p.Root, "-dist.")})
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
		fmt.Fprintf(os.Stderr, "[mbt] NOTE: %s\n", n) // stderr: --dry-run output stays a valid mbt.toml
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

func cmdRelease(args []string, pre bool) int {
	fl := flag.NewFlagSet("release", flag.ContinueOnError)
	next := fl.String("next", "", "the development version after the release (default: patch+1-dev)")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	if code := before(p, "release", false, false); code != exitOK {
		return code
	}
	o := release.Options{Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }}
	if pre {
		if fl.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "[mbt] ERROR: prerelease takes no version -- it tags the current one")
			return exitConfig
		}
		err = release.Prerelease(root, p, o)
	} else {
		if fl.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "[mbt] ERROR: usage: mbt release VERSION [--next V]")
			return exitConfig
		}
		err = release.Release(root, p, fl.Arg(0), *next, o)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
		lastErr = err
		return exitConfig
	}
	v := p.Version
	if !pre {
		v = fl.Arg(0)
	}
	return after(p, "release", map[string]any{"version": v, "tag": "v" + v, "prerelease": pre})
}

// cmdCIInfo prints what the reusable workflows need, one KEY=VALUE per line
// (suited to >> "$GITHUB_ENV"): the project, its version, and the git refs
// of the toolchain a release is built with -- a version names its tag,
// anything else is a ref, nothing is main.
func cmdCIInfo() int {
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	tc, _ := p.Raw["toolchain"].(map[string]any)
	ref := func(repo string) string {
		v, _ := tc[repo].(string)
		v = strings.TrimSpace(v)
		if v == "" {
			return "main"
		}
		if _, err := version.Parse(v); err == nil {
			return "v" + v
		}
		return v
	}
	pin, _ := launch.Pin(root)
	fmt.Printf("PROJECT_NAME=%s\nPROJECT_VERSION=%s\nPROJECT_FILE=%s\nCC370_REF=%s\nLIBC370_REF=%s\nMBT_PIN=%s\n",
		p.Name, p.Version, p.File, ref("cc370"), ref("libc370"), pin)
	return exitOK
}

// engine is the Lua of this invocation (mbt/init.lua, ~/.mbt/init.lua),
// loaded once; lastErr is what on_failure hooks are told.
var (
	engine  *ext.Engine
	lastErr error
)

// exts loads the extensions of an mbt.toml project. A v2 project gets none:
// its mbt/ is the submodule, and a v2 build must not change.
func exts(p *project.Project, verbose, dry bool) (*ext.Engine, error) {
	if engine != nil {
		return engine, nil
	}
	if p.Schema != 3 {
		engine = &ext.Engine{}
		return engine, nil
	}
	home := os.Getenv("MBT_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".mbt")
		}
	}
	tl, err := tools.Declared(p.Raw)
	if err != nil {
		return nil, err
	}
	pd, err := plugins.Declared(p.Raw)
	if err != nil {
		return nil, err
	}
	// a build uses what mbt deps staged; it never reaches out for a plugin
	staged, err := plugins.Resolve(p.Root, pd, plugins.Options{Offline: true, Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }})
	if err != nil {
		return nil, err
	}
	var pls []ext.Plugin
	for _, x := range staged {
		pls = append(pls, ext.Plugin{Key: x.Key, Dir: x.Dir, API: x.API, Exec: x.Exec})
	}
	e, err := ext.Load(ext.Options{Plugins: pls, Root: p.Root, Home: home, Version: mbtVersion, Verbose: verbose, DryRun: dry,
		Project: ext.Project{Name: p.Name, Version: p.Version, Modules: unitNames(p.Modules), Tests: unitNames(p.Tests)},
		Tools:   tl, ToolOpt: tools.Options{Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }},
		Target: func() (map[string]any, error) {
			t, err := chooseTarget(p.Root, selectedTarget)
			if err != nil {
				return nil, err
			}
			return targetInfo(t), nil
		},
		MVS: func() (ext.MVS, error) {
			_, c, code := connect(p.Root, selectedTarget, true)
			if code != exitOK {
				return nil, lastErrOr("no mvsMF session")
			}
			return c, nil
		},
		Console: func(cmd string) ([]string, string, error) {
			ch, err := consoleChain(p.Root, selectedTarget)
			if err != nil {
				return nil, "", err
			}
			r, err := ch.Send(cmd)
			return r.Lines, r.Channel, err
		}})
	if err != nil {
		return nil, err
	}
	engine = e
	return e, nil
}

func before(p *project.Project, point string, verbose, dry bool) int {
	e, err := exts(p, verbose, dry)
	if err != nil {
		return fail(err)
	}
	if err := e.Before(point); err != nil {
		return fail(err)
	}
	return exitOK
}

func after(p *project.Project, point string, result map[string]any) int {
	e, err := exts(p, false, false)
	if err != nil {
		return fail(err)
	}
	if err := e.After(point, result); err != nil {
		return fail(err)
	}
	return exitOK
}

func unitNames(us []*project.Unit) []string {
	var n []string
	for _, u := range us {
		n = append(n, u.Name)
	}
	return n
}

// distFiles lists dist/ (only names containing part, when given).
func distFiles(root, part string) []string {
	var out []string
	entries, _ := os.ReadDir(filepath.Join(root, "dist"))
	for _, e := range entries {
		if !e.IsDir() && (part == "" || strings.Contains(e.Name(), part)) {
			out = append(out, "dist/"+e.Name())
		}
	}
	return out
}

// selectedTarget is the --target of this command ("" = the default);
// targetWarned: the fallback to the mbt 2 settings was announced once.
var (
	selectedTarget string
	targetWarned   bool
)

// The one mvsMF session of this invocation, and the target it belongs to.
var (
	session       *mvsmf.Client
	sessionTarget *target.Target
	sessionCfg    *config.Config
)

func lastErrOr(s string) error {
	if lastErr != nil {
		return lastErr
	}
	return errors.New(s)
}

// chooseTarget picks the target (target.Select) and says when it falls back
// to the mbt 2 settings.
func chooseTarget(root, name string) (*target.Target, error) {
	f, err := target.Load(target.Home())
	if err != nil {
		return nil, err
	}
	legacy := func() *target.Target {
		c := config.Load(root)
		return target.Legacy(c.Get)
	}
	t, warn, err := target.Select(name, f, os.Getenv, legacy)
	if err != nil {
		return nil, err
	}
	if warn != "" && !targetWarned {
		targetWarned = true
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: %s\n", warn)
	}
	t.Defaults()
	return t, nil
}

// connect resolves the target into the settings deploy and test-mvs read
// and, with login, the session every call of this run shares.
func connect(root, name string, login bool) (*config.Config, *mvsmf.Client, int) {
	if session != nil {
		return sessionCfg, session, exitOK
	}
	t, err := chooseTarget(root, name)
	if err != nil {
		return nil, nil, fail(err)
	}
	host, port, err := t.MVSMF.HostPort()
	if err != nil {
		return nil, nil, fail(err)
	}
	// without a logon (a dry run) the password is not needed, so it is not
	// asked for: a keychain prompt or an unset variable must not stop it
	pw := ""
	if login {
		if pw, err = t.MVSMF.Password.Resolve(); err != nil {
			return nil, nil, fail(err)
		}
	}
	cfg := config.Fixed(map[string]string{"mvs.host": host, "mvs.port": strconv.Itoa(port), "mvs.user": t.MVSMF.User,
		"mvs.pass": pw, "mvs.hlq": t.HLQ, "mvs.deps_volume": t.Volume, "jes.jobclass": t.JobClass, "jes.msgclass": t.MsgClass})
	if !login {
		return cfg, nil, exitOK
	}
	c := mvsmf.New(host, port, t.MVSMF.User, pw)
	if err := c.Login(); err != nil {
		lastErr = err
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: target %s: %v\n", t.Name, err)
		return nil, nil, exitMVS
	}
	session, sessionTarget, sessionCfg = c, t, cfg
	return cfg, c, exitOK
}

// consoleChain is the target's console order: mvsMF through the session,
// the Hercules web console with its own credentials.
func consoleChain(root, name string) (*console.Chain, error) {
	t, err := chooseTarget(root, name)
	if err != nil {
		return nil, err
	}
	ch := &console.Chain{Log: func(s string) { fmt.Fprintf(os.Stderr, "[mbt] WARNING: %s\n", s) }}
	for _, c := range t.Console {
		switch c {
		case "mvsmf":
			ch.Channels = append(ch.Channels, &console.MVSMF{Client: func() (*mvsmf.Client, error) {
				_, cl, code := connect(root, name, true)
				if code != exitOK {
					return nil, lastErrOr("no mvsMF session")
				}
				return cl, nil
			}})
		case "hercules":
			pw := ""
			if t.Hercules.Password.Set() {
				if pw, err = t.Hercules.Password.Resolve(); err != nil {
					return nil, err
				}
			}
			ch.Channels = append(ch.Channels, &console.Hercules{URL: t.Hercules.URL, User: t.Hercules.User, Password: pw})
		}
	}
	return ch, nil
}

// closeSession logs off; it runs at the end of every command and on Ctrl-C.
func closeSession() {
	if session != nil {
		if err := session.Logout(); err != nil {
			fmt.Fprintf(os.Stderr, "[mbt] WARNING: mvsMF logoff: %v (httpd expires the session by itself)\n", err)
		}
		session = nil
	}
}

// onSignal logs off on SIGINT/SIGTERM before mbt goes.
func onSignal() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			closeSession()
			os.Exit(130)
		case <-done:
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}

// targetInfo is what Lua may see of a target: no passwords.
func targetInfo(t *target.Target) map[string]any {
	m := map[string]any{"name": t.Name, "hlq": t.HLQ, "volume": t.Volume, "jobclass": t.JobClass, "msgclass": t.MsgClass,
		"console": t.Console, "mvsmf": map[string]any{"url": t.MVSMF.URL, "user": t.MVSMF.User}}
	if t.Hercules != nil {
		m["hercules"] = map[string]any{"url": t.Hercules.URL, "user": t.Hercules.User}
	}
	if t.SSH != nil {
		m["ssh"] = map[string]any{"host": t.SSH.Host, "user": t.SSH.User, "port": t.SSH.Port}
	}
	if t.TN3270 != nil {
		m["tn3270"] = map[string]any{"host": t.TN3270.Host, "port": t.TN3270.Port, "tls": t.TN3270.TLS, "user": t.TN3270.User}
	}
	return m
}

// cmdTarget: mbt target list | ping | info | import
func cmdTarget(args []string) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	root, _ := os.Getwd()
	switch args[0] {
	case "list":
		f, err := target.Load(target.Home())
		if err != nil {
			return fail(err)
		}
		if len(f.Targets) == 0 {
			fmt.Printf("[mbt] no targets: %s does not exist or defines none -- 'mbt target import .env --name NAME' starts one\n", f.Path)
		}
		for _, n := range f.Names() {
			t := f.Targets[n]
			mark := " "
			if t.Default {
				mark = "*"
			}
			var extra []string
			if t.Hercules != nil {
				extra = append(extra, "hercules")
			}
			if t.SSH != nil {
				extra = append(extra, "ssh")
			}
			if t.TN3270 != nil {
				extra = append(extra, "tn3270")
			}
			fmt.Printf("%s %-12s %-28s %-10s password: %-18s %s\n", mark, n, t.MVSMF.URL, t.MVSMF.User, t.MVSMF.Password.Source(), strings.Join(extra, " "))
		}
		if t, ok := target.FromEnv(os.Getenv); ok {
			fmt.Printf("  %-12s %-28s %-10s (from MBT_TARGET_*, used unless --target names another)\n", "env", t.MVSMF.URL, t.MVSMF.User)
		}
		return exitOK
	case "ping", "info":
		fl := flag.NewFlagSet("target "+args[0], flag.ContinueOnError)
		wait := fl.Int("wait", 0, "wait up to this many seconds for mvsMF to answer")
		if err := fl.Parse(flagsFirst(args[1:])); err != nil {
			return exitConfig
		}
		name := fl.Arg(0)
		t, err := chooseTarget(root, name)
		if err != nil {
			return fail(err)
		}
		if *wait > 0 {
			fmt.Printf("[mbt] target %s: waiting up to %ds for %s ...\n", t.Name, *wait, t.MVSMF.URL)
			p, took := target.Wait(t, time.Duration(*wait)*time.Second, time.Sleep)
			if !p.OK {
				fmt.Fprintf(os.Stderr, "[mbt] ERROR: target %s: mvsMF not answering after %s: %s\n", t.Name, took.Round(time.Second), p.Detail)
				return exitMVS
			}
			fmt.Printf("[mbt] target %s: mvsMF answering after %s\n", t.Name, took.Round(time.Second))
		}
		if args[0] == "ping" {
			ok := true
			for _, p := range target.Ping(t, 5*time.Second) {
				fmt.Printf("[mbt] %-10s %-9s %-28s %s\n", t.Name, p.Access, p.Addr, p.Detail)
				ok = ok && (p.OK || p.Access != "mvsmf")
			}
			if !ok {
				return exitMVS
			}
			return exitOK
		}
		selectedTarget = name
		_, c, code := connect(root, name, true)
		if code != exitOK {
			return code
		}
		st, body, err := c.Request("GET", "/info", "", nil)
		if err != nil {
			return fail(err)
		}
		var info map[string]any
		json.Unmarshal(body, &info)
		fmt.Printf("[mbt] %-10s %-9s %-28s logged on as %s (HTTP %d) -- %v %v, %v\n", t.Name, "mvsmf", t.MVSMF.URL, t.MVSMF.User, st,
			info["zosmf_full_version"], info["zosmf_hostname"], info["zos_version"])
		if t.Hercules != nil {
			p := target.HerculesInfo(t.Hercules, 5*time.Second)
			fmt.Printf("[mbt] %-10s %-9s %-28s %s\n", t.Name, p.Access, p.Addr, p.Detail)
		}
		for _, p := range target.Ping(t, 5*time.Second) {
			if p.Access == "tn3270" || p.Access == "ssh" {
				fmt.Printf("[mbt] %-10s %-9s %-28s %s\n", t.Name, p.Access, p.Addr, p.Detail)
			}
		}
		return exitOK
	case "console":
		// mbt target console [NAME] -- CMD...
		rest := args[1:]
		name, cmd := "", []string{}
		for i, a := range rest {
			if a == "--" {
				cmd = rest[i+1:]
				if i > 0 {
					name = rest[0]
				}
				break
			}
		}
		if len(cmd) == 0 {
			fmt.Fprintln(os.Stderr, "[mbt] usage: mbt target console [NAME] -- COMMAND")
			return exitConfig
		}
		selectedTarget = name
		ch, err := consoleChain(root, name)
		if err != nil {
			return fail(err)
		}
		r, err := ch.Send(strings.Join(cmd, " "))
		if err != nil {
			return fail(err)
		}
		fmt.Printf("[mbt] console (%s): %s\n", r.Channel, strings.Join(cmd, " "))
		for _, l := range r.Lines {
			fmt.Println("  " + l)
		}
		return exitOK
	case "import":
		fl := flag.NewFlagSet("target import", flag.ContinueOnError)
		name := fl.String("name", "", "the new target's name")
		if err := fl.Parse(flagsFirst(args[1:])); err != nil {
			return exitConfig
		}
		if fl.NArg() != 1 || *name == "" {
			fmt.Fprintln(os.Stderr, "[mbt] usage: mbt target import .env --name NAME")
			return exitConfig
		}
		env, err := target.ReadDotenv(fl.Arg(0))
		if err != nil {
			return fail(err)
		}
		// mbt 2 merged the environment and ~/.mbt/config.toml under the
		// .env; what the file leaves out comes from there, as mbt 2 used it
		// (the password excepted: one from the environment stays there)
		out := config.Outside()
		var filled []string
		for _, k := range []string{"mvs.host", "mvs.port", "mvs.user", "mvs.hlq", "mvs.deps_volume", "jes.jobclass", "jes.msgclass"} {
			e := config.EnvName(k)
			if _, ok := env[e]; ok {
				continue
			}
			if v, src := out.Source(k); src != "default" && v != "" {
				env[e] = v
				filled = append(filled, fmt.Sprintf("%s = %s (from %s)", e, v, src))
			}
		}
		for _, f := range filled {
			fmt.Printf("[mbt] not in %s, taken as mbt 2 used it: %s\n", fl.Arg(0), f)
		}
		shown, skipped, err := target.Import(target.Home(), *name, env)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("[mbt] added to %s:\n%s", target.Path(target.Home()), shown)
		for _, s := range skipped {
			fmt.Printf("[mbt] not carried over: %s\n", s)
		}
		return exitOK
	}
	fmt.Fprintln(os.Stderr, "[mbt] usage: mbt target list | ping [NAME] [--wait SEC] | info [NAME] [--wait SEC] | console [NAME] -- CMD | import .env --name NAME")
	return exitConfig
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// flagsFirst moves "-x v" / "--x=v" ahead of the positional arguments, so
// "mbt target import .env --name X" parses as written (Go's flag package
// stops at the first non-flag).
func flagsFirst(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

// cmdRun: mbt run [NAME] [-v] [--dry-run] [-- ARGS...]
func cmdRun(args []string) int {
	var extra []string
	for i, a := range args {
		if a == "--" {
			args, extra = args[:i], append([]string{}, args[i+1:]...)
			break
		}
	}
	fl := flag.NewFlagSet("run", flag.ContinueOnError)
	verbose := fl.Bool("v", false, "show each program run and its output")
	dry := fl.Bool("dry-run", false, "run the Lua, but no program and no write")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	e, err := exts(p, *verbose, *dry)
	if err != nil {
		return fail(err)
	}
	if fl.NArg() == 0 {
		cmds := e.Commands()
		if len(cmds) == 0 {
			fmt.Println("[mbt] no commands or tasks: mbt/init.lua defines none (or there is none)")
			return exitOK
		}
		fmt.Println("[mbt] commands and tasks (mbt run NAME [-- ARGS]):")
		for _, c := range cmds {
			fmt.Printf("  %-20s %s\n", c[0], c[1])
		}
		return exitOK
	}
	if fl.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "[mbt] ERROR: usage: mbt run NAME [-v] [--dry-run] [-- ARGS...]")
		return exitConfig
	}
	if err := e.Run(fl.Arg(0), extra); err != nil {
		return fail(err)
	}
	return exitOK
}

// cmdClean: build/ and dist/ go; distclean takes .mbt/ too. mbt.lock stays.
func cmdClean(dist bool) int {
	root, _ := os.Getwd()
	if _, err := project.Find(root); err != nil {
		return fail(err)
	}
	dirs := []string{"build", "dist"}
	if dist {
		dirs = append(dirs, ".mbt")
	}
	for _, d := range dirs {
		if err := os.RemoveAll(filepath.Join(root, d)); err != nil {
			return fail(err)
		}
	}
	fmt.Printf("[mbt] Removed %s\n", strings.Join(dirs, ", "))
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
	if code := before(p, "package", false, false); code != exitOK {
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
	if code := after(p, "package", map[string]any{"artifacts": distFiles(p.Root, "")}); code != exitOK {
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
	tso := fl.Bool("tso", false, "run the interactive tests in test/tso/*.lua over TN3270")
	noDeploy := fl.Bool("no-deploy", false, "with --mvs: reuse the TESTLIB already there")
	targetName := fl.String("target", "", "with --mvs: the MVS system (a name in ~/.mbt/targets.toml)")
	linklib := fl.String("linklib", "", "with --mvs: the runtime load library the tests run against")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	selectedTarget = *targetName
	if *tso {
		var rest []string
		for _, o := range only {
			rest = append(rest, "--only", o)
		}
		if *targetName != "" {
			rest = append(rest, "--target", *targetName)
		}
		if *verbose {
			rest = append(rest, "-v")
		}
		return cmdTestTSO(rest)
	}
	if *mvs {
		return cmdTestMVS(only, *noDeploy, *linklib, *verbose)
	}
	root, _ := os.Getwd()
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	if code := before(p, "test", *verbose, false); code != exitOK {
		return code
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
	if code := after(p, "test", map[string]any{"kind": "host", "passed": ok}); code != exitOK {
		return code
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
	if code := before(p, "test", verbose, false); code != exitOK {
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
	cfg, client, code := connect(p.Root, selectedTarget, true)
	if code != exitOK {
		return code
	}
	code = mvstest.Run(mvstest.Options{Root: p.Root, BuildDir: "build", LD: t.LD, Project: p.Name, Version: p.Version,
		Tests: tests, TestTarget: tt, LinkTarget: target, ProjectTarget: pt, Only: only, NoDeploy: noDeploy,
		Verbose: verbose, Out: os.Stdout, Err: os.Stderr, Config: cfg, Client: client})
	if code == mvstest.ExitOK || code == mvstest.ExitFailed {
		if c := after(p, "test", map[string]any{"kind": "mvs", "passed": code == mvstest.ExitOK}); c != exitOK {
			return c
		}
	}
	return code
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

// doctorCC370 reports the installed cc370 and holds it against [toolchain]
// cc370 when that names a release: older is a warning, since a release is
// built with the pinned one and a local build only approximates it.
func doctorCC370(want string) {
	out, err := exec.Command("cc370", "--version").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: cc370 --version: %v\n", err)
		return
	}
	f := strings.Fields(string(out)) // "cc370 1.4.0 (2821ebb), based on GCC 3.4.6"
	if len(f) < 2 {
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: cannot read cc370's version from %q\n", strings.TrimSpace(string(out)))
		return
	}
	have, herr := version.Parse(f[1])
	pin, perr := version.Parse(strings.TrimPrefix(want, "v"))
	switch {
	case want == "" || perr != nil:
		fmt.Printf("[mbt] cc370 %s (no release pinned in [toolchain] cc370)\n", f[1])
	case herr != nil:
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: cc370 %s: not a version to compare with [toolchain] cc370 = %s\n", f[1], want)
	case version.Compare(have, pin) < 0:
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: cc370 %s is older than [toolchain] cc370 = %s, which release builds use\n", f[1], want)
	default:
		fmt.Printf("[mbt] cc370 %s (>= %s)\n", f[1], want)
	}
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
		// libc370's files, then cc370's: the compiler's prologue macros and
		// its runtime live in the same sysroot, and replacing a directory
		// there to install one of them silently drops the other's
		var missing, missingCC []string
		for _, f := range []string{"libc.a", "crtm.o"} {
			if _, err := os.Stat(filepath.Join(t.LibcDir, f)); err != nil {
				missing = append(missing, f)
			}
		}
		for _, f := range []string{"macros/pdptop.copy", "macros/pdpprlg.macro", "macros/pdpepil.macro", "lib/libcc370rt.a"} {
			if _, err := os.Stat(filepath.Join(t.Sysroot, f)); err != nil {
				missingCC = append(missingCC, f)
			}
		}
		if len(missing) > 0 {
			bad("sysroot %s incomplete, missing libc370's %s -- reinstall libc370", t.Sysroot, strings.Join(missing, ", "))
		}
		if len(missingCC) > 0 {
			bad("sysroot %s incomplete, missing cc370's %s -- reinstall cc370 (nothing assembles without its macros)", t.Sysroot, strings.Join(missingCC, ", "))
		}
		if len(missing)+len(missingCC) == 0 {
			fmt.Printf("[mbt] sysroot: %s (libc370: libc.a, crtm.o; cc370: macros, libcc370rt.a OK)\n", t.Sysroot)
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
		wantCC := ""
		if perr == nil {
			if tc, ok := p.Raw["toolchain"].(map[string]any); ok {
				wantCC, _ = tc["cc370"].(string)
			}
		}
		doctorCC370(strings.TrimSpace(wantCC))
	}
	if perr != nil {
		fmt.Fprintf(os.Stderr, "[mbt] WARNING: project file not loaded, skipping MVS checks (%v)\n", perr)
		failed++
	} else {
		fmt.Printf("[mbt] %s valid: %s v%s\n", p.File, p.Name, p.Version)
		tg, err := chooseTarget(root, "")
		if err != nil {
			bad("%v", err)
		} else {
			fmt.Printf("[mbt] target %s (from %s)\n", tg.Name, tg.Source)
			fmt.Printf("  mvsMF     %s as %s, password: %s\n", tg.MVSMF.URL, tg.MVSMF.User, tg.MVSMF.Password.Source())
			fmt.Printf("  hlq %s, volume %s, jobclass %s, msgclass %s, console: %s\n", tg.HLQ, orDash(tg.Volume), tg.JobClass, tg.MsgClass, strings.Join(tg.Console, " -> "))
			for _, pr := range target.Ping(tg, 5*time.Second) {
				fmt.Printf("  %-9s %-28s %s\n", pr.Access, pr.Addr, pr.Detail)
				if pr.Access == "mvsmf" && !pr.OK {
					fmt.Fprintln(os.Stderr, "[mbt] WARNING: mvsMF not reachable (only needed for deploy and test --mvs)")
					failed++
				}
			}
			if _, _, code := connect(root, "", true); code == exitOK {
				fmt.Printf("[mbt] MVS logon valid: %s\n", tg.MVSMF.User)
			} else {
				failed++
			}
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
	targetName := fl.String("target", "", "the MVS system (a name in ~/.mbt/targets.toml)")
	linklib := fl.String("linklib", "", "deploy into this load library instead of [deploy] target")
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
	if len(p.Modules) == 0 {
		// a library project: its archive is a release asset, not a load module
		fmt.Printf("[mbt] nothing to deploy: %s builds no load modules (kind = %q)\n", p.Name, p.Type)
		return exitOK
	}
	selectedTarget = *targetName
	if code := before(p, "deploy", *verbose, *dry); code != exitOK {
		return code
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
	cfg, client, code := connect(root, *targetName, !*dry)
	if code != exitOK {
		return code
	}
	code = deploy.Run(deploy.Options{Root: root, BuildDir: "build", LD: t.LD, Project: p.Name, Version: p.Version,
		Modules: names, Target: *linklib, ProjectTarget: pt, Only: mods, DryRun: *dry, Verbose: *verbose,
		Out: os.Stdout, Err: os.Stderr, Config: cfg, Client: client})
	if code != exitOK {
		return code
	}
	lib := *linklib
	if lib == "" {
		lib = pt
	}
	deployed := names
	if len(mods) > 0 {
		deployed = mods
	}
	return after(p, "deploy", map[string]any{"library": lib, "modules": deployed, "dry_run": *dry})
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
	// [tools] are pinned and staged with the dependencies
	p, err := project.Load(root)
	if err != nil {
		return fail(err)
	}
	ts, err := tools.Declared(p.Raw)
	if err != nil {
		return fail(err)
	}
	for _, t := range ts {
		if _, err := tools.Ensure(root, t, tools.Options{Update: *update, Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }}); err != nil {
			return fail(err)
		}
	}
	// and [plugins]
	pd, err := plugins.Declared(p.Raw)
	if err != nil {
		return fail(err)
	}
	if _, err := plugins.Resolve(root, pd, plugins.Options{Update: *update, Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }}); err != nil {
		return fail(err)
	}
	return exitOK
}

func fail(err error) int {
	lastErr = err
	fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
	var xe *ext.Error
	var te *tools.Error
	var tge *target.Error
	var pe *plugins.Error
	if errors.As(err, &tge) {
		return exitConfig
	}
	if errors.As(err, &pe) {
		return 3
	}
	if errors.As(err, &xe) {
		if xe.Config {
			return exitConfig
		}
		return exitBuild
	}
	if errors.As(err, &te) {
		return 3
	}
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
