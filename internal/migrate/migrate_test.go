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
		"include = [\"include\", \"client\"]\ncflags = [\"-Wall\"]  # warnings on",
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
