package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/deps"
)

func tgz(name, body string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return b.Bytes()
}

type fake struct {
	srv   *httptest.Server
	asset []byte
	hits  int
}

func newFake(t *testing.T) *fake {
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		switch r.URL.Path {
		case "/repos/o/ufsd-utils/releases/tags/v1.0.1":
			json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{
				{"name": "ufsd-utils-linux-amd64.tar.gz", "browser_download_url": f.srv.URL + "/dl"}}})
		case "/dl":
			w.Write(f.asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestDeclared(t *testing.T) {
	ts, err := Declared(map[string]any{"tools": map[string]any{
		"ufsd-utils": map[string]any{"repo": "o/ufsd-utils", "version": "1.0.1"}}})
	if err != nil || len(ts) != 1 || ts[0].Asset != "{name}-{os}-{arch}.tar.gz" {
		t.Fatalf("%v %v", ts, err)
	}
	if _, err := Declared(map[string]any{"tools": map[string]any{"x": map[string]any{"repo": "o/x"}}}); err == nil {
		t.Error("a tool without a version was accepted")
	}
}

func TestEnsurePinsAndRefusesAReplacedAsset(t *testing.T) {
	f := newFake(t)
	f.asset = tgz("ufsd-utils-linux-amd64", "v1")
	root := t.TempDir()
	tool := Tool{Name: "ufsd-utils", Repo: "o/ufsd-utils", Version: "1.0.1", Asset: "{name}-{os}-{arch}.tar.gz", Bin: "{name}-{os}-{arch}"}
	o := Options{API: f.srv.URL, Cache: t.TempDir(), GOOS: "linux", GOARCH: "amd64"}

	p, err := Ensure(root, tool, o)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); string(data) != "v1" {
		t.Errorf("staged %q", data)
	}
	lock := deps.ReadLock(root)
	if lock["tool:ufsd-utils@linux-amd64"].Version != "1.0.1" || len(lock["tool:ufsd-utils@linux-amd64"].SHA256) != 64 {
		t.Errorf("lock %v", lock)
	}

	// staged and pinned: no network at all
	f.hits = 0
	if _, err := Ensure(root, tool, o); err != nil || f.hits != 0 {
		t.Errorf("second Ensure: %v, %d requests", err, f.hits)
	}

	// the release asset replaced, nothing staged, nothing cached: refused
	os.RemoveAll(root + "/.mbt")
	o.Cache = t.TempDir()
	f.asset = tgz("ufsd-utils-linux-amd64", "v1-replaced")
	if _, err := Ensure(root, tool, o); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Errorf("replaced asset: %v", err)
	}
	// ... until the pin is moved on purpose
	o.Update = true
	if _, err := Ensure(root, tool, o); err != nil {
		t.Errorf("--update: %v", err)
	}
}

func TestEnsureNoAssetForPlatform(t *testing.T) {
	f := newFake(t)
	tool := Tool{Name: "ufsd-utils", Repo: "o/ufsd-utils", Version: "1.0.1", Asset: "{name}-{os}-{arch}.tar.gz", Bin: "{name}-{os}-{arch}"}
	_, err := Ensure(t.TempDir(), tool, Options{API: f.srv.URL, Cache: t.TempDir(), GOOS: "windows", GOARCH: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "no ufsd-utils-windows-amd64.tar.gz for this platform") {
		t.Errorf("%v", err)
	}
}

func TestEnsureMissingReleaseAndNoNetwork(t *testing.T) {
	f := newFake(t)
	tool := Tool{Name: "ufsd-utils", Repo: "o/ufsd-utils", Version: "9.9.9", Asset: "{name}-{os}-{arch}.tar.gz", Bin: "{name}-{os}-{arch}"}
	_, err := Ensure(t.TempDir(), tool, Options{API: f.srv.URL, Cache: t.TempDir(), GOOS: "linux", GOARCH: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "has no release v9.9.9") {
		t.Errorf("missing release: %v", err)
	}
	_, err = Ensure(t.TempDir(), tool, Options{API: "http://127.0.0.1:1", Cache: t.TempDir(), GOOS: "linux", GOARCH: "amd64"})
	if err == nil || !strings.Contains(err.Error(), "cannot be reached") {
		t.Errorf("no network: %v", err)
	}
}

// A lock written on one platform must hold on the others: the pin is per
// platform, and the first fetch records every platform's SHA-256 from the
// digests GitHub publishes, so a Mac-made mbt.lock checks a Linux runner's
// download instead of failing on it (or re-pinning it unchecked).
func TestPinsEveryPlatform(t *testing.T) {
	mac, lin := tgz("ufsd-utils-darwin-arm64", "mac"), tgz("ufsd-utils-linux-amd64", "linux")
	assets := map[string][]byte{"ufsd-utils-darwin-arm64.tar.gz": mac, "ufsd-utils-linux-amd64.tar.gz": lin}
	digest := func(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/ufsd-utils/releases/tags/v1.0.1" {
			var list []map[string]string
			for n, b := range assets {
				list = append(list, map[string]string{"name": n, "browser_download_url": srv.URL + "/dl/" + n, "digest": digest(b)})
			}
			json.NewEncoder(w).Encode(map[string]any{"assets": list})
			return
		}
		if b, ok := assets[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	tool := Tool{Name: "ufsd-utils", Repo: "o/ufsd-utils", Version: "1.0.1", Asset: "{name}-{os}-{arch}.tar.gz", Bin: "{name}-{os}-{arch}"}
	root := t.TempDir()
	// an old single-platform pin is replaced
	deps.WriteLock(root, map[string]deps.LockEntry{"tool:ufsd-utils": {Version: "1.0.1", SHA256: strings.Repeat("0", 64)}})

	if _, err := Ensure(root, tool, Options{API: srv.URL, Cache: t.TempDir(), GOOS: "darwin", GOARCH: "arm64"}); err != nil {
		t.Fatal(err)
	}
	lock := deps.ReadLock(root)
	if _, old := lock["tool:ufsd-utils"]; old {
		t.Error("the single-platform pin is still there")
	}
	for _, k := range []string{"tool:ufsd-utils@darwin-arm64", "tool:ufsd-utils@linux-amd64"} {
		if e := lock[k]; e.Version != "1.0.1" || len(e.SHA256) != 64 {
			t.Errorf("%s: %+v", k, e)
		}
	}
	// the Linux runner, same lock: its download is checked against the pin
	os.RemoveAll(root + "/.mbt/tools")
	lo := Options{API: srv.URL, Cache: t.TempDir(), GOOS: "linux", GOARCH: "amd64"}
	if _, err := Ensure(root, tool, lo); err != nil {
		t.Errorf("linux with a mac-made lock: %v", err)
	}
	os.RemoveAll(root + "/.mbt/tools")
	assets["ufsd-utils-linux-amd64.tar.gz"] = tgz("ufsd-utils-linux-amd64", "evil")
	lo.Cache = t.TempDir()
	if _, err := Ensure(root, tool, lo); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Errorf("a replaced linux asset: %v", err)
	}
}
