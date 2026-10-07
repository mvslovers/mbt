// Package deps resolves a project's [dependencies] against GitHub Releases,
// downloads each {repo}-{version}-lib.tar.gz, stages it under .mbt/deps and
// pins it in mbt.lock -- mbt v2's `make deps` (scripts/mbtdeps.py).
//
// The lock is the pin, and nothing but `mbt deps --update` moves it: a locked
// version that no longer fits its range, or an asset whose SHA-256 differs
// from the lock -- a republished -dev prerelease included -- is an error.
// (mbt 2 accepted a drifted prerelease with a warning and rewrote the lock,
// #52; that made two builds of one commit link different code.)  A missing
// mbt.lock is created, and a dependency newly declared is added: both are
// deliberate edits, not drift.
//
// Archives are cached by their SHA-256, so a locked prerelease stays
// buildable after its tag moved on, as long as this machine fetched it once.
package deps

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mvslovers/mbt/internal/version"
)

// Error is a dependency failure (exit 3) or a configuration error (exit 2).
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func depErr(format string, a ...any) error { return &Error{3, fmt.Sprintf(format, a...)} }

// Options for one run.
type Options struct {
	Update bool // re-resolve every range, rewrite the lock
	Log    func(string)
	Warn   func(string)
	// API is the GitHub API base (tests point it at a local server).
	API string
	// Cache is where downloaded archives are kept.
	Cache string
}

// LockEntry is one dependency in mbt.lock.
type LockEntry struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

