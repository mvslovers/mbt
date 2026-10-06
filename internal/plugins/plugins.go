// Package plugins resolves and stages the Lua plugins a project declares
// (internals/mbt-3-extensions.md §6):
//
//	[plugins]
//	"mvslovers/mbt-ufs" = "^1"
//
// A plugin is a GitHub repository whose releases carry the asset
// <repo>-<version>-plugin.tar.gz, holding
//
//	plugin.toml   api = 1, exec = ["ufsd-utils"]   (the programs it may run)
//	init.lua      returns the module require("owner/repo") gives
//	lua/          further modules, require("owner/repo/name")
//
// It is resolved like a dependency -- the newest release in the range --
// and pinned like one: the asset's SHA-256 goes into mbt.lock as
// "plugin:owner/repo", and the pin moves only with mbt deps --update. A
// local working copy can stand in through .mbt/deps.local.toml [override].
package plugins

import (
	"archive/tar"
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
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/mvslovers/mbt/internal/deps"
	"github.com/mvslovers/mbt/internal/version"
)

// Error is a plugin that cannot be had or is wrong (exit 3).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Decl is one [plugins] entry.
type Decl struct{ Key, Range string }

// Plugin is a staged plugin.
type Plugin struct {
	Key     string // owner/repo
	Version string // "" for a local override
	Dir     string
	API     int
	Exec    []string
}

