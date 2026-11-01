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
	if !strings.Contains(p.DistError, "prerelease") {
		t.Errorf("DistError %q", p.DistError)
	}
}

// A prerelease must not get the FMID of the release it leads to: an install
// of a -dev build would spend it. The FMID has to be set, throwaway, by hand.
func TestPrereleaseNeedsExplicitFMID(t *testing.T) {
	old := ReleasedPatches
	defer func() { ReleasedPatches = old }()
	ReleasedPatches = func(string, int, int) []string { return nil }
	load := func(ver, smp string) *Project {
		t.Helper()
		in := strings.Replace(v3Base, "1.4.0-dev", ver, 1)
		if smp != "" {
			in = strings.Replace(in, `prefix = "TUFS"`, smp, 1)
		}
		p, err := LoadV3(v3Tree(t, in), FileV3)
		if err != nil {
			t.Fatalf("%s: %v", ver, err)
		}
		return p
	}
	for _, ver := range []string{"1.4.0-dev", "1.4.0-rc1", "1.4.1-dev", "1.4.2-rc1"} {
		d := load(ver, "").DistError
		for _, want := range []string{"prerelease", "TUFS140", "[smp] fmid and delete explicitly"} {
			if !strings.Contains(d, want) {
				t.Errorf("%s: DistError %q lacks %q", ver, d, want)
			}
		}
		if d := load(ver, `prefix = "TUFS"`+"\ndelete = [\"TUFS130\"]").DistError; !strings.Contains(d, "prerelease") {
			t.Errorf("%s with delete only: DistError %q", ver, d)
		}
		if p := load(ver, `fmid = "TUFS14D"`+"\ndelete = [\"TUFS130\"]"); p.DistError != "" {
			t.Errorf("%s with fmid and delete: DistError %q", ver, p.DistError)
		}
	}
	if p := load("1.4.0", ""); p.DistError != "" || table(table(p.Raw, "distribution"), "smp")["fmid"] != "TUFS140" {
		t.Errorf("release: DistError %q", p.DistError)
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

// mbt#243: [tso.NAME] fixtures for mbt test --tso.
func TestV3TSOFixtures(t *testing.T) {
	root := v3Tree(t, v3Base+"[tso.fssmenu]\nfixtures = { tsotest = [\"t/a.rexx\"], RXLIB = [\"r/X.rexx\", \"r/Y.rexx\"] }\n")
	p, err := LoadV3(root, FileV3)
	if err != nil {
		t.Fatal(err)
	}
	fx := table(table(p.Raw, "tso"), "FSSMENU")["fixtures"].(map[string]any)
	if !reflect.DeepEqual(fx["TSOTEST"], []string{"t/a.rexx"}) || !reflect.DeepEqual(fx["RXLIB"], []string{"r/X.rexx", "r/Y.rexx"}) {
		t.Errorf("fixtures %v", fx)
	}
	// a test's own terminal size, COLSxROWS
	p, err = LoadV3(v3Tree(t, v3Base+"[tso.ispf]\nsize = \"160x62\"\n"), FileV3)
	if err != nil || table(table(p.Raw, "tso"), "ISPF")["size"] != "160x62" {
		t.Errorf("size: %v", err)
	}
	for add, want := range map[string]string{
		"[tso.FSS]\nsize = \"43x80\"\n":                  "write COLSxROWS",
		"[tso.FSS]\nsize = 4\n":                          "size must be a string",
		"[tso.FSS]\nfixture = {}\n":                      "unknown key 'fixture' in [tso.FSS]",
		"[tso.toolongname]\n":                            "a data set qualifier",
		"[tso.FSS]\nfixtures = { \"1ABC\" = [\"x\"] }\n": "last qualifier",
		"[tso.FSS]\nfixtures = { RXLIB = \"x\" }\n":      "non-empty list of files",
		"[tso.FSS]\nfixtures = { RXLIB = [] }\n":         "non-empty list of files",
		"[tso.FSS]\nfixtures = [\"x\"]\n":                "must be a table",
		"[tso.FSS]\n[tso.fss]\n":                         "declared twice",
	} {
		if _, err := LoadV3(v3Tree(t, v3Base+add), FileV3); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

// COBOL units: cobflags pass through; entry is required; rent = true is refused.
func TestV3COBOL(t *testing.T) {
	base := "schema = 3\n[project]\nname = \"cob\"\nversion = \"0.1.0\"\n[build]\ncobflags = [\"-I\", \"copy\"]\n" +
		"[module.HELLO]\nsources = [\"src/hello.cbl\"]\nrent = false\nreus = false\n"
	files := func(toml string) string {
		return writeTree(t, map[string]string{"mbt.toml": toml, "src/hello.cbl": ""})
	}
	p, err := LoadV3(files(base+"entry = \"HELLO\"\n"), FileV3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.COBFlags, []string{"-I", "copy"}) || p.Modules[0].Entry != "HELLO" {
		t.Errorf("%v %q", p.COBFlags, p.Modules[0].Entry)
	}
	if _, err := LoadV3(files(base), FileV3); err == nil || !strings.Contains(err.Error(), "set 'entry' to the PROGRAM-ID") {
		t.Errorf("no entry: %v", err)
	}
	if _, err := LoadV3(files(strings.Replace(base, "rent = false", "rent = true", 1)+"entry = \"HELLO\"\n"), FileV3); err == nil || !strings.Contains(err.Error(), "not reentrant") {
		t.Errorf("rent: %v", err)
	}
	// a C main() among the sources: COBOL is called from C, @@CRT0 stays
	cmain := strings.Replace(base, `sources = ["src/hello.cbl"]`, `sources = ["src/main.c", "src/hello.cbl"]`, 1)
	for src, isMain := range map[string]bool{
		"#include <stdio.h>\nint main(void)\n{\n    return 0;\n}\n": true,
		"int\nmain (int argc, char **argv) { return 0; }\n":         true, // the type on a line of its own
		"main()\n{\n}\n": true,
		"/* calls domain(x) */\nint helper(void) { return 0; }\n": false,
	} {
		p, err := LoadV3(writeTree(t, map[string]string{"mbt.toml": cmain, "src/hello.cbl": "", "src/main.c": src}), FileV3)
		switch {
		case isMain && (err != nil || p.Modules[0].Entry != "@@CRT0"):
			t.Errorf("main in %q: %v", src, err)
		case !isMain && (err == nil || !strings.Contains(err.Error(), "no C main()")):
			t.Errorf("no main in %q: %v", src, err)
		}
	}
}

// A library delivered as SMP elements: element, distlib and stage reach the
// distribution section untouched, and an unknown per-library key (a record
// format, say) is still refused.
func TestLoadV3ElementLibrary(t *testing.T) {
	in := strings.Replace(v3Base, "[distribution.library.samplib]\n",
		"[distribution.library.samplib]\nelement = \"MAC\"\ndistlib = \"UFSD.ASAMP\"\nstage = \"UFSD.TSAMP\"\n", 1)
	p, err := LoadV3(v3Tree(t, in), FileV3)
	if err != nil {
		t.Fatal(err)
	}
	lib := tables(table(p.Raw, "distribution"), "library")
	if len(lib) != 1 || lib[0]["element"] != "MAC" || lib[0]["distlib"] != "UFSD.ASAMP" ||
		lib[0]["stage"] != "UFSD.TSAMP" || lib[0]["target"] != "UFSD.SAMPLIB" {
		t.Errorf("library %v", lib)
	}
	bad := strings.Replace(v3Base, "[distribution.library.samplib]\n", "[distribution.library.samplib]\nelement = \"MAC\"\nrecfm = \"VB\"\n", 1)
	if _, err := LoadV3(v3Tree(t, bad), FileV3); err == nil || !strings.Contains(err.Error(), "recfm") {
		t.Errorf("recfm accepted: %v", err)
	}
}

// A table written in the wrong form is an error that names the key and the
// form to write, never a table that is quietly left out (mbt#312).
func TestLoadV3SMPVolumes(t *testing.T) {
	in := strings.Replace(v3Base, `prefix = "TUFS"`, "prefix = \"TUFS\"\ntarget_volume = \"TK5RES\"\ndlib_volume = \"TK5DLB\"", 1)
	p, err := LoadV3(v3Tree(t, in), FileV3)
	if err != nil {
		t.Fatal(err)
	}
	smp := table(p.Raw, "distribution")
	smp = table(smp, "smp")
	if smp["target_volume"] != "TK5RES" || smp["dlib_volume"] != "TK5DLB" {
		t.Errorf("volumes not carried into the descriptor: %v", smp)
	}
}

func TestLoadV3SMPDDNames(t *testing.T) {
	in := strings.Replace(v3Base, `prefix = "TUFS"`, "prefix = \"TUFS\"\ntarget_dd = \"UFSLINK\"\ndistlib_dd = \"AUFSLOD\"\nlklib_dd = \"UFSLOAD\"", 1)
	in = strings.Replace(in, "[distribution.library.samplib]\n", "[distribution.library.samplib]\nelement = \"SRC\"\ntarget_dd = \"UFSSAMP\"\ndistlib_dd = \"AUFSSAMP\"\n", 1)
	p, err := LoadV3(v3Tree(t, in), FileV3)
	if err != nil {
		t.Fatal(err)
	}
	dist := table(p.Raw, "distribution")
	smp := table(dist, "smp")
	if smp["target_dd"] != "UFSLINK" || smp["distlib_dd"] != "AUFSLOD" || smp["lklib_dd"] != "UFSLOAD" {
		t.Errorf("DD names not carried into the descriptor: %v", smp)
	}
	lib := dist["library"].([]map[string]any)[0]
	if lib["target_dd"] != "UFSSAMP" || lib["distlib_dd"] != "AUFSSAMP" {
		t.Errorf("library DD names not carried into the descriptor: %v", lib)
	}
}

func TestLoadV3SMPPredecessor(t *testing.T) {
	in := v3Base + "[smp.predecessor]\nLINKLIB = \"UFSD.LINKLIB\"\nAUFSLOD = \"UFSD.AUFSLOD\"\n"
	p, err := LoadV3(v3Tree(t, in), FileV3)
	if err != nil {
		t.Fatal(err)
	}
	pre := table(table(table(p.Raw, "distribution"), "smp"), "predecessor")
	if pre["LINKLIB"] != "UFSD.LINKLIB" || pre["AUFSLOD"] != "UFSD.AUFSLOD" {
		t.Errorf("[smp.predecessor] not carried into the descriptor: %v", pre)
	}
}

func TestLoadV3WrongForm(t *testing.T) {
	lib := "[distribution.library.samplib]\n"
	cases := []struct {
		name string
		edit func(string) string
		want []string
	}{
		{"array library", func(s string) string {
			return strings.Replace(s, lib, "[[distribution.library]]\ndir = \"samplib\"\n", 1)
		},
			[]string{"[[distribution.library]]", "[distribution.library.DIR]"}},
		{"library with keys of its own", func(s string) string {
			return strings.Replace(s, lib, "[distribution.library]\ntarget = \"UFSD.SAMPLIB\"\n", 1)
		},
			[]string{"distribution.library", "'target'", "[distribution.library.DIR]"}},
		{"array build", func(s string) string { return strings.Replace(s, "[build]\n", "[[build]]\n", 1) },
			[]string{"[[build]]", "[build]"}},
		{"array smp", func(s string) string { return strings.Replace(s, "[smp]\n", "[[smp]]\n", 1) },
			[]string{"[[smp]]", "[smp]"}},
		{"array distribution", func(s string) string { return strings.Replace(s, "[distribution]\n", "[[distribution]]\n", 1) },
			[]string{"[[distribution]]", "[distribution]"}},
		{"array project", func(s string) string { return strings.Replace(s, "[project]\n", "[[project]]\n", 1) },
			[]string{"[[project]]", "[project]"}},
		{"array deploy", func(s string) string { return s + "[[deploy]]\ntarget = \"X.Y\"\n" },
			[]string{"[[deploy]]", "[deploy]"}},
		{"array tests", func(s string) string { return strings.Replace(s, "[tests]\n", "[[tests]]\n", 1) },
			[]string{"[[tests]]", "[tests]"}},
		{"array build.host", func(s string) string { return s + "[[build.host]]\ncc = \"cc\"\n" },
			[]string{"[[build.host]]", "[build.host]"}},
		{"array tso", func(s string) string { return s + "[[tso]]\nsize = \"80x24\"\n" },
			[]string{"[[tso]]", "[tso.NAME]"}},
		{"project a string", func(s string) string {
			return strings.Replace(s, "[project]\nname = \"ufsd\"\nversion = \"1.4.0-dev\"\n", "project = \"ufsd\"\n", 1)
		},
			[]string{"project is a string", "[project]"}},
		{"parm an array", func(s string) string {
			return strings.Replace(s, `parm = { batch = "0", tso = "1" }`, `parm = ["0"]`, 1)
		},
			[]string{"test.TSTB.parm", "an array"}},
		{"fixtures a list", func(s string) string {
			return strings.Replace(s, `fixtures = { SYSPROC = ["f/b"], SYSEXEC = ["f/a"] }`, `fixtures = ["f/a"]`, 1)
		}, []string{"test.TSTB.fixtures"}},
		{"dependency a table", func(s string) string { return s + "[dependencies.\"mvslovers/libc370\"]\nversion = \"^2\"\n" },
			[]string{"mvslovers/libc370", "range"}},
		{"tools array", func(s string) string { return s + "[[tools]]\nrepo = \"a/b\"\n" },
			[]string{"[[tools]]", "[tools]"}},
		{"tool unknown key", func(s string) string { return s + "[tools.x]\nrepo = \"a/b\"\nversion = \"1\"\nrelease = \"2\"\n" },
			[]string{"[tools]", "'release'"}},
		{"plugins wrong", func(s string) string { return s + "[plugins]\nufs = \"^1\"\n" },
			[]string{"[plugins]", "ufs"}},
	}
	for _, c := range cases {
		_, err := LoadV3(v3Tree(t, c.edit(v3Base)), FileV3)
		if err == nil {
			t.Errorf("%s: loaded without an error", c.name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q lacks %q", c.name, err, w)
			}
		}
		if !strings.Contains(err.Error(), "mbt.toml") {
			t.Errorf("%s: %q does not name the file", c.name, err)
		}
	}
}

// A misspelt key is an error in every table that has keys of its own.
func TestLoadV3UnknownKeyEverywhere(t *testing.T) {
	cases := []struct{ name, from, to, want string }{
		{"top level", "schema = 3\n", "schema = 3\nbogus = 1\n", "'bogus' in the top level"},
		{"project", `version = "1.4.0-dev"`, "version = \"1.4.0-dev\"\nkindd = \"library\"", "'kindd' in [project]"},
		{"build", `cflags = ["-Wall"]`, `cflags = ["-Wall"]` + "\nmaxrc = 4", "'maxrc' in [build]"},
		{"module", "[module.UFSD]\n", "[module.UFSD]\nsrc = []\n", "'src' in [module.UFSD]"},
		{"test", "[test.TSTB]\n", "[test.TSTB]\nhots = true\n", "'hots' in [test.TSTB]"},
		{"tests", "[tests]\n", "[tests]\nrnt = true\n", "'rnt' in [tests]"},
		{"distribution", `readme = "README.md"`, "readme = \"README.md\"\nreadmee = \"x\"", "'readmee' in [distribution]"},
		{"library", "[distribution.library.samplib]\n", "[distribution.library.samplib]\nelements = \"MAC\"\n", "'elements' in [distribution.library.samplib]"},
		{"smp", `prefix = "TUFS"`, "prefix = \"TUFS\"\nprefx = \"x\"", "'prefx' in [smp]"},
		{"smp volume", `prefix = "TUFS"`, "prefix = \"TUFS\"\ntarget_volum = \"MVSRES\"", "'target_volum' in [smp]"},
	}
	for _, c := range cases {
		in := strings.Replace(v3Base, c.from, c.to, 1)
		if in == v3Base {
			t.Fatalf("%s: edit did not apply", c.name)
		}
		_, err := LoadV3(v3Tree(t, in), FileV3)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

// Every mbt.toml the repository ships as an example or in the books loads:
// the checks of mbt#312 must not turn a documented file into an error.
func TestShippedMbtTomlLoads(t *testing.T) {
	top := filepath.Join("..", "..")
	n := 0
	err := filepath.WalkDir(top, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".mbt") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != FileV3 {
			return nil
		}
		n++
		if _, err := LoadV3(filepath.Dir(p), FileV3); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no mbt.toml found under the repository root")
	}
}

// parm is a string for both runs, or a table with one per run.
func TestLoadV3ParmString(t *testing.T) {
	in := strings.Replace(v3Base, `parm = { batch = "0", tso = "1" }`, `parm = "21771"`, 1)
	p, err := LoadV3(v3Tree(t, in), FileV3)
	if err != nil {
		t.Fatal(err)
	}
	if tb := p.RawTables("test")[1]; tb["parm"] != "21771" {
		t.Errorf("TSTB %v", tb)
	}
}
