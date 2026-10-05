package build

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadDepfile(t *testing.T) {
	// cc370 -MMD -MP: the object's rule, continued lines, then one empty rule
	// per header.  '#' is not escaped; a space in a name is.
	d := "build/ufsd#cmd.o: src/ufsd#cmd.c include/ufsd.h \\\n" +
		"  include/my\\ file.h .mbt/buildstamp.h\n\n" +
		"include/ufsd.h:\n\ninclude/my\\ file.h:\n\n.mbt/buildstamp.h:\n"
	p := filepath.Join(t.TempDir(), "x.d")
	if err := os.WriteFile(p, []byte(d), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"src/ufsd#cmd.c", "include/ufsd.h", "include/my file.h", ".mbt/buildstamp.h"}
	if got := readDepfile(p); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
	// escaped as v2 did, and read back the same; escaping twice is a no-op
	escapeDepfile(p)
	escapeDepfile(p)
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `build/ufsd\#cmd.o: src/ufsd\#cmd.c`) || strings.Contains(string(data), `\\#`) {
		t.Errorf("escaped .d:\n%s", data)
	}
	if got := readDepfile(p); !reflect.DeepEqual(got, want) {
		t.Errorf("after escaping: got %q", got)
	}
	if got := readDepfile(filepath.Join(t.TempDir(), "missing.d")); got != nil {
		t.Errorf("missing file: %q", got)
	}
}
