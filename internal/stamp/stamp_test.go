package stamp

import (
	"os"
	"testing"
)

// The golden files were rendered by mbt v2's scripts/mbt/buildstamp.py; an
// object that includes the header must not differ between v2 and v3.
func TestRenderMatchesV2(t *testing.T) {
	for _, c := range []struct {
		file, project, version, commit string
		dirty                          bool
	}{
		{"testdata/clean.h", "ufsd", "1.4.0-dev", "a5c7b82", false},
		{"testdata/dirty.h", `my "q" proj`, "1.0.0", "abc1234", true},
	} {
		want, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		if got := Render(c.project, c.version, c.commit, c.dirty); got != string(want) {
			t.Errorf("%s: rendered header differs from v2's\n--- got\n%s\n--- want\n%s", c.file, got, want)
		}
	}
}

func TestWriteOnlyWhenChanged(t *testing.T) {
	dir := t.TempDir()
	if w, err := Write(dir, "p", "1.0.0"); err != nil || !w {
		t.Fatalf("first write: written=%v err=%v", w, err)
	}
	if w, err := Write(dir, "p", "1.0.0"); err != nil || w {
		t.Fatalf("unchanged content was rewritten: written=%v err=%v", w, err)
	}
	if w, _ := Write(dir, "p", "1.0.1"); !w {
		t.Fatal("a changed version was not written")
	}
}
