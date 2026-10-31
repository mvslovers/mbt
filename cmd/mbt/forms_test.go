package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const formsBase = `schema = 3
[project]
name = "demo"
version = "1.0.0"
[module.DEMO]
sources = ["src/demo.c"]
rent = true
reus = true
[tests]
rent = true
reus = true
[distribution]
%s
[smp]
prefix = "TDEM"
%s
`

// Every command that reads mbt.toml refuses a table in the wrong form and a
// misspelt key, with exit code 2 and a message that names them (mbt#312).
func TestCommandsRefuseBadMbtToml(t *testing.T) {
	cases := []struct {
		name, dist, smp string
		want            []string
	}{
		{"array library", "[[distribution.library]]\ndir = \"samplib\"", "", []string{"[[distribution.library]]", "[distribution.library.DIR]"}},
		{"misspelt key", "readme = \"README.md\"", "prefx = \"TDEM\"", []string{"unknown key 'prefx' in [smp]"}},
	}
	commands := [][]string{
		{"build"}, {"build", "--all"}, {"test"}, {"package"}, {"dist"}, {"deploy", "--dry-run"},
		{"deps"}, {"compiledb"}, {"ci-info"}, {"module-data"}, {"release", "1.0.1"}, {"prerelease"},
		{"run", "nothing"}, {"doctor", "--offline"},
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MBT_HOME", home)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)

	for _, c := range cases {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "mbt.toml"), []byte(sprintfForms(c.dist, c.smp)), 0o644)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		for _, args := range commands {
			got, code := captureStderr(func() int { return dispatch(args) })
			if code != exitConfig {
				t.Errorf("%s: mbt %s: exit %d, want %d (%s)", c.name, strings.Join(args, " "), code, exitConfig, got)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("%s: mbt %s: message %q lacks %q", c.name, strings.Join(args, " "), got, w)
				}
			}
		}
	}
}

func sprintfForms(dist, smp string) string {
	return strings.Replace(strings.Replace(formsBase, "%s", dist, 1), "%s", smp, 1)
}

func captureStderr(f func() int) (string, int) {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	code := f()
	w.Close()
	os.Stderr = old
	return <-done, code
}