// Declared reads [plugins], sorted by key.
func Declared(raw map[string]any) ([]Decl, error) {
	t, _ := raw["plugins"].(map[string]any)
	var out []Decl
	for k, v := range t {
		r, ok := v.(string)
		if !ok || !strings.Contains(k, "/") {
			return nil, errf(`[plugins]: "%s" -- write "owner/repo" = "<range>", e.g. "mvslovers/mbt-ufs" = "^1"`, k)
		}
		out = append(out, Decl{Key: k, Range: r})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Options for Resolve; the zero value means the real thing.
type Options struct {
	API     string
	Cache   string
	Update  bool
	Offline bool // use only what is staged (a build never reaches out)
	Log     func(string)
}

func (o *Options) fill() {
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Cache == "" {
		home, _ := os.UserHomeDir()
		o.Cache = filepath.Join(home, ".mbt", "v3", "cache", "plugins")
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
}

// Dir is where a plugin is staged in root.
func Dir(root, key string) string {
	return filepath.Join(root, ".mbt", "plugins", filepath.FromSlash(key))
}

// Resolve stages every declared plugin and returns them. Offline, a plugin
// that is not staged at its pin is an error that says to run mbt deps.
func Resolve(root string, decls []Decl, o Options) ([]Plugin, error) {
	o.fill()
	overrides := deps.Overrides(root)
	var out []Plugin
	for _, d := range decls {
		if p, ok := overrides[d.Key]; ok {
			pl, err := readManifest(d.Key, "", p)
			if err != nil {
				return nil, err
			}
			o.Log(fmt.Sprintf("Plugin %s from %s (local override)", d.Key, p))
			out = append(out, pl)
			continue
		}
		pl, err := resolveOne(root, d, o)
		if err != nil {
			return nil, err
		}
		out = append(out, pl)
	}
	return out, nil
}

func resolveOne(root string, d Decl, o Options) (Plugin, error) {
	key := deps.PluginPrefix + d.Key
	lock := deps.ReadLock(root)
	pin, pinned := lock[key]
	dir := Dir(root, d.Key)
	if pinned && !o.Update {
		if ok, _ := version.Allowed(mustParse(pin.Version), d.Range); !ok {
			return Plugin{}, errf("plugin %s: mbt.lock pins %s, outside %q -- run 'mbt deps --update'", d.Key, pin.Version, d.Range)
		}
		if stamp, err := os.ReadFile(filepath.Join(dir, ".mbt-sha256")); err == nil && strings.TrimSpace(string(stamp)) == pin.SHA256 {
			return readManifest(d.Key, pin.Version, dir)
		}
	}
	if o.Offline {
		return Plugin{}, errf("plugin %s is not staged -- run 'mbt deps'", d.Key)
	}
	ver := pin.Version
	if !pinned || o.Update {
		v, err := newest(d, o)
		if err != nil {
			return Plugin{}, err
		}
		ver = v
	}
	_, repo, _ := strings.Cut(d.Key, "/")
	asset := fmt.Sprintf("%s-%s-plugin.tar.gz", repo, ver)
	data, err := fetchAsset(d.Key, ver, asset, pin, pinned && !o.Update && pin.Version == ver, o)
	if err != nil {
		return Plugin{}, err
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if pinned && !o.Update && pin.SHA256 != sha {
		return Plugin{}, errf("plugin %s %s: %s has SHA-256 %s, mbt.lock pins %s -- the release asset was replaced; check it, then 'mbt deps --update'", d.Key, ver, asset, sha[:12], pin.SHA256[:12])
	}
	keep(o, d.Key, ver, sha, asset, data)
	os.RemoveAll(dir)
	if err := extract(data, dir); err != nil {
		return Plugin{}, errf("plugin %s %s: %v", d.Key, ver, err)
	}
	os.WriteFile(filepath.Join(dir, ".mbt-sha256"), []byte(sha+"\n"), 0o644)
	pl, err := readManifest(d.Key, ver, dir)
	if err != nil {
		return Plugin{}, err
	}
	lock = deps.ReadLock(root)
	lock[key] = deps.LockEntry{SHA256: sha, Version: ver}
	if err := deps.WriteLock(root, lock); err != nil {
		return Plugin{}, err
	}
	o.Log(fmt.Sprintf("Plugin %s %s -> .mbt/plugins/%s (sha %s)", d.Key, ver, d.Key, sha[:12]))
	return pl, nil
}

func mustParse(s string) version.Version {
	v, _ := version.Parse(s)
	return v
}

func get(o Options, url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(url, o.API) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func newest(d Decl, o Options) (string, error) {
	data, err := get(o, fmt.Sprintf("%s/repos/%s/releases?per_page=100", o.API, d.Key))
	if err != nil {
		return "", errf("plugin %s: cannot list its releases: %v", d.Key, err)
	}
	var rels []struct {
		Tag   string `json:"tag_name"`
		Draft bool   `json:"draft"`
	}
	if err := json.Unmarshal(data, &rels); err != nil {
		return "", errf("plugin %s: %v", d.Key, err)
	}
	var best *version.Version
	for _, r := range rels {
		v, err := version.Parse(strings.TrimPrefix(r.Tag, "v"))
		if r.Draft || err != nil {
			continue
		}
		if ok, _ := version.Allowed(v, d.Range); ok && (best == nil || version.Compare(v, *best) > 0) {
			vv := v
			best = &vv
		}
	}
	if best == nil {
		return "", errf("plugin %s: no release in %q", d.Key, d.Range)
	}
	return best.String(), nil
}

func cachePath(o Options, key, ver, sha, asset string) string {
	return filepath.Join(o.Cache, filepath.FromSlash(key), ver, "sha256", sha, asset)
}

func keep(o Options, key, ver, sha, asset string, data []byte) {
	p := cachePath(o, key, ver, sha, asset)
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		os.WriteFile(p, data, 0o644)
	}
}

func fetchAsset(key, ver, asset string, pin deps.LockEntry, usePin bool, o Options) ([]byte, error) {
	if usePin {
		if data, err := os.ReadFile(cachePath(o, key, ver, pin.SHA256, asset)); err == nil {
			return data, nil
		}
	}
	data, err := get(o, fmt.Sprintf("%s/repos/%s/releases/tags/v%s", o.API, key, ver))
	if err != nil {
		return nil, errf("plugin %s: release v%s: %v", key, ver, err)
	}
	var rel struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return nil, errf("plugin %s: %v", key, err)
	}
	for _, a := range rel.Assets {
		if a.Name == asset {
			o.Log(fmt.Sprintf("Plugin %s %s: fetching %s", key, ver, asset))
			d, err := get(o, a.URL)
			if err != nil {
				return nil, errf("plugin %s: %v", key, err)
			}
			return d, nil
		}
	}
	return nil, errf("plugin %s: release v%s has no %s", key, ver, asset)
}

// extract unpacks a plugin tarball into dir; one top-level directory, if
// there is one, is stripped. Nothing may land outside dir.
func extract(data []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	type file struct {
		name string
		data []byte
	}
	var files []file
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			continue // never outside dir -- and no say in the common prefix
		}
		files = append(files, file{name, b})
	}
	// strip a common top directory
	prefix := ""
	if len(files) > 0 {
		if i := strings.Index(files[0].name, "/"); i > 0 {
			prefix = files[0].name[:i+1]
			for _, f := range files {
				if !strings.HasPrefix(f.name, prefix) {
					prefix = ""
					break
				}
			}
		}
	}
	for _, f := range files {
		rel := strings.TrimPrefix(f.name, prefix)
		if rel == "" || strings.HasPrefix(rel, "../") || path.IsAbs(rel) || rel == ".." {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readManifest(key, ver, dir string) (Plugin, error) {
	var m struct {
		API  int      `toml:"api"`
		Exec []string `toml:"exec"`
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "plugin.toml"), &m); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Plugin{}, errf("plugin %s: no plugin.toml in %s", key, dir)
		}
		return Plugin{}, errf("plugin %s: plugin.toml: %v", key, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "init.lua")); err != nil {
		return Plugin{}, errf("plugin %s: no init.lua in %s", key, dir)
	}
	if m.API == 0 {
		return Plugin{}, errf("plugin %s: plugin.toml has no api = N", key)
	}
	return Plugin{Key: key, Version: ver, Dir: dir, API: m.API, Exec: m.Exec}, nil
}