// Run stages the dependencies of the project at root.
func Run(root, projectFile string, o Options) error {
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Cache == "" {
		home, _ := os.UserHomeDir()
		o.Cache = filepath.Join(home, ".mbt", "v3", "cache")
	}
	var raw struct {
		Dependencies map[string]string `toml:"dependencies"`
	}
	meta, err := toml.DecodeFile(filepath.Join(root, projectFile), &raw)
	if err != nil {
		return &Error{2, fmt.Sprintf("cannot parse %s: %v", projectFile, err)}
	}
	if len(raw.Dependencies) == 0 {
		o.Log("Dependencies: none declared")
		return nil
	}
	// declaration order, as v2 processed them
	var keys []string
	for _, k := range meta.Keys() {
		if len(k) == 2 && k[0] == "dependencies" {
			keys = append(keys, k[1])
		}
	}

	lockPath := filepath.Join(root, "mbt.lock")
	lock := map[string]LockEntry{}
	if !o.Update {
		if data, err := os.ReadFile(lockPath); err == nil {
			json.Unmarshal(data, &lock)
		}
	}
	overrides := readOverrides(filepath.Join(root, ".mbt", "deps.local.toml"))
	if len(overrides) > 0 {
		var names []string
		for k := range overrides {
			names = append(names, k)
		}
		sort.Strings(names)
		o.Log(fmt.Sprintf("Local overrides (.mbt/deps.local.toml): %s", strings.Join(names, ", ")))
	}

	depsDir := filepath.Join(root, ".mbt", "deps")
	if err := os.MkdirAll(depsDir, 0o755); err != nil {
		return err
	}
	newLock := map[string]LockEntry{}
	for _, key := range keys {
		constraint := raw.Dependencies[key]
		owner, repo, ok := strings.Cut(key, "/")
		if !ok {
			return &Error{2, fmt.Sprintf("bad dependency key '%s' (want owner/repo)", key)}
		}
		locked, hasLock := lock[key]

		if ov, ok := overrides[key]; ok && ov != "" {
			name, err := stageOverride(ov, filepath.Join(depsDir, repo))
			if err != nil {
				return depErr("%s: path override failed: %v", key, err)
			}
			o.Log(fmt.Sprintf("%s -> LOCAL %s (%s.a, override)", key, ov, name))
			if hasLock {
				newLock[key] = locked
			}
			continue
		}

		keep := !o.Update && hasLock
		if keep && !lockFits(locked, constraint) {
			return depErr("%s: locked '%s' no longer fits '%s' -- run 'mbt deps --update' to re-resolve", key, locked.Version, constraint)
		}
		var ver string
		if keep {
			ver = locked.Version
		} else {
			ver, err = resolve(o, owner, repo, constraint)
			if err != nil {
				return err
			}
			if !o.Update && !hasLock && len(lock) > 0 {
				o.Log(fmt.Sprintf("%s: not in mbt.lock yet -- adding it", key))
			}
		}
		v, err := version.Parse(ver)
		if err != nil {
			return depErr("%s: %v", key, err)
		}
		o.Log(fmt.Sprintf("%s %s -> %s", key, constraint, ver))

		asset := fmt.Sprintf("%s-%s-lib.tar.gz", repo, ver)
		var tarball string
		if keep && locked.SHA256 != "" {
			// the locked archive itself, if this machine ever fetched it
			if p := cachedBySHA(o, owner, repo, ver, locked.SHA256, asset); p != "" {
				tarball = p
			}
		}
		if tarball == "" {
			tarball, err = fetch(o, owner, repo, ver, asset, v.IsPre())
			if err != nil {
				return err
			}
		}
		sha, err := sha256File(tarball)
		if err != nil {
			return err
		}
		if keep && locked.SHA256 != "" && locked.SHA256 != sha {
			what := "the release asset changed"
			if v.IsPre() {
				what = "the prerelease was republished, and the locked archive is not in the local cache"
			}
			return depErr("%s %s: lib SHA %s differs from mbt.lock (%s) -- %s. Run 'mbt deps --update' to pin the new one.", key, ver, sha[:12], locked.SHA256[:12], what)
		}
		if err := keepBySHA(o, owner, repo, ver, sha, asset, tarball); err != nil {
			return err
		}
		dest := filepath.Join(depsDir, repo)
		if err := stage(tarball, dest); err != nil {
			return depErr("%s: cannot stage %s: %v", key, asset, err)
		}
		o.Log(fmt.Sprintf("  staged -> .mbt/deps/%s (sha %s)", repo, sha[:12]))
		newLock[key] = LockEntry{SHA256: sha, Version: ver}
	}

	// the pins of [tools] live in the same file and are not ours to drop
	for k, v := range ReadLock(root) {
		if strings.HasPrefix(k, ToolPrefix) {
			newLock[k] = v
		}
	}
	data := lockJSON(newLock)
	old, _ := os.ReadFile(lockPath)
	if string(old) != string(data) {
		if err := os.WriteFile(lockPath, data, 0o644); err != nil {
			return err
		}
	}
	o.Log(fmt.Sprintf("Locked %d dependency(ies) -> mbt.lock", len(newLock)))
	return nil
}

// ToolPrefix marks the mbt.lock entries of [tools] ("tool:ufsd-utils").
const ToolPrefix = "tool:"

// ReadLock reads root/mbt.lock; a missing or unreadable file is empty.
func ReadLock(root string) map[string]LockEntry {
	lock := map[string]LockEntry{}
	if data, err := os.ReadFile(filepath.Join(root, "mbt.lock")); err == nil {
		json.Unmarshal(data, &lock)
	}
	return lock
}

// WriteLock writes root/mbt.lock in v2's format, only when it changes.
func WriteLock(root string, lock map[string]LockEntry) error {
	path := filepath.Join(root, "mbt.lock")
	data := lockJSON(lock)
	if old, _ := os.ReadFile(path); string(old) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

// lockJSON writes the lock exactly as v2 did: json.dumps(indent=2,
// sort_keys=True) plus a newline.
func lockJSON(m map[string]LockEntry) []byte {
	if len(m) == 0 {
		return []byte("{}\n")
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	return append(data, '\n')
}

func lockFits(l LockEntry, constraint string) bool {
	v, err := version.Parse(l.Version)
	if err != nil {
		return false
	}
	ok, err := version.Allowed(v, constraint)
	return err == nil && ok
}

// -- GitHub -----------------------------------------------------------------

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

var client = &http.Client{Timeout: 120 * time.Second}

func get(o Options, url, accept string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "mbt/3")
	if tok := token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return client.Do(req)
}

func token() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("MBT_GITHUB_TOKEN")
}

