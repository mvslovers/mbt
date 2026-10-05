// Command mbt is the MVS Build Tool, version 3.
//
// Phase 3 of the mbt 3 proposal (internals/mbt-3-design.md): the cc370 host
// path.  This first cut builds from an mbt v2 project.toml, so its output can
// be compared byte for byte with mbt v2's.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mvslovers/mbt/include"
	"github.com/mvslovers/mbt/internal/build"
	"github.com/mvslovers/mbt/internal/config"
	"github.com/mvslovers/mbt/internal/deploy"
	"github.com/mvslovers/mbt/internal/deps"
	"github.com/mvslovers/mbt/internal/dist"
	"github.com/mvslovers/mbt/internal/hosttest"
	"github.com/mvslovers/mbt/internal/moddata"
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
  deps [--update] [--locked]          resolve, download and stage dependencies
  module-data [--all]                 check for writable data in RENT/AC(1) modules
  package                             build, then write the release artifacts to dist/
  test [--only NAME]... [-v]          build and run the dual tests on the host
  deploy [--target DSN] [--module M]... [--dry-run] [-v]
                                      pack the built modules and RECEIVE them on MVS
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
	case "test":
		return cmdTest(args[1:])
	case "deploy":
		return cmdDeploy(args[1:])
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

	p, err := project.LoadV2(root, "project.toml")
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
		var sec int64
		if _, err := fmt.Sscan(e, &sec); err == nil {
			o.Mtime = time.Unix(sec, 0).UTC()
		}
	}
	if err := pkg.Run(p, o); err != nil {
		return fail(err)
	}
	if d, ok := p.Raw["distribution"].(map[string]any); ok && len(d) > 0 {
		var mods []dist.Module
		for _, m := range p.Modules {
			mods = append(mods, dist.Module{Name: m.Name, Aliases: m.Aliases})
		}
		err := dist.Build(p.Raw, p.Name, p.Version, mods, dist.Options{
			Root: p.Root, DistDir: "dist", BuildDir: "build", Mtime: o.Mtime,
			Log: func(s string) { fmt.Printf("[mbt] %s\n", s) },
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
			return exitConfig
		}
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
	p, err := project.LoadV2(root, "project.toml")
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
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	p, err := project.LoadV2(root, "project.toml")
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
	p, err := project.LoadV2(root, "project.toml")
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
	locked := fl.Bool("locked", false, "fail instead of changing mbt.lock")
	if err := fl.Parse(args); err != nil {
		return exitConfig
	}
	root, _ := os.Getwd()
	err := deps.Run(root, "project.toml", deps.Options{
		Update: *update, Locked: *locked,
		Log:  func(s string) { fmt.Printf("[mbt] %s\n", s) },
		Warn: func(s string) { fmt.Fprintf(os.Stderr, "[mbt] WARNING: %s\n", s) },
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
