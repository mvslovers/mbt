// Package tools fetches the host tools a project declares in [tools]:
//
//	[tools]
//	ufsd-utils = { repo = "mvslovers/ufsd-utils", version = "1.0.1" }
//
// A tool is a GitHub release asset for the running platform, pinned like a
// dependency: its SHA-256 goes into mbt.lock as "tool:<name>" on the first
// fetch, and a later fetch of the same version with another SHA is refused
// unless mbt deps --update moves the pin. The binary is staged at
// .mbt/tools/<name>-<version>/<name>; downloads are cached by SHA.
//
// The asset and the member inside it default to ufsd-utils' layout and can
// be spelled out per tool, with {name}, {version}, {os} and {arch}:
//
//	asset = "{name}-{os}-{arch}.tar.gz"   # the release asset
//	bin   = "{name}-{os}-{arch}"          # the member of a .tar.gz/.zip
package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/deps"
)

// Tool is one [tools] entry.
type Tool struct {
	Name, Repo, Version, Asset, Bin string
}

// Error is a tool that cannot be had (exit 3).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Declared reads [tools] from a project's raw table, sorted by name.
func Declared(raw map[string]any) ([]Tool, error) {
	t, _ := raw["tools"].(map[string]any)
	var out []Tool
	for name, v := range t {
		e, ok := v.(map[string]any)
		if !ok {
			return nil, &Error{fmt.Sprintf("[tools] %s must be a table: { repo = \"owner/repo\", version = \"x.y.z\" }", name)}
		}
		tool := Tool{Name: name}
		for k, x := range e {
			s, ok := x.(string)
			if !ok {
				return nil, &Error{fmt.Sprintf("[tools] %s.%s must be a string", name, k)}
			}
			switch k {
			case "repo":
				tool.Repo = s
			case "version":
				tool.Version = s
			case "asset":
				tool.Asset = s
			case "bin":
				tool.Bin = s
			default:
				return nil, &Error{fmt.Sprintf("[tools] %s: unknown key '%s' (repo, version, asset, bin)", name, k)}
			}
		}
		if !strings.Contains(tool.Repo, "/") || tool.Version == "" {
			return nil, &Error{fmt.Sprintf("[tools] %s needs repo = \"owner/repo\" and version = \"x.y.z\"", name)}
		}
		if tool.Asset == "" {
			tool.Asset = "{name}-{os}-{arch}.tar.gz"
		}
		if tool.Bin == "" {
			tool.Bin = "{name}-{os}-{arch}"
		}
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Options for Ensure; the zero value means the real thing.
type Options struct {
	API    string // https://api.github.com
	Cache  string // ~/.mbt/v3/cache/tools
	GOOS   string
	GOARCH string
	Update bool // move the pin (mbt deps --update)
	Log    func(string)
}

func (o *Options) fill() {
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Cache == "" {
		home, _ := os.UserHomeDir()
		o.Cache = filepath.Join(home, ".mbt", "v3", "cache", "tools")
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
}

func (t Tool) expand(s string, o Options) string {
	return strings.NewReplacer("{name}", t.Name, "{version}", t.Version, "{os}", o.GOOS, "{arch}", o.GOARCH).Replace(s)
}

// Path is where the staged binary of t lives in root.
func (t Tool) Path(root string) string {
	exe := t.Name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return filepath.Join(root, ".mbt", "tools", t.Name+"-"+t.Version, exe)
}

// Ensure stages t in root (if it is not staged at the pinned version yet)
// and returns the binary's path.
func Ensure(root string, t Tool, o Options) (string, error) {
	o.fill()
	key := deps.ToolPrefix + t.Name
	lock := deps.ReadLock(root)
	pinned, hasPin := lock[key]
	dest := t.Path(root)
	if hasPin && pinned.Version == t.Version && !o.Update {
		if _, err := os.Stat(dest); err == nil {
			return dest, nil
		}
	}

	asset := t.expand(t.Asset, o)
	data, err := fetch(t, asset, pinned, hasPin && pinned.Version == t.Version && !o.Update, o)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if hasPin && pinned.Version == t.Version && pinned.SHA256 != sha && !o.Update {
		return "", errf("tool %s %s: %s has SHA-256 %s, mbt.lock pins %s -- the release asset was replaced; check it, then 'mbt deps --update' moves the pin", t.Name, t.Version, asset, sha[:12], pinned.SHA256[:12])
	}
	keep(o, t, sha, asset, data)

	bin, err := member(data, asset, t.expand(t.Bin, o))
	if err != nil {
		return "", errf("tool %s %s: %v", t.Name, t.Version, err)
	}
	os.RemoveAll(filepath.Dir(dest))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, bin, 0o755); err != nil {
		return "", err
	}
	if !hasPin || pinned.SHA256 != sha || pinned.Version != t.Version {
		lock = deps.ReadLock(root)
		lock[key] = deps.LockEntry{SHA256: sha, Version: t.Version}
		if err := deps.WriteLock(root, lock); err != nil {
			return "", err
		}
		o.Log(fmt.Sprintf("Tool %s %s pinned in mbt.lock (sha %s)", t.Name, t.Version, sha[:12]))
	}
	o.Log(fmt.Sprintf("Tool %s %s -> %s", t.Name, t.Version, rel(root, dest)))
	return dest, nil
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

func cachePath(o Options, t Tool, sha, asset string) string {
	return filepath.Join(o.Cache, t.Repo, t.Version, "sha256", sha, asset)
}

func keep(o Options, t Tool, sha, asset string, data []byte) {
	p := cachePath(o, t, sha, asset)
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		os.WriteFile(p, data, 0o644)
	}
}

// fetch returns the asset: from the cache when the pin names it, else from
// the release.
func fetch(t Tool, asset string, pinned deps.LockEntry, usePin bool, o Options) ([]byte, error) {
	if usePin {
		if data, err := os.ReadFile(cachePath(o, t, pinned.SHA256, asset)); err == nil {
			return data, nil
		}
	}
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	body, err := get(o, fmt.Sprintf("%s/repos/%s/releases/tags/v%s", o.API, t.Repo, t.Version))
	var nf notFound
	if errors.As(err, &nf) {
		return nil, errf("tool %s: %s has no release v%s", t.Name, t.Repo, t.Version)
	} else if err != nil {
		return nil, errf("tool %s %s is not staged and GitHub cannot be reached: %v", t.Name, t.Version, err)
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, errf("tool %s: release v%s: %v", t.Name, t.Version, err)
	}
	var names []string
	for _, a := range rel.Assets {
		if a.Name == asset {
			o.Log(fmt.Sprintf("Tool %s %s: fetching %s", t.Name, t.Version, asset))
			data, err := get(o, a.URL)
			if err != nil {
				return nil, errf("tool %s: %v", t.Name, err)
			}
			return data, nil
		}
		names = append(names, a.Name)
	}
	return nil, errf("tool %s: release v%s of %s has no %s for this platform (it has: %s)", t.Name, t.Version, t.Repo, asset, strings.Join(names, ", "))
}

func get(o Options, url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(url, o.API) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, notFound(url)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

type notFound string

func (n notFound) Error() string { return string(n) + ": HTTP 404" }

// member extracts the binary: a member of a .tar.gz/.tgz/.zip, or the
// asset itself.
func member(data []byte, asset, name string) ([]byte, error) {
	switch {
	case strings.HasSuffix(asset, ".tar.gz") || strings.HasSuffix(asset, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && (h.Name == name || path.Base(h.Name) == name) {
				return io.ReadAll(tr)
			}
		}
	case strings.HasSuffix(asset, ".zip"):
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name == name || path.Base(f.Name) == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
	default:
		return data, nil
	}
	return nil, fmt.Errorf("%s holds no %s", asset, name)
}
