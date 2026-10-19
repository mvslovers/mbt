package build

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// crtmBuilder links one startup = "crtm" module against a libc directory
// that holds crtm.o or not.
func crtmBuilder(t *testing.T, withCRTM bool) (*Builder, string) {
	t.Helper()
	root := t.TempDir()
	libc := filepath.Join(root, "lib")
	os.MkdirAll(libc, 0o755)
	os.WriteFile(filepath.Join(libc, "libc.a"), []byte("!<arch>\n"), 0o644)
	if withCRTM {
		os.WriteFile(filepath.Join(libc, "crtm.o"), []byte("x"), 0o644)
	}
	p := &project.Project{Root: root, Name: "m"}
	b := New(p, &toolchain.Toolchain{LD: "ld370", Sysroot: root, LibcDir: libc, CRTInLibc: true, EntryAutocall: true},
		Options{BuildDir: "build", Jobs: 1, Out: &bytes.Buffer{}})
	return b, filepath.Join(libc, "crtm.o")
}

// libc370#608 drops crtm.o: a module that asks for it is told so, as a
// configuration error, instead of ld370 failing to open the file.
func TestCRTMMissingIsConfigError(t *testing.T) {
	b, crtm := crtmBuilder(t, false)
	_, err := b.linkStep(&project.Unit{Name: "MOD", Entry: "MAIN", Startup: project.StartupCRTM})
	var e *Error
	if !errors.As(err, &e) || e.Code != 2 {
		t.Fatalf("want a configuration error (code 2), got %v", err)
	}
	if !strings.Contains(e.Error(), crtm) || !strings.Contains(e.Error(), "libc370#608") {
		t.Errorf("error should name %s and libc370#608: %v", crtm, e)
	}
}

func TestCRTMPresentIsLinked(t *testing.T) {
	b, crtm := crtmBuilder(t, true)
	s, err := b.linkStep(&project.Unit{Name: "MOD", Entry: "MAIN", Startup: project.StartupCRTM})
	if err != nil {
		t.Fatalf("crtm.o is there: %v", err)
	}
	if !strings.Contains(strings.Join(s.argv, " "), crtm) {
		t.Errorf("crtm.o not on the link: %v", s.argv)
	}
}
