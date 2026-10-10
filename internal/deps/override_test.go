package deps

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A dependency served by a local override keeps its committed pin, also
// under --update: the override is a local detour, not a new resolution.
func TestOverrideKeepsLockUnderUpdate(t *testing.T) {
	gh := &fakeGitHub{rels: map[string]bool{"1.0.0": false}, data: map[string][]byte{}}
	gh.data["1.0.0"] = libTarball(t, "lib", "1.0.0", "v1.0.0")
	srv := gh.server(t)
	defer srv.Close()
	root, o, _ := setup(t, ">=1.0.0")
	o.API = srv.URL
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatal(err)
	}
	pinned := lockOf(t, root)

	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "project.toml"), []byte("[project]\nname = \"lib\"\n[lib]\n"), 0o644)
	os.MkdirAll(filepath.Join(work, "build"), 0o755)
	os.WriteFile(filepath.Join(work, "build", "lib.a"), []byte("local"), 0o644)
	os.WriteFile(filepath.Join(root, ".mbt", "deps.local.toml"), []byte("[override.\"o/lib\"]\npath = \""+filepath.ToSlash(work)+"\"\n"), 0o644)

	for _, update := range []bool{false, true} {
		o.Update = update
		if err := Run(root, "project.toml", o); err != nil {
			t.Fatalf("update=%v: %v", update, err)
		}
		if got := lockOf(t, root); !bytes.Equal(got, pinned) {
			t.Errorf("update=%v: lock changed:\n%s\nwant:\n%s", update, got, pinned)
		}
	}
}
