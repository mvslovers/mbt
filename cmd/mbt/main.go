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

	"github.com/mvslovers/mbt/include"
	"github.com/mvslovers/mbt/internal/build"
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
	root, _ := os.Getwd()

	p, err := project.LoadV2(root, "project.toml")
	if err != nil {
		return fail(err)
	}
	for _, w := range p.Warnings {
		fmt.Printf("[mbt] WARNING: %s\n", w)
	}
	t, err := toolchain.Find()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
		return exitConfig
	}
	for _, w := range t.Warnings {
		fmt.Printf("[mbt] WARNING: %s\n", w)
	}
	if _, err := stamp.Write(root, p.Name, p.Version); err != nil {
		return fail(err)
	}
	incDir := ".mbt/include"
	if err := writeHeaders(filepath.Join(root, incDir)); err != nil {
		return fail(err)
	}

	o := build.Options{IncludeDir: incDir, Jobs: *jobs, Verbose: *verbose, Only: fl.Args()}
	switch {
	case len(o.Only) > 0:
	case p.Type == "library":
		o.Lib = true
	default:
		o.Modules = true
		o.Lib = *all
	}
	if *all {
		o.Modules, o.Lib = p.Type != "library" || len(p.Modules) > 0, true
	}
	o.Tests = *tests
	if err := build.New(p, t, o).Run(); err != nil {
		return fail(err)
	}
	fmt.Println("[mbt] Build complete")
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

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "[mbt] ERROR: %v\n", err)
	var ce *project.ConfigError
	var be *build.Error
	switch {
	case errors.As(err, &ce):
		return exitConfig
	case errors.As(err, &be):
		return be.Code
	}
	return exitInternal
}
