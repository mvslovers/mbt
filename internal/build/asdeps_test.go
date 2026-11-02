package build

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// fakeVerboseAS is an as370 that does what the real one does with -v: it
// lists its macro search path and every COPY member it reads, and it punches
// the members into the deck, so a changed member changes the deck.  Each run
// appends a line to as.log in the working directory.
const fakeVerboseAS = `#!/bin/sh
v=0; dirs=""; n=0
while [ $# -gt 0 ]; do
  case "$1" in -v) v=1;; -I) dirs="$dirs $2"; shift;; -o) obj=$2; shift;; *) src=$1;; esac
  shift
done
echo run >> as.log
[ $v = 1 ] && echo "as370: assembling $src" >&2
for d in $dirs; do n=$((n+1)); [ $v = 1 ] && echo "as370: macro path $n: $d" >&2; done
echo "card of $src" > "$obj"
for m in $(sed -n 's/^ *COPY *//p' "$src"); do
  for d in $dirs; do
    for f in "$d/$m" "$d/$m.asm"; do
      if [ -f "$f" ]; then
        [ $v = 1 ] && echo "as370: macro $m from $f" >&2
        cat "$f" >> "$obj"
        break 2
      fi
    done
  done
done
[ $v = 1 ] && echo " Assembler Done   0 Statements Flagged /   0 was Highest Severity" >&2
exit 0
`

type asProject struct {
	t    *testing.T
	root string
	b    *Builder
	out  *bytes.Buffer
}

func newASProject(t *testing.T, asflags ...string) *asProject {
	t.Helper()
	root := t.TempDir()
	as := filepath.Join(root, "as370")
	if err := os.WriteFile(as, []byte(fakeVerboseAS), 0o755); err != nil {
		t.Fatal(err)
	}
	ar := filepath.Join(root, "ar370")
	os.WriteFile(ar, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	var out bytes.Buffer
	p := &project.Project{Root: root, Name: "d", ASFlags: asflags,
		Lib: &project.Lib{Name: "d", Sources: []string{"src/main.asm"}}}
	b := New(p, &toolchain.Toolchain{AS: as, AR: ar},
		Options{BuildDir: "build", Lib: true, Jobs: 1, Out: &out})
	return &asProject{t, root, b, &out}
}

// write puts a file in place and dates it; later is relative to now.
func (a *asProject) write(rel, text string, later time.Duration) {
	a.t.Helper()
	p := filepath.Join(a.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		a.t.Fatal(err)
	}
	_, statErr := os.Stat(p)
	isNew := statErr != nil
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		a.t.Fatal(err)
	}
	at := time.Now().Add(later)
	if err := os.Chtimes(p, at, at); err != nil {
		a.t.Fatal(err)
	}
	// the directory's own date moves when a member is new, not when one changes
	if isNew {
		if err := os.Chtimes(filepath.Dir(p), at, at); err != nil {
			a.t.Fatal(err)
		}
	}
}

// build runs the build and returns how many times as370 ran in total.
func (a *asProject) build() int {
	a.t.Helper()
	if err := a.b.Run(); err != nil {
		a.t.Fatalf("build: %v\n%s", err, a.out)
	}
	data, _ := os.ReadFile(filepath.Join(a.root, "as.log"))
	return strings.Count(string(data), "run")
}

func (a *asProject) deck() string {
	data, err := os.ReadFile(filepath.Join(a.root, "build", "main.o"))
	if err != nil {
		a.t.Fatal(err)
	}
	return string(data)
}

