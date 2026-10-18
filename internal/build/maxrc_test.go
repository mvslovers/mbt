package build

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// fakeAS writes an as370 that punches its deck (-o) and ends with rc, with
// as370's own diagnostics for IFO026 on stderr and, as -a would, a listing
// line on stdout.
func fakeAS(t *testing.T, dir string, rc int) string {
	t.Helper()
	p := filepath.Join(dir, "as370")
	script := "#!/bin/sh\n" +
		"while [ $# -gt 0 ]; do [ \"$1\" = -o ] && : > \"$2\"; shift; done\n" +
		"echo 'LISTING LINE'\n" +
		"echo '         BR    14' >&2\n" +
		"echo ' ERROR: This card was consumed as a continuation and the statement on it discarded (IFOX00 IFO026, severity 4) in line 3' >&2\n" +
		"echo ' Assembler Done   1 Statement Flagged /   4 was Highest Severity' >&2\n" +
		fmt.Sprintf("exit %d\n", rc)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// asBuild is a library of one assembler source, built by Run with a fake
// as370 and an ar370 that does nothing.
func asBuild(t *testing.T, rc, maxRC int) (*Builder, *bytes.Buffer, string) {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "warn.asm"), []byte("x"), 0o644)
	ar := filepath.Join(root, "ar370")
	os.WriteFile(ar, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	var out bytes.Buffer
	p := &project.Project{Root: root, Name: "w", MaxRC: maxRC,
		Lib: &project.Lib{Name: "w", Sources: []string{"warn.asm"}}}
	b := New(p, &toolchain.Toolchain{AS: fakeAS(t, root, rc), AR: ar},
		Options{BuildDir: "build", Lib: true, Jobs: 1, Out: &out})
	return b, &out, filepath.Join(root, "build", "warn.o")
}

// mbt#278, mbt#286: rc 4 passes at the default max_rc, and the warning is
// shown under its source, then counted at the end.
func TestAsWarningPassesAndShows(t *testing.T) {
	b, out, deck := asBuild(t, 4, 4)
	if err := b.Run(); err != nil {
		t.Fatalf("rc 4 at max_rc 4 failed: %v", err)
	}
	if !exists(deck) {
		t.Error("the warned deck is gone")
	}
	got := out.String()
	for _, want := range []string{
		"LISTING LINE\n[mbt] WARNING: as370 rc=4 for warn.asm:\n         BR    14\n ERROR: This card was consumed",
		"IFO026",
		"[mbt] WARNING: 1 assembly ended with warnings (rc 4): warn.asm\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	// up to date: nothing runs, nothing is counted again
	out.Reset()
	b.warned = nil
	if err := b.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("an up-to-date build warned again:\n%s", out.String())
	}
}

// max_rc = 0: the same assembly fails, names rc and max_rc, and leaves no
// deck for a later build to take for up to date.
func TestAsWarningFailsAtMaxRC0(t *testing.T) {
	b, out, deck := asBuild(t, 4, 0)
	err := b.Run()
	if err == nil || !strings.Contains(err.Error(), "[as370] warn.asm failed (rc=4, max_rc=0)") {
		t.Fatalf("rc 4 at max_rc 0: %v", err)
	}
	if exists(deck) {
		t.Error("the failed step left its deck behind")
	}
	if !strings.Contains(out.String(), "IFO026") || strings.Contains(out.String(), "ended with warnings") {
		t.Errorf("output:\n%s", out.String())
	}
}

// rc 8 is an error at the default, as it always was.
func TestAsErrorFailsAtDefault(t *testing.T) {
	b, out, deck := asBuild(t, 8, 4)
	err := b.Run()
	if err == nil || !strings.Contains(err.Error(), "failed (rc=8, max_rc=4)") {
		t.Fatalf("rc 8: %v", err)
	}
	if exists(deck) {
		t.Error("the failed step left its deck behind")
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("an error reported as a warning:\n%s", out.String())
	}
}

// A clean assembly prints nothing new.
func TestAsCleanIsQuiet(t *testing.T) {
	b, out, _ := asBuild(t, 0, 4)
	if err := b.Run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "WARNING") {
		t.Errorf("a clean assembly warned:\n%s", out.String())
	}
}