// resolve picks the highest release that the constraint allows.  GitHub is
// authoritative; the cache answers only when GitHub cannot (unreachable, 5xx,
// rate limit), with a warning, because it holds only what this machine
// happened to download (mbt#125).
func resolve(o Options, owner, repo, constraint string) (string, error) {
	resp, err := get(o, fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", o.API, owner, repo), "application/vnd.github+json")
	if err != nil {
		return resolveOffline(o, owner, repo, constraint, fmt.Sprintf("Cannot reach GitHub API (%v)", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return resolveOffline(o, owner, repo, constraint, fmt.Sprintf("GitHub API HTTP %d", resp.StatusCode))
	}
	if resp.StatusCode != 200 {
		return "", depErr("GitHub API error for %s/%s: HTTP %d", owner, repo, resp.StatusCode)
	}
	var rels []release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return "", depErr("GitHub API answer for %s/%s: %v", owner, repo, err)
	}
	allowPre := version.AllowsPre(constraint)
	var best *version.Version
	for _, r := range rels {
		if r.Draft || (r.Prerelease && !allowPre) {
			continue
		}
		v, err := version.Parse(strings.TrimLeft(r.TagName, "v"))
		if err != nil {
			continue
		}
		if ok, err := version.Satisfies(v, constraint); err != nil {
			return "", &Error{2, fmt.Sprintf("%s/%s: %v", owner, repo, err)}
		} else if ok && (best == nil || version.Compare(v, *best) > 0) {
			vv := v
			best = &vv
		}
	}
	if best == nil {
		return "", depErr("No release of %s/%s satisfies '%s'", owner, repo, constraint)
	}
	return best.String(), nil
}

func resolveOffline(o Options, owner, repo, constraint, reason string) (string, error) {
	entries, _ := os.ReadDir(filepath.Join(o.Cache, owner, repo))
	var best *version.Version
	for _, e := range entries {
		v, err := version.Parse(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		if ok, _ := version.Allowed(v, constraint); ok && (best == nil || version.Compare(v, *best) > 0) {
			vv := v
			best = &vv
		}
	}
	if best == nil {
		return "", depErr("%s for %s/%s, and no cached version satisfies '%s'", reason, owner, repo, constraint)
	}
	o.Warn(fmt.Sprintf("%s: resolved %s/%s %s -> %s from the local cache; a newer release may exist", reason, owner, repo, constraint, best))
	return best.String(), nil
}

// fetch returns the cached asset, downloading it when it is missing or (a
// prerelease, whose tag may have moved) always.  A release gone upstream
// falls back to the cache with a warning: that build fails wherever the cache
// is absent.
func fetch(o Options, owner, repo, ver, asset string, force bool) (string, error) {
	dir := filepath.Join(o.Cache, owner, repo, ver)
	path := filepath.Join(dir, asset)
	_, statErr := os.Stat(path)
	cached := statErr == nil
	if cached && !force {
		return path, nil
	}
	resp, err := get(o, fmt.Sprintf("%s/repos/%s/%s/releases/tags/v%s", o.API, owner, repo, ver), "application/vnd.github+json")
	if err != nil {
		if cached {
			return path, nil
		}
		return "", depErr("Cannot find release v%s for %s/%s and no local cache available", ver, owner, repo)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if cached {
			if resp.StatusCode == 404 {
				o.Warn(fmt.Sprintf("%s/%s %s: release v%s no longer exists on GitHub; using the local cache (%s). A build without that cache (CI) will fail.", owner, repo, ver, ver, dir))
			}
			return path, nil
		}
		return "", depErr("Cannot find release v%s for %s/%s and no local cache available", ver, owner, repo)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", depErr("release v%s of %s/%s: %v", ver, owner, repo, err)
	}
	url := ""
	for _, a := range r.Assets {
		if a.Name == asset {
			url = a.URL
		}
	}
	if url == "" {
		return "", depErr("%s/%s %s: no %s in the release (dependency has no library artifact)", owner, repo, ver, asset)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := download(o, url, path); err != nil {
		return "", err
	}
	return path, nil
}

func download(o Options, url, dest string) error {
	resp, err := get(o, url, "application/octet-stream")
	if err != nil {
		return depErr("Download failed for %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return depErr("Download failed for %s: HTTP %d", url, resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return depErr("Download failed for %s: %v", url, err)
	}
	f.Close()
	return os.Rename(tmp, dest)
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stage extracts {repo}-{ver}-lib.tar.gz into dest, stripping the top
// directory, as v2 did.  Paths that would leave dest are skipped.
func stage(tarball, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		parts := strings.Split(strings.Trim(filepath.ToSlash(h.Name), "/"), "/")
		if len(parts) <= 1 {
			continue
		}
		rel := filepath.Join(parts[1:]...)
		if filepath.IsAbs(rel) || strings.Contains("/"+filepath.ToSlash(rel)+"/", "/../") {
			continue
		}
		target := filepath.Join(dest, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			os.Chtimes(target, h.ModTime, h.ModTime)
		}
	}
}

// -- local overrides ---------------------------------------------------------

func readOverrides(p string) map[string]string {
	var raw struct {
		Override map[string]struct {
			Path string `toml:"path"`
		} `toml:"override"`
	}
	if _, err := toml.DecodeFile(p, &raw); err != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range raw.Override {
		out[k] = v.Path
	}
	return out
}

// stageOverride stages a dependency from a local working copy: its [lib]
// archive from build/ and its declared headers.
func stageOverride(path, dest string) (string, error) {
	var raw struct {
		Project struct {
			Name string `toml:"name"`
		} `toml:"project"`
		Lib *struct {
			Name    string   `toml:"name"`
			Headers []string `toml:"headers"`
		} `toml:"lib"`
	}
	file := "mbt.toml"
	if _, err := os.Stat(filepath.Join(path, file)); err != nil {
		file = "project.toml"
	}
	if _, err := toml.DecodeFile(filepath.Join(path, file), &raw); err != nil {
		return "", fmt.Errorf("no mbt.toml or project.toml in override path %s", path)
	}
	if raw.Lib == nil {
		return "", fmt.Errorf("%s has no [lib] section to consume", path)
	}
	name := raw.Lib.Name
	if name == "" {
		name = raw.Project.Name
	}
	lib := filepath.Join(path, "build", name+".a")
	if _, err := os.Stat(lib); err != nil {
		return "", fmt.Errorf("%s not built -- run 'mbt build' in %s", lib, path)
	}
	os.RemoveAll(dest)
	for _, d := range []string{"include", "lib"} {
		if err := os.MkdirAll(filepath.Join(dest, d), 0o755); err != nil {
			return "", err
		}
	}
	if err := copyFile(lib, filepath.Join(dest, "lib", name+".a")); err != nil {
		return "", err
	}
	for _, h := range raw.Lib.Headers {
		src := filepath.Join(path, h)
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(dest, "include", filepath.Base(h))); err != nil {
				return "", err
			}
		}
	}
	return name, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// The SHA-addressed cache: <cache>/<owner>/<repo>/<version>/sha256/<sha>/<asset>.

func shaPath(o Options, owner, repo, ver, sha, asset string) string {
	return filepath.Join(o.Cache, owner, repo, ver, "sha256", sha, asset)
}

func cachedBySHA(o Options, owner, repo, ver, sha, asset string) string {
	p := shaPath(o, owner, repo, ver, sha, asset)
	if got, err := sha256File(p); err == nil && got == sha {
		return p
	}
	return ""
}

func keepBySHA(o Options, owner, repo, ver, sha, asset, src string) error {
	p := shaPath(o, owner, repo, ver, sha, asset)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return copyFile(src, p)
}