// mbt#231: only a COPY member changes; the build used to stay green and keep
// the old deck.
func TestChangedCopyMemberRebuilds(t *testing.T) {
	a := newASProject(t, "-I", "maclib")
	a.write("src/main.asm", "         COPY  CPY\n", -time.Hour)
	a.write("maclib/CPY", "version one\n", -time.Hour)
	if n := a.build(); n != 1 {
		t.Fatalf("first build ran as370 %d times", n)
	}
	if !strings.Contains(a.deck(), "version one") {
		t.Fatalf("deck: %q", a.deck())
	}
	if n := a.build(); n != 1 {
		t.Fatalf("nothing changed, and as370 ran again (%d runs)", n)
	}
	a.write("maclib/CPY", "version two\n", time.Minute)
	if n := a.build(); n != 2 {
		t.Fatalf("a changed COPY member did not rebuild (%d runs)", n)
	}
	if !strings.Contains(a.deck(), "version two") {
		t.Errorf("deck is the old one: %q", a.deck())
	}
}

// A COPY member in a directory that holds the sources too (-I asm, as
// brexx370 has it): the member is read from the same directory.
func TestCopyMemberNextToTheSource(t *testing.T) {
	a := newASProject(t, "-I", "asm")
	a.b.P.Lib.Sources = []string{"asm/main.asm"}
	a.write("asm/main.asm", "         COPY  CPY\n", -time.Hour)
	a.write("asm/CPY.asm", "one\n", -time.Hour)
	a.build()
	a.write("asm/CPY.asm", "two\n", time.Minute)
	if n := a.build(); n != 2 {
		t.Fatalf("changed member next to the source did not rebuild (%d runs)", n)
	}
}

// A new member in an earlier -I directory hides one that was read from a
// later directory; the changed search result has to rebuild.
func TestNewMemberShadowingAnotherRebuilds(t *testing.T) {
	a := newASProject(t, "-I", "mine", "-I", "theirs")
	a.write("src/main.asm", "         COPY  CPY\n", -time.Hour)
	a.write("theirs/CPY", "theirs\n", -time.Hour)
	a.write("mine/OTHER", "unrelated\n", -time.Hour)
	if n := a.build(); n != 1 {
		t.Fatalf("first build ran as370 %d times", n)
	}
	a.write("mine/CPY", "mine\n", time.Minute)
	if n := a.build(); n != 2 {
		t.Fatalf("a member that shadows another did not rebuild (%d runs)", n)
	}
	if !strings.Contains(a.deck(), "mine") || strings.Contains(a.deck(), "theirs") {
		t.Errorf("deck: %q", a.deck())
	}
}

// A member that was not read and is not in a search directory leaves the
// deck alone.
func TestUnrelatedFileDoesNotRebuild(t *testing.T) {
	a := newASProject(t, "-I", "maclib")
	a.write("src/main.asm", "         COPY  CPY\n", -time.Hour)
	a.write("maclib/CPY", "x\n", -time.Hour)
	a.write("maclib/UNUSED", "y\n", -time.Hour)
	a.build()
	a.write("maclib/UNUSED", "changed\n", time.Minute)
	if n := a.build(); n != 1 {
		t.Errorf("a member nothing read rebuilt the deck (%d runs)", n)
	}
	a.write("elsewhere/NEW", "z\n", time.Minute)
	if n := a.build(); n != 1 {
		t.Errorf("a file outside every search directory rebuilt the deck (%d runs)", n)
	}
}

// A deck from before mbt kept track (no .d) is built once more, so the next
// change of a member is seen.
func TestDeckWithoutDependencyFileRebuildsOnce(t *testing.T) {
	a := newASProject(t, "-I", "maclib")
	a.write("src/main.asm", "         COPY  CPY\n", -time.Hour)
	a.write("maclib/CPY", "x\n", -time.Hour)
	a.build()
	if err := os.Remove(filepath.Join(a.root, "build", "main.d")); err != nil {
		t.Fatalf("an assembler step leaves a dependency file: %v", err)
	}
	if n := a.build(); n != 2 {
		t.Fatalf("no .d, and the deck was kept (%d runs)", n)
	}
	if n := a.build(); n != 2 {
		t.Errorf("rebuilt again after the .d was written (%d runs)", n)
	}
}

