// Package hosttest builds and runs a project's dual tests natively on the
// host -- mbt v2's `make test-host` (scripts/mbttesthost.py).
//
// A test runs on the host when all its sources are .c and it does not set
// host = false.  The host build is the project's [build] cflags, every staged
// dependency's include dir, mbt's headers, .mbt (buildstamp.h) and the [host]
// extras: cc, cflags, sources (extra link sources), replace (a source swapped
// for a host stand-in).  The exit code is the contract; PASS:/FAIL: lines are
// counted for the summary.
package hosttest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Options for one run.
type Options struct {
	Root, BuildDir, IncludeDir string
	Only                       []string
	Verbose                    bool
	Out                        io.Writer
}

// Test is what the runner needs of a [[test]].
type Test struct {
	Name     string
	Host     bool // false: host = false
	Sources  []string
	Excludes []string
}

// Config is the project's host-relevant settings.
type Config struct {
	CFlags      []string // [build] cflags, as written
	HostCC      string
	HostCFlags  []string
	HostSources []string
	Replace     map[string]string
	Tests       []Test
	// Resolve expands source patterns the way the build does.
	Resolve func(patterns, exclude []string) []string
}

var (
	passRE = regexp.MustCompile(`(?m)^\s*PASS:`)
	failRE = regexp.MustCompile(`(?m)^\s*FAIL:`)
)

type row struct {
	ok           bool
	status       string
	npass, nfail int
}

// Run returns true when every host test passed.
func Run(c Config, o Options) (bool, error) {
	cc := c.HostCC
	if cc == "" {
		cc = os.Getenv("CC")
	}
	if cc == "" {
		cc = "cc"
	}
	flags := append([]string{}, c.CFlags...)
	incs, _ := filepath.Glob(filepath.Join(o.Root, ".mbt/deps/*/include"))
	sort.Strings(incs)
	for _, i := range incs {
		r, _ := filepath.Rel(o.Root, i)
		flags = append(flags, "-I", filepath.ToSlash(r))
	}
	flags = append(flags, "-I", o.IncludeDir, "-I", ".mbt")
	flags = append(flags, c.HostCFlags...)
	extra := c.Resolve(c.HostSources, nil)

	outdir := filepath.Join(o.BuildDir, "host")
	if err := os.MkdirAll(filepath.Join(o.Root, outdir), 0o755); err != nil {
		return false, err
	}
	want := map[string]bool{}
	for _, n := range o.Only {
		want[strings.ToUpper(n)] = true
	}
	rows := map[string]row{}
	var skipped []string
	totPass, totFail := 0, 0
	for _, t := range c.Tests {
		if t.Name == "" || (len(want) > 0 && !want[strings.ToUpper(t.Name)]) {
			continue
		}
		if !t.Host {
			skipped = append(skipped, fmt.Sprintf("%s (host = false)", t.Name))
			continue
		}
		var srcs []string
		seen := map[string]bool{}
		for _, s := range c.Resolve(t.Sources, t.Excludes) {
			if r, ok := c.Replace[s]; ok {
				s = r
			}
			if !seen[s] {
				seen[s] = true
				srcs = append(srcs, s)
			}
		}
		nonC := ""
		for _, s := range srcs {
			if !strings.HasSuffix(s, ".c") {
				nonC = s
				break
			}
		}
		if nonC != "" {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", t.Name, nonC))
			continue
		}
		out := filepath.Join(outdir, strings.ToLower(t.Name))
		args := append(append(append(append([]string{}, flags...), srcs...), extra...), "-o", out)
		if o.Verbose {
			fmt.Fprintf(o.Out, "[mbt] + %s %s\n", cc, strings.Join(args, " "))
		}
		comp := exec.Command(cc, args...)
		comp.Dir = o.Root
		var cerr bytes.Buffer
		comp.Stderr = &cerr
		if err := comp.Run(); err != nil {
			rows[t.Name] = row{false, "COMPILE", 0, 0}
			if o.Verbose {
				fmt.Fprintln(o.Out, strings.TrimRight(cerr.String(), "\n"))
			}
			continue
		}
		run := exec.Command(filepath.Join(o.Root, out))
		run.Dir = o.Root
		var spool bytes.Buffer
		run.Stdout, run.Stderr = &spool, &spool
		err := run.Run()
		rc := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc = ee.ExitCode()
		} else if err != nil {
			rc = -1
		}
		np, nf := len(passRE.FindAllIndex(spool.Bytes(), -1)), len(failRE.FindAllIndex(spool.Bytes(), -1))
		totPass += np
		totFail += nf
		rows[t.Name] = row{rc == 0, fmt.Sprintf("rc=%d", rc), np, nf}
	}

	fmt.Fprintf(o.Out, "\n  %-10s %-12s %-14s\n", "TEST", "RESULT", "ASSERTIONS")
	fmt.Fprintf(o.Out, "  %s %s %s\n", strings.Repeat("-", 10), strings.Repeat("-", 12), strings.Repeat("-", 14))
	var names []string
	for n := range rows {
		names = append(names, n)
	}
	sort.Strings(names)
	failed := 0
	for _, n := range names {
		r := rows[n]
		cell := "ok  " + r.status
		if !r.ok {
			failed++
			cell = "FAIL " + r.status
		}
		fmt.Fprintf(o.Out, "  %-10s %-12s %d pass / %d fail\n", n, cell, r.npass, r.nfail)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(o.Out, "\n  skipped (MVS-only): %s\n", strings.Join(skipped, ", "))
	}
	fmt.Fprintf(o.Out, "\n  %d host test(s) | assertions: %d PASS, %d FAIL\n", len(rows), totPass, totFail)
	if failed > 0 {
		fmt.Fprintf(o.Out, "[mbt] %d host test(s) FAILED\n", failed)
		return false, nil
	}
	fmt.Fprintln(o.Out, "[mbt] all host tests passed")
	return true, nil
}
