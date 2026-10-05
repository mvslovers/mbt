package deps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves releases of owner/lib: tag -> (prerelease, asset bytes).
type fakeGitHub struct {
	rels map[string]bool   // version -> prerelease
	data map[string][]byte // version -> lib tarball
}

func libTarball(t *testing.T, repo, ver, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := fmt.Sprintf("%s-%s", repo, ver)
	for name, body := range map[string]string{
		top + "/include/lib.h": "/* " + content + " */\n",
		top + "/lib/lib.a":     content,
		"../evil":              "x",
	} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func (f *fakeGitHub) server(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/repos/o/lib/releases":
			var out []map[string]any
			for v, pre := range f.rels {
				out = append(out, map[string]any{"tag_name": "v" + v, "prerelease": pre})
			}
			json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/repos/o/lib/releases/tags/v"):
			v := strings.TrimPrefix(p, "/repos/o/lib/releases/tags/v")
			if _, ok := f.rels[v]; !ok {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + v, "assets": []map[string]string{
				{"name": "lib-" + v + "-lib.tar.gz", "url": srv.URL + "/asset/" + v}}})
		case strings.HasPrefix(p, "/asset/"):
			w.Write(f.data[strings.TrimPrefix(p, "/asset/")])
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func setup(t *testing.T, constraint string) (root string, o Options, warnings *[]string) {
	root = t.TempDir()
	os.WriteFile(filepath.Join(root, "project.toml"), []byte(fmt.Sprintf("[dependencies]\n\"o/lib\" = %q\n", constraint)), 0o644)
	var w []string
	o = Options{Cache: t.TempDir(), Log: func(string) {}, Warn: func(s string) { w = append(w, s) }}
	return root, o, &w
}

func code(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func TestResolveStageAndLock(t *testing.T) {
	gh := &fakeGitHub{rels: map[string]bool{"1.0.0": false, "1.1.0": false, "1.2.0-dev": true}, data: map[string][]byte{}}
	for v := range gh.rels {
		gh.data[v] = libTarball(t, "lib", v, "v"+v)
	}
	srv := gh.server(t)
	defer srv.Close()
	root, o, _ := setup(t, ">=1.0.0")
	o.API = srv.URL
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatal(err)
	}
	// stable constraint: the prerelease is not picked
	h, _ := os.ReadFile(filepath.Join(root, ".mbt/deps/lib/include/lib.h"))
	if string(h) != "/* v1.1.0 */\n" {
		t.Errorf("staged header %q", h)
	}
	if _, err := os.Stat(filepath.Join(root, ".mbt/deps/evil")); err == nil {
		t.Error("a path outside the archive's top directory was extracted")
	}
	lock, _ := os.ReadFile(filepath.Join(root, "mbt.lock"))
	if !strings.HasPrefix(string(lock), "{\n  \"o/lib\": {\n    \"sha256\": \"") || !strings.HasSuffix(string(lock), "\"version\": \"1.1.0\"\n  }\n}\n") {
		t.Errorf("lock not in v2's format:\n%s", lock)
	}
}

func TestDrift(t *testing.T) {
	gh := &fakeGitHub{rels: map[string]bool{"1.0.0": false, "2.0.0-dev": true}, data: map[string][]byte{}}
	gh.data["1.0.0"] = libTarball(t, "lib", "1.0.0", "a")
	gh.data["2.0.0-dev"] = libTarball(t, "lib", "2.0.0-dev", "a")
	srv := gh.server(t)
	defer srv.Close()

	// stable: a changed asset is an error
	root, o, _ := setup(t, ">=1.0.0")
	o.API = srv.URL
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatal(err)
	}
	gh.data["1.0.0"] = libTarball(t, "lib", "1.0.0", "b")
	os.RemoveAll(o.Cache)
	if err := Run(root, "project.toml", o); code(err) != 3 {
		t.Errorf("stable drift: %v", err)
	}

	// prerelease: accepted with a warning and re-pinned -- unless --locked
	root, o, warns := setup(t, ">=2.0.0-dev")
	o.API = srv.URL
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "mbt.lock"))
	gh.data["2.0.0-dev"] = libTarball(t, "lib", "2.0.0-dev", "b")
	o.Locked = true
	if err := Run(root, "project.toml", o); code(err) != 3 {
		t.Errorf("prerelease drift under --locked: %v", err)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "mbt.lock")); !bytes.Equal(before, after) {
		t.Error("--locked changed mbt.lock")
	}
	o.Locked = false
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatalf("prerelease drift: %v", err)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "rolling prerelease") {
		t.Errorf("warnings: %q", *warns)
	}
	if after, _ := os.ReadFile(filepath.Join(root, "mbt.lock")); bytes.Equal(before, after) {
		t.Error("prerelease drift was not re-pinned")
	}
}

func TestOfflineFallsBackToCache(t *testing.T) {
	gh := &fakeGitHub{rels: map[string]bool{"1.0.0": false}, data: map[string][]byte{}}
	gh.data["1.0.0"] = libTarball(t, "lib", "1.0.0", "a")
	srv := gh.server(t)
	root, o, warns := setup(t, ">=1.0.0")
	o.API = srv.URL
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatal(err)
	}
	srv.Close() // GitHub unreachable from here on
	os.Remove(filepath.Join(root, "mbt.lock"))
	if err := Run(root, "project.toml", o); err != nil {
		t.Fatalf("offline with a cache: %v", err)
	}
	if len(*warns) != 1 || !strings.Contains((*warns)[0], "from the local cache") {
		t.Errorf("warnings: %q", *warns)
	}
}