// The -v lines are mbt's, not the user's: they do not reach the log.
func TestVerboseLinesAreNotShown(t *testing.T) {
	a := newASProject(t, "-I", "maclib")
	a.write("src/main.asm", "         COPY  CPY\n", -time.Hour)
	a.write("maclib/CPY", "x\n", -time.Hour)
	a.build()
	for _, bad := range []string{"macro path", "macro CPY from", "assembling", "Assembler Done"} {
		if strings.Contains(a.out.String(), bad) {
			t.Errorf("%q in the log:\n%s", bad, a.out)
		}
	}
}

func TestParseAsVerbose(t *testing.T) {
	diag := "as370: assembling src/m.asm\n" +
		"as370: macro path 1: maclib\n" +
		"as370: macro path 2: /opt/cc 370/bin/../macros\n" +
		"as370: macro path 3: /opt/cc/libc370/macros (absent)\n" +
		"as370: macro CPY from maclib/CPY.asm\n" +
		"as370: macro PATH from /opt/cc 370/m/PATH.macro\n" +
		"         BOGUS\n" +
		" ERROR: Undefined operation code in line 3 - BOGUS\n" +
		" Assembler Done   1 Statement Flagged /   8 was Highest Severity\n" +
		" Assembler Done   0 Statements Flagged /   0 was Highest Severity\n"
	files, dirs, rest := parseAsVerbose([]byte(diag))
	if want := []string{"maclib/CPY.asm", "/opt/cc 370/m/PATH.macro"}; !reflect.DeepEqual(files, want) {
		t.Errorf("members %q, want %q", files, want)
	}
	if want := []string{"maclib", "/opt/cc 370/bin/../macros"}; !reflect.DeepEqual(dirs, want) {
		t.Errorf("directories %q, want %q", dirs, want)
	}
	if want := "         BOGUS\n ERROR: Undefined operation code in line 3 - BOGUS\n Assembler Done   1 Statement Flagged /   8 was Highest Severity\n"; string(rest) != want {
		t.Errorf("rest %q", rest)
	}
}

func TestAsDepfileRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.d")
	deps := []string{"src/a#b.asm", "my maclib/C$D", "maclib"}
	if err := writeAsDepfile(p, "build/a#b.o", deps); err != nil {
		t.Fatal(err)
	}
	if got := readDepfile(p); !reflect.DeepEqual(got, deps) {
		data, _ := os.ReadFile(p)
		t.Errorf("got %q\nfrom:\n%s", got, data)
	}
}

// The same with the real assembler, when there is one.
func TestRealAs370TracksMacros(t *testing.T) {
	as, err := exec.LookPath("as370")
	if err != nil {
		t.Skip("no as370")
	}
	root := t.TempDir()
	ar := filepath.Join(root, "ar370")
	os.WriteFile(ar, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	a := &asProject{t: t, root: root, out: &bytes.Buffer{}}
	a.write("src/main.asm", "T        CSECT\n         MYMAC\n         END\n", -time.Hour)
	macro := func(v string) string {
		return "         MACRO\n         MYMAC\n         DC    F'" + v + "'\n         MEND\n"
	}
	a.write("maclib/MYMAC", macro("1"), -time.Hour)
	p := &project.Project{Root: root, Name: "d", ASFlags: []string{"-I", "maclib"},
		Lib: &project.Lib{Name: "d", Sources: []string{"src/main.asm"}}}
	a.b = New(p, &toolchain.Toolchain{AS: as, AR: ar}, Options{BuildDir: "build", Lib: true, Jobs: 1, Out: a.out})
	if err := a.b.Run(); err != nil {
		t.Fatal(err)
	}
	first := a.deck()
	a.write("maclib/MYMAC", macro("2"), time.Minute)
	if err := a.b.Run(); err != nil {
		t.Fatal(err)
	}
	if a.deck() == first {
		t.Error("a changed macro left the deck as it was")
	}
}
