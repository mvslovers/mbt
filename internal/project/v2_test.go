package project

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProject(t *testing.T, toml string, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	return dir
}

func load(t *testing.T, toml string, files ...string) (*Project, error) {
	return LoadV2(writeProject(t, toml, files...), "project.toml")
}

func TestGlobIsSortedAndSkipsDotFiles(t *testing.T) {
	p, err := load(t, `
[project]
name = "x"
[[module]]
name = "M"
sources = ["src/*.c", "src/a.c"]
exclude = ["src/c.c"]
`, "src/b.c", "src/a.c", "src/c.c", "src/.hidden.c")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"src/a.c", "src/b.c"} // sorted, excluded, deduplicated, no dot file
	if got := p.Modules[0].Sources; !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
}

func TestStartupValues(t *testing.T) {
	cases := []struct {
		val       string
		startup   Startup
		startfile string
		bad       bool
	}{
		{"", StartupC, "crt0", false},
		{`startup = "crt1"`, StartupC, "crt1", false},
		{`startup = "crt0"`, StartupC, "crt0", false},
		{`startup = "crtm"`, StartupCRTM, "", false},
		{`startup = false`, StartupNone, "", false},
		{`startup = true`, 0, "", true},
		{`startup = "bogus"`, 0, "", true},
	}
	for _, c := range cases {
		p, err := load(t, "[[module]]\nname = \"M\"\n"+c.val+"\n")
		if c.bad {
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Errorf("%q: want a ConfigError, got %v", c.val, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.val, err)
		}
		u := p.Modules[0]
		if u.Startup != c.startup || u.Startfile != c.startfile {
			t.Errorf("%q: startup=%v startfile=%q", c.val, u.Startup, u.Startfile)
		}
	}
}

func TestLegacyStartupWarnsOncePerProject(t *testing.T) {
	p, err := load(t, `
[[module]]
name = "A"
startup = "crt1"
[[module]]
name = "B"
startup = "crt1"
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "2 module(s)/test(s)") {
		t.Errorf("warnings = %q", p.Warnings)
	}
}

func TestLinkAttributes(t *testing.T) {
	p, err := load(t, `
[[module]]
name = "A"
rent = true
reus = false
refr = false
[[module]]
name = "B"
norent = true
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Modules[0].Attrs; !reflect.DeepEqual(got, []string{"--rent", "--noreus"}) {
		t.Errorf("A attrs = %v", got)
	}
	if got := p.Modules[1].Attrs; !reflect.DeepEqual(got, []string{"--norent"}) {
		t.Errorf("B attrs = %v", got)
	}
	if len(p.Warnings) != 1 {
		t.Errorf("norent should warn once: %q", p.Warnings)
	}
	if _, err := load(t, "[[module]]\nname = \"A\"\nrent = 1\n"); err == nil {
		t.Error("a non-boolean rent was accepted")
	}
	if _, err := load(t, "[[module]]\nname = \"A\"\nrent = true\nnorent = true\n"); err == nil {
		t.Error("rent and norent together were accepted")
	}
}

func TestMemberNamesAndAliases(t *testing.T) {
	bad := []string{
		"[[module]]\nname = \"TOOLONGNAME\"\n",
		"[[module]]\nname = \"1ABC\"\n",
		"[[module]]\nname = \"A\"\naliases = [\"A\"]\n",
		"[[module]]\nname = \"A\"\naliases = [\"X\"]\n[[module]]\nname = \"B\"\naliases = [\"X\"]\n",
		"[[test]]\nname = \"T\"\naliases = [\"X\"]\n",
	}
	for _, b := range bad {
		if _, err := load(t, b); err == nil {
			t.Errorf("accepted:\n%s", b)
		}
	}
	p, err := load(t, "[[module]]\nname = \"IRX#HELO\"\naliases = [\"RX\"]\n")
	if err != nil || p.Modules[0].Aliases[0] != "RX" {
		t.Errorf("a valid national-character name with an alias: %v", err)
	}
}

func TestHostOnlyTestIsSkipped(t *testing.T) {
	p, err := load(t, "[[test]]\nname = \"T\"\nmvs = false\n[[test]]\nname = \"U\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tests) != 1 || p.Tests[0].Name != "U" {
		t.Errorf("tests = %v", p.Tests)
	}
}

func TestObjectPath(t *testing.T) {
	for src, want := range map[string]string{
		"src/ufsd#cmd.c":  "build/ufsd#cmd.o",
		"client/libufs.c": "build/libufs.o",
		"asm/foo.asm":     "build/foo.o",
	} {
		if got := ObjectPath("build", src); got != want {
			t.Errorf("%s -> %s, want %s", src, got, want)
		}
	}
}
