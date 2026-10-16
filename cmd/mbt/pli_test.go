package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

// pl1c370 is looked up for PL/I anywhere the build compiles: a module, a
// test, the [lib] archive or [internal] -- a library project has no units.
func TestPL1SourceEverywhere(t *testing.T) {
	cases := map[string]*project.Project{
		"src/u.pli": {Modules: []*project.Unit{{Sources: []string{"src/a.c", "src/u.pli"}}}},
		"lib/l.pl1": {Lib: &project.Lib{Sources: []string{"lib/x.c", "lib/l.pl1"}}},
		"int/i.pli": {Internal: []string{"int/i.pli"}},
		"":          {Modules: []*project.Unit{{Sources: []string{"src/a.c", "asm/b.asm"}}}, Lib: &project.Lib{Sources: []string{"x.c"}}},
	}
	for want, p := range cases {
		if got := pl1Source(p); got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	}
}

// Both extensions, case-insensitively; nothing else -- COBOL and plain C/asm
// in particular must not be taken for PL/I.
func TestIsPLI(t *testing.T) {
	yes := []string{"a.pli", "a.pl1", "dir/B.PLI", "X.Pl1", "src/f.PLi"}
	no := []string{"a.c", "a.cbl", "a.cob", "a.asm", "a.s", "a.pl", "a", "a.pli.c"}
	for _, s := range yes {
		if !project.IsPLI(s) {
			t.Errorf("IsPLI(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if project.IsPLI(s) {
			t.Errorf("IsPLI(%q) = true, want false", s)
		}
	}
}

// findPL1C's three branches: the MBT_PL1C370 override, the sysroot's sibling
// bin, and nothing-anywhere (a ConfigError that names the PL/I source).
func TestFindPL1C(t *testing.T) {
	p := &project.Project{Modules: []*project.Unit{{Sources: []string{"src/u.pli"}}}}

	// 1. MBT_PL1C370 names an executable (here an absolute path to one).
	t.Run("env", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "mypl1c")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MBT_PL1C370", bin)
		got, err := findPL1C(p, "")
		if err != nil || got != bin {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, bin)
		}
	})

	// 2. The sysroot's sibling bin holds pl1c370 (sysroot is "<bindir>/../cc370",
	// so the compiler is <bindir>/pl1c370). The sysroot branch stats a file; it
	// does not require the execute bit, so a plain 0644 file must satisfy it.
	t.Run("sysroot", func(t *testing.T) {
		t.Setenv("MBT_PL1C370", "")
		root := t.TempDir()
		binDir := filepath.Join(root, "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(binDir, "pl1c370")
		if err := os.WriteFile(want, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := findPL1C(p, filepath.Join(root, "cc370"))
		if err != nil || got != want {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, want)
		}
	})

	// 3. Nothing anywhere -> a ConfigError, so the build fails with exit 2.
	t.Run("missing", func(t *testing.T) {
		t.Setenv("MBT_PL1C370", "")
		t.Setenv("PATH", t.TempDir()) // an empty dir: no pl1c370 on PATH
		_, err := findPL1C(p, "")
		if err == nil {
			t.Fatal("want an error, got nil")
		}
		if _, ok := err.(*project.ConfigError); !ok {
			t.Fatalf("want *project.ConfigError, got %T: %v", err, err)
		}
	})
}
