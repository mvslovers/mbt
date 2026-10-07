package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const v2File = `# ufsd -- the file comment
[project]
name = "ufsd"
version = "1.4.0-dev"
type = "application"

[toolchain]
# why 2.4.0
libc370 = "2.4.0"

[build]
cflags = ["-I", "include", "-I", "client", "-Wall"]   # warnings on

[host]
cflags = ["-Wextra"]

# -- modules --
[[module]]
name = "UFSD"
startup = "crt1"
rent = true          # checked: no writable data
reus = true
sources = [
  "src/ufsd.c",
  # the helper, kept apart
  "src/a.c",
]

[[module]]
name = "UFS#A"
norent = true
reus = false
sources = ["src/a.c"]

# -- tests --
[[test]]
name = "TSTA"
rent = false
reus = false
sources = ["test/tsta.c"]

[[test]]
name = "TSTB"
rent = false
reus = false
sources = ["test/mvs/tstb.c", "src/a.c"]
parm_batch = "0"
parm_tso = "1"
[[test.fixture]]
dd = "SYSEXEC"
# two members
members = ["f/a", "f/b"]

[distribution]
readme = "README.md"

[distribution.smp]
fmid = "TUFS140"
delete = ["TUFS130"]   # the level this one replaces
system = "Z038"
prereq = []            # RAKF is a runtime one
lklib = "UFSD.UFSDLOAD"

[[distribution.library]]
dir = "samplib"
target = "UFSD.SAMPLIB"

[release]
version_files = ["VERSION"]
`

func tree(t *testing.T) string {
	root := t.TempDir()
	for name, body := range map[string]string{
		"project.toml": v2File, "VERSION": "1.4.0-dev\n", "src/ufsd.c": "", "src/a.c": "", "README.md": "",
		"test/tsta.c": "", "test/mvs/tstb.c": "", "test/not_a_test.c": "", "samplib/X": "",
	} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return root
}

func TestConvert(t *testing.T) {
	root := tree(t)
	res, err := Convert(root)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Text
	for _, want := range []string{
		// a comment directly above [project] stays with it
		"schema = 3\n\n# ufsd -- the file comment\n[project]\n",
		"kind = \"application\"",
		"# why 2.4.0\nlibc370",
		"[build]\ninclude = [\"include\", \"client\"]\n\n",
		"[build.host]\ncflags = [\"-Wextra\"]",
		"# -- modules --\n[module.UFSD]\nrent = true          # checked: no writable data",
		"  # the helper, kept apart\n",
		"[module.\"UFS#A\"]\nrent = false\n",
		"[tests]\nrent = false\nreus = false\nexclude = [\n  \"test/not_a_test.c\",\n]",
		"[test.TSTB]\nsources = [\"test/mvs/tstb.c\", \"src/a.c\"]\nparm.batch = \"0\"\nparm.tso = \"1\"\n# two members\nfixtures.SYSEXEC = [\"f/a\", \"f/b\"]",
		"[smp]\nprefix = \"TUFS\"",
		"prereq = []            # RAKF is a runtime one",
		"[distribution.library.samplib]\ntarget = \"UFSD.SAMPLIB\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing:\n%s\n--- in:\n%s", want, out)
		}
	}
	for _, gone := range []string{"crt1", "[test.TSTA]", "fmid =", "delete =", "system =", "lklib", "[release]", "VERSION", "type ="} {
		if strings.Contains(out, gone) {
			t.Errorf("still there: %q\n%s", gone, out)
		}
	}
	diffs, err := Check(root, out)
	if err != nil || len(diffs) > 0 {
		t.Errorf("check: %v %v", err, diffs)
	}
}

// A conversion whose model differs is caught by Check.
func TestCheckCatchesADifference(t *testing.T) {
	root := tree(t)
	res, _ := Convert(root)
	broken := strings.Replace(res.Text, "rent = true          # checked", "rent = false  # checked", 1)
	diffs, err := Check(root, broken)
	if err != nil || len(diffs) == 0 {
		t.Errorf("a changed rent passed the check: %v", err)
	}
}

func TestMakefileNote(t *testing.T) {
	root := tree(t)
	os.WriteFile(filepath.Join(root, "Makefile"), []byte("MBT_ROOT := mbt\ninclude $(MBT_ROOT)/mk/mbt.mk\nBUILD_ID := x\nwebroot: $(IMG)\n.PHONY: webroot\n"), 0o644)
	res, err := Convert(root)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Join(res.Notices, "\n"); !strings.Contains(n, "(targets: webroot)") {
		t.Errorf("notes:\n%s", n)
	}
	os.WriteFile(filepath.Join(root, "Makefile"), []byte("# two lines\nMBT_ROOT := mbt\ninclude $(MBT_ROOT)/mk/mbt.mk\n"), 0o644)
	res, _ = Convert(root)
	if strings.Contains(strings.Join(res.Notices, "\n"), "Makefile") {
		t.Errorf("a plain Makefile got a note: %v", res.Notices)
	}
}

