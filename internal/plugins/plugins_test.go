package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/deps"
)

func tgz(files map[string]string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for n, body := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

type fake struct {
	srv    *httptest.Server
	rels   []string
	assets map[string][]byte // version -> asset
}

func newFake(t *testing.T) *fake {
	f := &fake{assets: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/repos/o/ufs/releases":
			var out []map[string]any
			for _, v := range f.rels {
				out = append(out, map[string]any{"tag_name": "v" + v})
			}
			json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/repos/o/ufs/releases/tags/v"):
			v := strings.TrimPrefix(p, "/repos/o/ufs/releases/tags/v")
			json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{
				{"name": "ufs-" + v + "-plugin.tar.gz", "browser_download_url": f.srv.URL + "/dl/" + v}}})
		case strings.HasPrefix(p, "/dl/"):
			w.Write(f.assets[strings.TrimPrefix(p, "/dl/")])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func pluginTar(body string) []byte {
	return tgz(map[string]string{
		"ufs/plugin.toml":  "api = 1\nexec = [\"ufsd-utils\"]\n",
		"ufs/init.lua":     body,
		"ufs/lua/util.lua": "return {}",
		"../escape.lua":    "no",
	})
}

func TestResolvePinsAndStages(t *testing.T) {
	f := newFake(t)
	f.rels = []string{"1.0.0", "1.2.0", "2.0.0", "1.3.0-dev"}
	f.assets["1.2.0"] = pluginTar("return 12")
	root := t.TempDir()
	o := Options{API: f.srv.URL, Cache: t.TempDir()}
	pl, err := Resolve(root, []Decl{{Key: "o/ufs", Range: "^1"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(pl) != 1 || pl[0].Version != "1.2.0" || pl[0].API != 1 || strings.Join(pl[0].Exec, ",") != "ufsd-utils" {
		t.Fatalf("%+v", pl)
	}
	if b, _ := os.ReadFile(filepath.Join(Dir(root, "o/ufs"), "init.lua")); string(b) != "return 12" {
		t.Errorf("staged init.lua %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, ".mbt", "escape.lua")); err == nil {
		t.Error("a path outside the plugin was extracted")
	}
	if e := deps.ReadLock(root)["plugin:o/ufs"]; e.Version != "1.2.0" || len(e.SHA256) != 64 {
		t.Errorf("lock %+v", e)
	}
	// offline and staged: fine; a newer release does not move the pin
	f.rels = append(f.rels, "1.4.0")
	f.assets["1.4.0"] = pluginTar("return 14")
	if pl, err = Resolve(root, []Decl{{Key: "o/ufs", Range: "^1"}}, Options{API: "http://127.0.0.1:1", Offline: true}); err != nil || pl[0].Version != "1.2.0" {
		t.Errorf("offline: %+v %v", pl, err)
	}
	// the asset replaced under the same version: refused
	os.RemoveAll(Dir(root, "o/ufs"))
	f.assets["1.2.0"] = pluginTar("return 'evil'")
	if _, err = Resolve(root, []Decl{{Key: "o/ufs", Range: "^1"}}, Options{API: f.srv.URL, Cache: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Errorf("replaced: %v", err)
	}
	// --update moves to the newest in range
	if pl, err = Resolve(root, []Decl{{Key: "o/ufs", Range: "^1"}}, Options{API: f.srv.URL, Cache: t.TempDir(), Update: true}); err != nil || pl[0].Version != "1.4.0" {
		t.Errorf("update: %+v %v", pl, err)
	}
	// offline, not staged
	if _, err = Resolve(t.TempDir(), []Decl{{Key: "o/ufs", Range: "^1"}}, Options{Offline: true}); err == nil || !strings.Contains(err.Error(), "run 'mbt deps'") {
		t.Errorf("offline unstaged: %v", err)
	}
}

func TestLocalOverride(t *testing.T) {
	root := t.TempDir()
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "plugin.toml"), []byte("api = 1\nexec = []\n"), 0o644)
	os.WriteFile(filepath.Join(work, "init.lua"), []byte("return {}"), 0o644)
	os.MkdirAll(filepath.Join(root, ".mbt"), 0o755)
	os.WriteFile(filepath.Join(root, ".mbt", "deps.local.toml"), []byte("[override]\n\"o/ufs\" = { path = \""+work+"\" }\n"), 0o644)
	pl, err := Resolve(root, []Decl{{Key: "o/ufs", Range: "^1"}}, Options{Offline: true})
	if err != nil || pl[0].Dir != work || pl[0].Version != "" {
		t.Errorf("%+v %v", pl, err)
	}
	if _, ok := deps.ReadLock(root)["plugin:o/ufs"]; ok {
		t.Error("an override was pinned")
	}
}

func TestManifestRequired(t *testing.T) {
	root, work := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(work, "init.lua"), []byte("return {}"), 0o644)
	os.MkdirAll(filepath.Join(root, ".mbt"), 0o755)
	os.WriteFile(filepath.Join(root, ".mbt", "deps.local.toml"), []byte("[override]\n\"o/ufs\" = { path = \""+work+"\" }\n"), 0o644)
	if _, err := Resolve(root, []Decl{{Key: "o/ufs", Range: "^1"}}, Options{}); err == nil || !strings.Contains(err.Error(), "no plugin.toml") {
		t.Errorf("%v", err)
	}
}
