package launch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatches(t *testing.T) {
	for _, c := range []struct {
		pin, v string
		ok     bool
	}{
		{"3.0", "3.0.0", true}, {"3.0", "3.0.7", true}, {"3.0", "3.0.0-dev", true},
		{"3.0", "3.1.0", false}, {"3.0.2", "3.0.2", true}, {"3.0.2", "3.0.3", false},
		{"3.0.0-dev", "3.0.0-dev", true}, {"3.0.0-dev", "3.0.0", false},
	} {
		if Matches(c.pin, c.v) != c.ok {
			t.Errorf("Matches(%s, %s) != %v", c.pin, c.v, c.ok)
		}
	}
}

func tgz(t *testing.T, body string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "mbt-x/mbt", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return b.Bytes()
}

type fake struct {
	srv   *httptest.Server
	files map[string][]byte
	rels  []map[string]any
	hits  int
}

func newFake(t *testing.T) *fake {
	f := &fake{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		if r.URL.Path == "/repos/mvslovers/mbt/releases" {
			json.NewEncoder(w).Encode(f.rels)
			return
		}
		if d, ok := f.files[r.URL.Path]; ok {
			w.Write(d)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// publish adds a release with the linux/amd64 archive and its SHA256 list;
// tamper makes the list disagree with the archive.
func (f *fake) publish(t *testing.T, ver string, pre, tamper bool) {
	arc := tgz(t, "#!binary "+ver)
	name := AssetName(ver, "linux", "amd64")
	sum := sha256.Sum256(arc)
	h := hex.EncodeToString(sum[:])
	if tamper {
		h = strings.Repeat("0", 64)
	}
	f.files["/dl/"+name] = arc
	f.files["/dl/"+SumsName(ver)] = []byte(fmt.Sprintf("%s  %s\n", h, name))
	f.rels = append(f.rels, map[string]any{"tag_name": "v" + ver, "prerelease": pre, "assets": []map[string]string{
		{"name": name, "browser_download_url": f.srv.URL + "/dl/" + name},
		{"name": SumsName(ver), "browser_download_url": f.srv.URL + "/dl/" + SumsName(ver)},
	}})
}

func project(t *testing.T, pin string) string {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "mbt.toml"), []byte("schema = 3\n[toolchain]\nmbt = \""+pin+"\"\n"), 0o644)
	return root
}

func opts(t *testing.T, f *fake, root string, ran *string) Options {
	return Options{Root: root, Home: t.TempDir(), API: f.srv.URL, GOOS: "linux", GOARCH: "amd64",
		Log: func(string) {},
		Run: func(bin string, args []string, env []string) int {
			data, _ := os.ReadFile(bin)
			*ran = string(data)
			return 7
		}}
}

func TestSwitch(t *testing.T) {
	t.Setenv("MBT_VERSION_SWITCHED", "")
	t.Setenv("MBT_NO_SWITCH", "")
	f := newFake(t)
	f.publish(t, "3.0.0", false, false)
	f.publish(t, "3.0.1", false, false)
	f.publish(t, "3.0.2-dev", true, false)
	f.publish(t, "3.1.0", false, false)
	var ran string
	o := opts(t, f, project(t, "3.0"), &ran)

	// matching: carry on
	if h, _ := Switch("3.0.5", nil, o); h {
		t.Error("3.0.5 switched away from a 3.0 pin")
	}
	// not matching: newest stable 3.0.x is fetched and run, its exit code passed on
	h, code := Switch("3.1.0", []string{"build"}, o)
	if !h || code != 7 || ran != "#!binary 3.0.1" {
		t.Errorf("switch: %v %d %q", h, code, ran)
	}
	// installed now: no network
	f.hits, ran = 0, ""
	if h, _ := Switch("3.1.0", nil, o); !h || f.hits != 0 || ran != "#!binary 3.0.1" {
		t.Errorf("second run: hits=%d ran=%q", f.hits, ran)
	}
	// an exact prerelease pin
	o.Root = project(t, "3.0.2-dev")
	if h, _ := Switch("3.1.0", nil, o); !h || ran != "#!binary 3.0.2-dev" {
		t.Errorf("prerelease pin: %q", ran)
	}
}

func TestSwitchRefuses(t *testing.T) {
	t.Setenv("MBT_VERSION_SWITCHED", "")
	t.Setenv("MBT_NO_SWITCH", "")
	f := newFake(t)
	f.publish(t, "3.0.1", false, true)
	var ran string
	o := opts(t, f, project(t, "3.0"), &ran)
	if h, code := Switch("3.1.0", nil, o); !h || code != 3 || ran != "" {
		t.Errorf("tampered archive ran: %v %d %q", h, code, ran)
	}
	if _, err := os.Stat(filepath.Join(o.Home, "versions", "3.0.1")); err == nil {
		t.Error("tampered archive was installed")
	}
	o.Root = project(t, "4.0")
	if h, code := Switch("3.1.0", nil, o); !h || code != 3 {
		t.Errorf("no release: %v %d", h, code)
	}
	o.Root = project(t, "main")
	if h, code := Switch("3.1.0", nil, o); !h || code != 2 {
		t.Errorf("bad pin: %v %d", h, code)
	}
	t.Setenv("MBT_NO_SWITCH", "1")
	if h, _ := Switch("3.1.0", nil, o); h {
		t.Error("MBT_NO_SWITCH=1 switched")
	}
}
