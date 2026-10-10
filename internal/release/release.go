// Package release bumps the version, tags and pushes: `mbt release` and
// `mbt prerelease`, ported from scripts/mvsrelease.py.
//
// release VERSION: the tree is at VERSION-dev; the version becomes VERSION,
// is committed as "release: vVERSION", tagged vVERSION and pushed; then the
// next development version (default: patch+1-dev) is committed and pushed.
//
// prerelease: the tag v<current version> is deleted (local and remote) and
// created again on HEAD, so the tag push triggers the release workflow anew.
// Nothing in the tree changes.
//
// In an mbt.toml the version lives in [project] version only (the FMID
// follows from it); in a project.toml it is also in the [release]
// version_files, VERSION among them.
package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/version"
)

// Error is a refusal (exit 2) or a failed git step.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Options: Log prints progress; Git runs git (tests replace it).
type Options struct {
	Log func(string)
	Git func(dir string, args ...string) (string, error)
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (o *Options) fill() {
	if o.Git == nil {
		o.Git = runGit
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
}

func clean(root string, o Options) error {
	out, err := o.Git(root, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		return errf("the working tree is not clean -- commit or stash first")
	}
	return nil
}

// Release releases the -dev version in the tree as ver, then moves the tree
// to next ("" = patch+1-dev).
func Release(root string, p *project.Project, ver, next string, o Options) error {
	o.fill()
	if err := clean(root, o); err != nil {
		return err
	}
	cur, err := version.Parse(p.Version)
	if err != nil {
		return errf("%s: %v", p.File, err)
	}
	rel, err := version.Parse(ver)
	if err != nil {
		return errf("release version: %v", err)
	}
	if rel.IsPre() || !cur.IsPre() || cur.Major != rel.Major || cur.Minor != rel.Minor || cur.Patch != rel.Patch {
		return errf("cannot release %s: the tree is at %s, expected %s-dev", ver, p.Version, ver)
	}
	if next == "" {
		next = fmt.Sprintf("%d.%d.%d-dev", rel.Major, rel.Minor, rel.Patch+1)
	} else if nv, err := version.Parse(next); err != nil || !nv.IsPre() {
		return errf("next version '%s' must be a prerelease, e.g. %s-dev", next, next)
	}
	if err := nextFMIDCheck(p, rel, next); err != nil {
		return err
	}
	if err := ownsTags(root, p, o); err != nil {
		return errf("release refused: %v", err)
	}
	if p.Schema == 3 {
		if err := smpCheck(root, p, ver); err != nil {
			return err
		}
	}
	tag := "v" + ver
	if out, _ := o.Git(root, "tag", "-l", tag); strings.TrimSpace(out) != "" {
		return errf("tag %s already exists locally -- if an aborted run left it: git tag -d %s; git push origin --delete %s", tag, tag, tag)
	}
	if out, _ := o.Git(root, "ls-remote", "--tags", "origin", "refs/tags/"+tag); strings.TrimSpace(out) != "" {
		return errf("tag %s already exists on origin -- git push origin --delete %s first", tag, tag)
	}

	o.Log(fmt.Sprintf("Releasing %s -> %s, next: %s", p.Version, ver, next))
	steps := []func() error{
		func() error { return bump(root, p, p.Version, ver, o) },
		func() error { return git(root, o, "commit", "-m", "release: "+tag) },
		func() error { return git(root, o, "tag", tag) },
		func() error { return git(root, o, "push", "origin", "HEAD") },
		func() error { return git(root, o, "push", "origin", tag) },
		func() error { return bump(root, p, ver, next, o) },
		func() error { return git(root, o, "commit", "-m", "chore: bump to "+next) },
		func() error { return git(root, o, "push", "origin", "HEAD") },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	o.Log(fmt.Sprintf("Released %s. Now on %s.", ver, next))
	return nil
}

// Prerelease moves the tag v<version> to HEAD and pushes it.
func Prerelease(root string, p *project.Project, o Options) error {
	o.fill()
	if err := clean(root, o); err != nil {
		return err
	}
	if err := ownsTags(root, p, o); err != nil {
		return errf("prerelease refused: %v", err)
	}
	// on a final version the tag is a release: moving it would replace a
	// published release with whatever HEAD holds now
	if v, err := version.Parse(p.Version); err != nil || !v.IsPre() {
		return errf("prerelease refused: %s is not a prerelease -- v%s is a release tag; bump to the next -dev version first", p.Version, p.Version)
	}
	tag := "v" + p.Version
	o.Log(fmt.Sprintf("Prerelease %s...", tag))
	o.Git(root, "tag", "-d", tag)                  // may not exist
	o.Git(root, "push", "origin", "--delete", tag) // may not exist
	if err := git(root, o, "tag", tag); err != nil {
		return err
	}
	if err := git(root, o, "push", "origin", tag); err != nil {
		return err
	}
	o.Log(fmt.Sprintf("Prerelease %s pushed.", tag))
	return nil
}

func git(root string, o Options, args ...string) error {
	_, err := o.Git(root, args...)
	if err == nil {
		switch args[0] {
		case "commit":
			o.Log("Committed: " + args[2])
		case "tag":
			o.Log("Tagged: " + args[1])
		case "push":
			if args[2] != "HEAD" {
				o.Log("Pushed " + args[2] + " to origin")
			}
		}
	}
	return err
}

// smpCheck refuses a release whose SMP package could not be built: a patch
// version with a derived FMID would spend the minor's id a second time.
func smpCheck(root string, p *project.Project, ver string) error {
	if p.Raw["distribution"] == nil {
		return nil
	}
	v, _ := version.Parse(ver)
	// the derived FMID ignores the patch; only an explicit one may ship it
	if v.Patch != 0 && !hasExplicitFMID(root, p.File) {
		return errf("release %s refused: a patch release would ship under the minor's FMID again; set [smp] fmid (and delete) explicitly -- a PTF is not built yet (design §6.4)", ver)
	}
	return nil
}

// nextFMIDCheck refuses a next version an SMP package cannot express --
// after x.y.9 the default x.y.10-dev -- before anything is tagged: the tree
// would no longer load once bumped to it.
func nextFMIDCheck(p *project.Project, rel version.Version, next string) error {
	if p.Raw["distribution"] == nil {
		return nil
	}
	nv, _ := version.Parse(next)
	if nv.Major <= 9 && nv.Minor <= 9 && nv.Patch <= 9 {
		return nil
	}
	way := fmt.Sprintf("%d.%d.0-dev", rel.Major, rel.Minor+1)
	if rel.Minor+1 > 9 {
		way = fmt.Sprintf("%d.0.0-dev", rel.Major+1)
	}
	return errf("next version %s has a component above 9 -- an FMID has one digit per component; release with --next %s", next, way)
}

var fmidLineRE = regexp.MustCompile(`(?m)^\s*fmid\s*=`)

func hasExplicitFMID(root, file string) bool {
	data, _ := os.ReadFile(filepath.Join(root, file))
	return fmidLineRE.Match(data)
}

// bump rewrites the version in the project file and the version files, and
// stages them.
func bump(root string, p *project.Project, from, to string, o Options) error {
	files := []string{p.File}
	if r, ok := p.Raw["release"].(map[string]any); ok {
		if l, ok := r["version_files"].([]any); ok {
			for _, f := range l {
				if s, ok := f.(string); ok && s != p.File {
					files = append(files, s)
				}
			}
		}
	}
	for i, f := range files {
		o.Log(fmt.Sprintf("Updating %s (%s -> %s)...", f, from, to))
		path := filepath.Join(root, f)
		data, err := os.ReadFile(path)
		if err != nil {
			return errf("version file %s: %v", f, err)
		}
		var out string
		if i == 0 {
			out, err = setProjectVersion(string(data), from, to)
		} else {
			out, err = replaceVersion(string(data), from, to, f)
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return err
		}
	}
	return git(root, o, append([]string{"add"}, files...)...)
}

var headerRE = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(#.*)?$`)

// setProjectVersion changes `version = "from"` in [project] and nowhere
// else: a dependency range or a libc370 pin can carry the same string.
func setProjectVersion(text, from, to string) (string, error) {
	lines := strings.Split(text, "\n")
	in := false
	re := regexp.MustCompile(`^(\s*version\s*=\s*)"` + regexp.QuoteMeta(from) + `"`)
	for i, l := range lines {
		if m := headerRE.FindStringSubmatch(l); m != nil {
			in = strings.TrimSpace(m[1]) == "project"
			continue
		}
		if in && re.MatchString(l) {
			lines[i] = re.ReplaceAllString(l, `${1}"`+to+`"`)
			return strings.Join(lines, "\n"), nil
		}
	}
	return "", errf("version \"%s\" not found in [project]", from)
}

// replaceVersion is v2's rule for a version file: the quoted version where
// there is one, else the bare one.
func replaceVersion(text, from, to, file string) (string, error) {
	out := strings.ReplaceAll(text, `"`+from+`"`, `"`+to+`"`)
	if out == text {
		out = strings.ReplaceAll(text, from, to)
	}
	if out == text {
		return "", errf("version '%s' not found in %s", from, file)
	}
	return out, nil
}

// ownsTags: the tags v<version> of a repository belong to one project -- the
// one whose project file is at the repository root, or the only one (#85).
func ownsTags(root string, p *project.Project, o Options) error {
	top, err := o.Git(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("not inside a git repository")
	}
	topDir, _ := filepath.EvalSymlinks(strings.TrimSpace(top))
	listed, _ := o.Git(topDir, "ls-files", "--full-name", "mbt.toml", "*/mbt.toml", "project.toml", "*/project.toml")
	var manifests []string
	seen := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(listed), "\n") {
		b := filepath.Base(l)
		if l != "" && (b == project.FileV3 || b == project.FileV2) && !seen[l] {
			seen[l] = true
			manifests = append(manifests, l)
		}
	}
	rootDir, _ := filepath.EvalSymlinks(root)
	rel, _ := filepath.Rel(topDir, filepath.Join(rootDir, p.File))
	this := filepath.ToSlash(rel)
	owner := ""
	for _, m := range manifests {
		if m == project.FileV3 || m == project.FileV2 {
			owner = m
		}
	}
	if owner == "" {
		if len(manifests) != 1 {
			return fmt.Errorf("this repository holds %d project files and none at its root (%s), so it is not clear whose tags v<version> are -- put the owning project's at the root", len(manifests), strings.Join(manifests, ", "))
		}
		owner = manifests[0]
	}
	if this != owner {
		return fmt.Errorf("the tags of this repository belong to the project in %s; %s may not move them", owner, this)
	}
	return nil
}
