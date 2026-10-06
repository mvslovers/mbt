package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return string(out)
}

const toml = `schema = 3
[project]
name = "sbx"
version = "VER"
[toolchain]
libc370 = "1.4.0-dev"   # the same string as the version, on purpose
[module.HELLO]
sources = ["src/hello.c"]
rent = true
reus = true
[distribution]
[smp]
prefix = "TSBX"
EXTRA`

// repo makes a clone with a bare origin, mbt.toml at the given version.
func repo(t *testing.T, ver, extra string) (string, string) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	work := filepath.Join(base, "work")
	sh(t, base, "git", "init", "-q", "--bare", "-b", "main", origin)
	sh(t, base, "git", "clone", "-q", origin, work)
	os.MkdirAll(filepath.Join(work, "src"), 0o755)
	os.WriteFile(filepath.Join(work, "src", "hello.c"), nil, 0o644)
	body := strings.Replace(strings.Replace(toml, "VER", ver, 1), "EXTRA", extra, 1)
	os.WriteFile(filepath.Join(work, "mbt.toml"), []byte(body), 0o644)
	sh(t, work, "git", "add", ".")
	sh(t, work, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")
	sh(t, work, "git", "push", "-q", "origin", "HEAD:main")
	sh(t, work, "git", "branch", "-q", "-u", "origin/main")
	return work, origin
}

func opts(t *testing.T) Options {
	return Options{Git: func(dir string, args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), errf("git %v: %s", args, out)
		}
		return string(out), nil
	}}
}

func load(t *testing.T, work string) *project.Project {
	p, err := project.Load(work)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRelease(t *testing.T) {
	work, origin := repo(t, "1.4.0-dev", "")
	if err := Release(work, load(t, work), "1.4.0", "", opts(t)); err != nil {
		t.Fatal(err)
	}
	tags := sh(t, origin, "git", "tag")
	if strings.TrimSpace(tags) != "v1.4.0" {
		t.Errorf("tags on origin: %q", tags)
	}
	released := sh(t, origin, "git", "show", "v1.4.0:mbt.toml")
	if !strings.Contains(released, `version = "1.4.0"`) || !strings.Contains(released, `libc370 = "1.4.0-dev"`) {
		t.Errorf("released file:\n%s", released)
	}
	head := sh(t, origin, "git", "show", "main:mbt.toml")
	if !strings.Contains(head, `version = "1.4.1-dev"`) || !strings.Contains(head, `libc370 = "1.4.0-dev"`) {
		t.Errorf("main after release:\n%s", head)
	}
	log := sh(t, origin, "git", "log", "--format=%s", "main")
	if !strings.HasPrefix(log, "chore: bump to 1.4.1-dev\nrelease: v1.4.0\n") {
		t.Errorf("log:\n%s", log)
	}
}

func TestReleaseRefusals(t *testing.T) {
	work, _ := repo(t, "1.4.0-dev", "")
	for want, call := range map[string]func() error{
		"expected 1.4.1-dev":   func() error { return Release(work, load(t, work), "1.4.1", "", opts(t)) },
		"must be a prerelease": func() error { return Release(work, load(t, work), "1.4.0", "1.5.0", opts(t)) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	// a patch release with a derived FMID
	work, _ = repo(t, "1.4.1-dev", "")
	if err := Release(work, load(t, work), "1.4.1", "", opts(t)); err == nil || !strings.Contains(err.Error(), "minor's FMID") {
		t.Errorf("patch: %v", err)
	}
	// ... is fine with an explicit one
	work, _ = repo(t, "1.4.1-dev", "fmid = \"TSBX141\"\ndelete = [\"TSBX140\"]\n")
	if err := Release(work, load(t, work), "1.4.1", "", opts(t)); err != nil {
		t.Errorf("patch with explicit fmid: %v", err)
	}
	// a dirty tree
	work, _ = repo(t, "1.4.0-dev", "")
	os.WriteFile(filepath.Join(work, "src", "hello.c"), []byte("x"), 0o644)
	if err := Release(work, load(t, work), "1.4.0", "", opts(t)); err == nil || !strings.Contains(err.Error(), "not clean") {
		t.Errorf("dirty: %v", err)
	}
}

func TestPrereleaseMovesTheTag(t *testing.T) {
	work, origin := repo(t, "1.4.0-dev", "")
	o := opts(t)
	if err := Prerelease(work, load(t, work), o); err != nil {
		t.Fatal(err)
	}
	first := sh(t, origin, "git", "rev-parse", "v1.4.0-dev")
	os.WriteFile(filepath.Join(work, "src", "hello.c"), []byte("x"), 0o644)
	sh(t, work, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-am", "more")
	sh(t, work, "git", "push", "-q")
	if err := Prerelease(work, load(t, work), o); err != nil {
		t.Fatal(err)
	}
	if second := sh(t, origin, "git", "rev-parse", "v1.4.0-dev"); second == first {
		t.Error("the tag did not move")
	}
}

// Another project's tags are not this one's to move (#85).
func TestTagOwner(t *testing.T) {
	work, _ := repo(t, "1.4.0-dev", "")
	sub := filepath.Join(work, "tools")
	os.MkdirAll(filepath.Join(sub, "src"), 0o755)
	os.WriteFile(filepath.Join(sub, "src", "hello.c"), nil, 0o644)
	os.WriteFile(filepath.Join(sub, "mbt.toml"), []byte(strings.Replace(strings.Replace(toml, "VER", "1.4.0-dev", 1), "EXTRA", "", 1)), 0o644)
	sh(t, work, "git", "add", ".")
	sh(t, work, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "sub")
	if err := Prerelease(sub, load(t, sub), opts(t)); err == nil || !strings.Contains(err.Error(), "belong to the project in mbt.toml") {
		t.Errorf("sub-project prerelease: %v", err)
	}
}