// What mbt 3 does by itself is left out: the warning flags, and include/
// where the convention puts it (first, and present on disk).
func TestBuildDefaultsLeftOut(t *testing.T) {
	for _, c := range []struct {
		cflags, want, note string
		noInclude, differs bool
	}{
		{cflags: `["-I", "include", "-Wall", "-Werror"]`, want: "", note: "left out -Wall -Werror"},
		{cflags: `["-I", "include", "-I", "x", "-Wall", "-DY"]`, want: "[build]\ninclude = [\"x\"]\ncflags = [\"-DY\"]\n", note: "left out -Wall"},
		{cflags: `["-I", "x", "-I", "include"]`, want: "[build]\ninclude = [\"x\", \"include\"]\n"},
		// include/ alone: a NOTE too (httprexx: -std=gnu99 -I include)
		{cflags: `["-std=gnu99", "-I", "include"]`, want: "[build]\ncflags = [\"-std=gnu99\"]\n", note: "left out -I include"},
		{cflags: `["-Wno-unused", "-Wall"]`, want: "[build]\ncflags = [\"-Wno-unused\", \"-Wall\"]\n", noInclude: true},
		// include/ on disk that mbt 2 did not use: the default would add it,
		// and the check says so instead of writing a different build
		{cflags: `["-DY"]`, want: "[build]\ncflags = [\"-DY\"]\n", differs: true},
	} {
		root := t.TempDir()
		if !c.noInclude {
			os.Mkdir(filepath.Join(root, "include"), 0o755)
		}
		os.MkdirAll(filepath.Join(root, "src"), 0o755)
		os.WriteFile(filepath.Join(root, "src", "a.c"), []byte("int main(void){return 0;}\n"), 0o644)
		os.WriteFile(filepath.Join(root, "project.toml"), []byte("[project]\nname = \"p\"\nversion = \"1.0.0-dev\"\ntype = \"application\"\n\n[build]\ncflags = "+c.cflags+"\n\n[[module]]\nname = \"P\"\nrent = false\nreus = false\nsources = [\"src/a.c\"]\n"), 0o644)
		res, err := Convert(root)
		if err != nil {
			t.Fatalf("%s: %v", c.cflags, err)
		}
		if diffs, err := Check(root, res.Text); err != nil || (len(diffs) > 0) != c.differs {
			t.Errorf("%s: check %v %v", c.cflags, diffs, err)
		}
		got := ""
		if i := strings.Index(res.Text, "[build]"); i >= 0 {
			got = res.Text[i:]
			got = got[:strings.Index(got, "\n\n")+1]
		}
		if got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.cflags, got, c.want)
		}
		if n := strings.Join(res.Notices, "\n"); c.note != "" && !strings.Contains(n, c.note) {
			t.Errorf("%s: notes %q", c.cflags, n)
		}
	}
}

// From the httpd migration: the first test keeps its own comment under the
// section banner; a [deploy] target equal to the default and SMP defaults
// are left out with a NOTE each.
func TestMigratePolish(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, "test"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "a.c"), []byte("int main(void){return 0;}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "test", "tsta.c"), []byte("int main(void){return 0;}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "project.toml"), []byte(`[project]
name = "p"
version = "1.0.0-dev"
type = "application"

[[module]]
name = "P"
rent = false
reus = false
sources = ["src/a.c"]

# -- Tests ----------------------------------------------
# TSTA checks a.c on its own.
[[test]]
name = "TSTA"
rent = false
reus = false
sources = ["test/tsta.c", "src/a.c"]

# Where make deploy RECEIVEs; without this it would derive a versioned name.
[deploy]
target = "P.DEV.LINKLIB"
`), 0o644)
	res, err := Convert(root)
	if err != nil {
		t.Fatal(err)
	}
	if diffs, err := Check(root, res.Text); err != nil || len(diffs) > 0 {
		t.Fatalf("check %v %v", diffs, err)
	}
	if !strings.Contains(res.Text, "# -- Tests ----------------------------------------------\n# Defaults for every test") {
		t.Errorf("the banner is not above [tests]:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "# TSTA checks a.c on its own.\n[test.TSTA]") {
		t.Errorf("TSTA lost its comment:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "[deploy]") {
		t.Errorf("[deploy] target equal to the default was kept:\n%s", res.Text)
	}
	if strings.Contains(res.Text, "make deploy RECEIVEs") {
		t.Errorf("the comment of the dropped [deploy] stayed:\n%s", res.Text)
	}
	if n := strings.Join(res.Notices, "\n"); !strings.Contains(n, "[deploy] target: left out P.DEV.LINKLIB") || !strings.Contains(n, "[deploy]: dropped with its comment") {
		t.Errorf("notes: %s", n)
	}
}
