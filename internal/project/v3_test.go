package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/version"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const v3Base = `schema = 3
[project]
name = "ufsd"
version = "1.4.0-dev"
[build]
include = ["include"]
cflags = ["-Wall"]
[module.UFSD]
sources = ["src/ufsd.c"]
rent = true
reus = true
[module."UFS#A"]
sources = ["src/a.c"]
rent = false
reus = false
[tests]
rent = false
reus = false
exclude = ["test/helper.c"]
[test.TSTB]
host = false
parm = { batch = "0", tso = "1" }
fixtures = { SYSPROC = ["f/b"], SYSEXEC = ["f/a"] }
[distribution]
readme = "README.md"
[distribution.library.samplib]
[smp]
prefix = "TUFS"
`

func v3Tree(t *testing.T, mbtToml string) string {
	return writeTree(t, map[string]string{
		"mbt.toml": mbtToml, "src/ufsd.c": "", "src/a.c": "", "README.md": "",
		"test/tsta.c": "", "test/mvs/tstb.c": "", "test/helper.c": "", "samplib/X": "",
	})
}

func TestLoadV3(t *testing.T) {
	root := v3Tree(t, v3Base)
	p, err := LoadV3(root, FileV3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.CFlags, []string{"-I", "include", "-Wall"}) {
		t.Errorf("cflags %q", p.CFlags)
	}
	var names []string
	for _, m := range p.Modules {
		names = append(names, m.Name)
	}
	if !reflect.DeepEqual(names, []string{"UFS#A", "UFSD"}) {
		t.Errorf("modules not in name order: %v", names)
	}
	if !reflect.DeepEqual(p.Modules[1].Attrs, []string{"--rent", "--reus"}) {
		t.Errorf("UFSD attrs %v", p.Modules[1].Attrs)
	}
	names = nil
	for _, u := range p.Tests {
		names = append(names, u.Name+"="+strings.Join(u.Sources, ","))
	}
	if !reflect.DeepEqual(names, []string{"TSTA=test/tsta.c", "TSTB=test/mvs/tstb.c"}) {
		t.Errorf("discovered tests: %v", names)
	}
	tb := p.RawTables("test")[1]
	if tb["parm_batch"] != "0" || tb["parm_tso"] != "1" || tb["host"] != false {
		t.Errorf("TSTB %v", tb)
	}
	fx := tables(tb, "fixture")
	if len(fx) != 2 || fx[0]["dd"] != "SYSEXEC" || fx[1]["dd"] != "SYSPROC" {
		t.Errorf("fixtures not in DD order: %v", fx)
	}
	smp := table(table(p.Raw, "distribution"), "smp")
	if smp["fmid"] != "TUFS140" || !reflect.DeepEqual(smp["delete"], []any{"TUFS130"}) ||
		smp["lklib"] != "UFSD.UFSDLOAD" || smp["distlib"] != "UFSD.AUFSDLOD" || smp["target"] != "UFSD.LINKLIB" {
		t.Errorf("smp %v", smp)
	}
	lib := tables(table(p.Raw, "distribution"), "library")
	if len(lib) != 1 || lib[0]["target"] != "UFSD.SAMPLIB" {
		t.Errorf("library %v", lib)
	}
	if table(p.Raw, "deploy")["target"] != "UFSD.DEV.LINKLIB" {
		t.Errorf("deploy %v", p.Raw["deploy"])
	}
	if p.DistError != "" {
		t.Errorf("DistError %q", p.DistError)
	}
}

func TestLoadV3Errors(t *testing.T) {
	cases := map[string]struct{ edit func(string) string }{
		"'rent' is required": {func(s string) string { return strings.Replace(s, "rent = true\n", "", 1) }},
		"startup = \"crt1\" is gone": {func(s string) string {
			return strings.Replace(s, "rent = true\n", "rent = true\nstartup = \"crt1\"\n", 1)
		}},
		"unknown key 'cflag'":  {func(s string) string { return strings.Replace(s, "cflags = [\"-Wall\"]", "cflag = [\"-Wall\"]", 1) }},
		"[[module]] is the v2": {func(s string) string { return strings.Replace(s, "[module.UFSD]", "[[module]]\nname = \"UFSD\"", 1) }},
		"schema = 3":           {func(s string) string { return strings.Replace(s, "schema = 3", "schema = 2", 1) }},
		"is 11 characters":     {func(s string) string { return strings.Replace(s, `exclude = ["test/helper.c"]`, "", 1) + "" }},
		"no previous minor":    {func(s string) string { return strings.Replace(s, "1.4.0-dev", "2.0.0-dev", 1) }},
		"lists other sources":  {func(s string) string { return s + "[test.TSTA]\nsources = [\"test/helper.c\"]\n" }},
		// a DEV library from a name longer than a qualifier: refused, not cut
		"set [deploy] target": {func(s string) string { return strings.Replace(s, `name = "ufsd"`, `name = "ufsdextra10"`, 1) }},
	}
	for want, c := range cases {
		root := v3Tree(t, c.edit(v3Base))
		if want == "is 11 characters" {
			os.WriteFile(filepath.Join(root, "test", "helper_long.c"), nil, 0o644)
		}
		_, err := LoadV3(root, FileV3)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestDeriveFMID(t *testing.T) {
	for _, c := range []struct {
		v, fmid, del string
		ok           bool
	}{
		{"1.4.0", "TUFS140", "TUFS130", true},
		{"4.2.0-dev", "TUFS420", "TUFS410", true},
		{"1.4.3", "TUFS140", "TUFS130", true},
		{"2.0.0", "TUFS200", "", false},
	} {
		v, _ := version.Parse(c.v)
		f, d, ok, err := DeriveFMID("TUFS", v)
		if err != nil || f != c.fmid || d != c.del || ok != c.ok {
			t.Errorf("%s: %s %s %v %v", c.v, f, d, ok, err)
		}
	}
	v, _ := version.Parse("1.10.0")
	if _, _, _, err := DeriveFMID("TUFS", v); err == nil {
		t.Error("1.10.0 derived an FMID")
	}
}

func TestPatchReleaseBlocksDist(t *testing.T) {
	root := v3Tree(t, strings.Replace(v3Base, "1.4.0-dev", "1.4.1", 1))
	p, err := LoadV3(root, FileV3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.DistError, "patch release") {
		t.Errorf("DistError %q", p.DistError)
	}
	root = v3Tree(t, strings.Replace(strings.Replace(v3Base, "1.4.0-dev", "1.4.1", 1), `prefix = "TUFS"`, `fmid = "TUFS141"`+"\ndelete = [\"TUFS140\"]", 1))
	if p, err = LoadV3(root, FileV3); err != nil || p.DistError != "" {
		t.Errorf("explicit fmid: %v %q", err, p.DistError)
	}
}

func TestFindBothFiles(t *testing.T) {
	root := writeTree(t, map[string]string{"mbt.toml": "", "project.toml": ""})
	if _, err := Find(root); err == nil || !strings.Contains(err.Error(), "both") {
		t.Errorf("both: %v", err)
	}
}

// After 1.4.1 shipped under an FMID of its own, 1.5.0 must not derive
// DELETE(TUFS140): TUFS141 owns the modules, and SMP would install nothing.
func TestMinorAfterReleasedPatch(t *testing.T) {
	old := ReleasedPatches
	defer func() { ReleasedPatches = old }()
	ReleasedPatches = func(root string, major, minor int) []string {
		if major == 1 && minor == 4 {
			return []string{"v1.4.1"}
		}
		return nil
	}
	root := v3Tree(t, strings.Replace(v3Base, "1.4.0-dev", "1.5.0", 1))
	p, err := LoadV3(root, FileV3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.DistError, "v1.4.1") || !strings.Contains(p.DistError, "TUFS140") || !strings.Contains(p.DistError, "set [smp] delete explicitly") {
		t.Errorf("DistError %q", p.DistError)
	}
	root = v3Tree(t, strings.Replace(strings.Replace(v3Base, "1.4.0-dev", "1.5.0", 1), `prefix = "TUFS"`, `prefix = "TUFS"`+"\ndelete = [\"TUFS141\"]", 1))
	if p, err = LoadV3(root, FileV3); err != nil || p.DistError != "" {
		t.Errorf("explicit delete: %v %q", err, p.DistError)
	}
	ReleasedPatches = func(string, int, int) []string { return nil }
	root = v3Tree(t, strings.Replace(v3Base, "1.4.0-dev", "1.5.0", 1))
	if p, err = LoadV3(root, FileV3); err != nil || p.DistError != "" {
		t.Errorf("no patch released: %v %q", err, p.DistError)
	}
}

// ReleasedPatches against a real repository: releases only, no prereleases,
// no other minor, in version order.
func TestReleasedPatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	run := func(a ...string) {
		c := exec.Command("git", append([]string{"-C", root}, a...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "x")
	for _, tag := range []string{"v1.4.0", "v1.4.10", "v1.4.2", "v1.4.3-dev", "v1.3.1", "v1.4.1"} {
		run("tag", tag)
	}
	if got := strings.Join(ReleasedPatches(root, 1, 4), ","); got != "v1.4.1,v1.4.2,v1.4.10" {
		t.Errorf("got %q", got)
	}
	if got := ReleasedPatches(t.TempDir(), 1, 4); len(got) != 0 {
		t.Errorf("no repository: %v", got)
	}
}
