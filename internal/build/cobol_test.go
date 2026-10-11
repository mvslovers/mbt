package build

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// A COBOL source is two steps: cobc370 to build/cobol/<stem>.asm (with a
// depfile), then as370 on that into the object of the .cbl's stem.
func TestCOBOLSteps(t *testing.T) {
	b := New(&project.Project{COBFlags: []string{"-I", "copy"}, ASFlags: []string{"-I", "maclib"}},
		&toolchain.Toolchain{AS: "as370", COBC: "/x/cobc370"}, Options{BuildDir: "build"})
	tr, err := b.translateStep("src/Hello.CBL")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/x/cobc370", "src/Hello.CBL", "-o", "build/cobol/Hello.asm", "-I", "copy", "-MMD", "-MP", "-MF", "build/cobol/Hello.d"}
	if !reflect.DeepEqual(tr.argv, want) || tr.out != "build/cobol/Hello.asm" || tr.depfile != "build/cobol/Hello.d" || tr.kind != "cobc" {
		t.Errorf("translate %+v", tr)
	}
	as := b.compileStep("src/Hello.CBL", "build/Hello.o")
	if as.kind != "as" || !reflect.DeepEqual(as.inputs, []string{"build/cobol/Hello.asm"}) ||
		!reflect.DeepEqual(as.argv, []string{"as370", "-I", "maclib", "-o", "build/Hello.o", "build/cobol/Hello.asm"}) || as.depfile != "" {
		t.Errorf("assemble %+v", as)
	}
	if cc := b.compileStep("src/a.c", "build/a.o"); cc.depfile != "build/a.d" {
		t.Errorf("cc depfile %q", cc.depfile)
	}
	b.T.COBC = ""
	if _, err := b.translateStep("src/x.cob"); err == nil || !strings.Contains(err.Error(), "no cobc370") {
		t.Errorf("no cobc370: %v", err)
	}
}

// The depfile is read for any step that has one: a copybook newer than the
// translated assembler makes the step stale.
func TestDepfileMakesStale(t *testing.T) {
	root := t.TempDir()
	b := New(&project.Project{Root: root}, &toolchain.Toolchain{COBC: "cobc370"}, Options{BuildDir: "build"})
	for _, d := range []string{"build/cobol", "copy", "src", ".mbt/cmd"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	write := func(p, s string, at time.Time) {
		os.WriteFile(filepath.Join(root, p), []byte(s), 0o644)
		os.Chtimes(filepath.Join(root, p), at, at)
	}
	old, now := time.Now().Add(-time.Hour), time.Now()
	s, _ := b.translateStep("src/p.cbl")
	write("src/p.cbl", "x", old)
	write("copy/CB.cpy", "x", old)
	write("build/cobol/p.d", "build/cobol/p.asm: src/p.cbl \\\n copy/CB.cpy\n\ncopy/CB.cpy:\n", old)
	write("build/cobol/p.asm", "x", now)
	os.WriteFile(b.cmdFile(s.out), []byte(signature(s.argv)), 0o644)
	if b.stale(s) {
		t.Fatal("up to date reads as stale")
	}
	write("copy/CB.cpy", "y", now.Add(time.Minute))
	if !b.stale(s) {
		t.Error("a newer copybook did not make the translation stale")
	}
}

// mbt#253: a cobc370 that predates -MMD stops without writing the .d; the
// error says so. One that fails after writing it does not get the hint.
func TestOldCobc370Named(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "build", "cobol"), 0o755)
	os.MkdirAll(filepath.Join(root, ".mbt", "cmd"), 0o755)
	os.WriteFile(filepath.Join(root, "p.cbl"), []byte("x"), 0o644)
	old := filepath.Join(root, "old-cobc370")
	os.WriteFile(old, []byte("#!/bin/sh\necho 'unknown option -MMD' >&2\nexit 2\n"), 0o755)
	b := New(&project.Project{Root: root}, &toolchain.Toolchain{COBC: old}, Options{BuildDir: "build"})
	s, _ := b.translateStep("p.cbl")
	if err := b.exec(s); err == nil || !strings.Contains(err.Error(), "predates commit c531019") {
		t.Errorf("old cobc370: %v", err)
	}
	broken := filepath.Join(root, "broken-cobc370")
	os.WriteFile(broken, []byte("#!/bin/sh\nshift; shift; shift\nwhile [ $# -gt 0 ]; do [ \"$1\" = -MF ] && : > \"$2\"; shift; done\nexit 8\n"), 0o755)
	b.T.COBC = broken
	s, _ = b.translateStep("p.cbl")
	if err := b.exec(s); err == nil || strings.Contains(err.Error(), "predates") {
		t.Errorf("a COBOL error with a .d: %v", err)
	}
}

// mbt#255: a dependency's include/ is on cobc370's path, after the
// project's own copybook directories.
func TestCobflagsDependencies(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".mbt", "deps", "mbt-example-lib", "include"), 0o755)
	b := New(&project.Project{Root: root, COBFlags: []string{"-I", "copy"}}, &toolchain.Toolchain{COBC: "cobc370"}, Options{BuildDir: "build"})
	want := []string{"-I", "copy", "-I", ".mbt/deps/mbt-example-lib/include"}
	if got := b.cobflags(); !reflect.DeepEqual(got, want) {
		t.Errorf("%q", got)
	}
}
