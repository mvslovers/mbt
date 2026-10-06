package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func ebcdic(s string) []byte {
	rev := map[rune]byte{}
	for i, r := range cp037 {
		rev[r] = byte(i)
	}
	var out []byte
	for _, r := range s {
		out = append(out, rev[r])
	}
	return out
}

func TestStampVersion(t *testing.T) {
	data := append([]byte("garbage\x00\x01"), ebcdic("LIBC370 2.4.0 (ecfb39f)")...)
	if got := StampVersion(data); got != "2.4.0" {
		t.Errorf("EBCDIC stamp: %q", got)
	}
	if got := StampVersion([]byte("x libc370 v1.0.2-dev (5c0deeb)")); got != "1.0.2-dev" {
		t.Errorf("Latin-1 stamp: %q", got)
	}
	if got := StampVersion([]byte("nothing here")); got != "" {
		t.Errorf("no stamp: %q", got)
	}
}

func TestCheckLibc370(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "libc.a"), ebcdic("LIBC370 2.3.1 (cfa5afd)"), 0o644)
	tc := &Toolchain{Sysroot: dir, LibcDir: dir}
	for _, c := range []struct {
		want  string
		exact bool
		st    CheckStatus
	}{
		{"", false, CheckSkip},
		{"main", false, CheckSkip},
		{"2.3.0", false, CheckOK},
		{"2.3.1", true, CheckOK},
		{"2.3.0", true, CheckDrift},
		{"2.4.0", false, CheckFail},
	} {
		if st, msg := tc.CheckLibc370(c.want, c.exact); st != c.st {
			t.Errorf("want=%q exact=%v: status %v (%s)", c.want, c.exact, st, msg)
		}
	}
}

// The installed sysroot, when there is one: the stamp must be readable.
func TestInstalledSysroot(t *testing.T) {
	if _, err := exec.LookPath("cc370"); err != nil {
		t.Skip("no cc370 on PATH")
	}
	tc, err := Find()
	if err != nil {
		t.Skip(err)
	}
	if v := tc.InstalledLibc370(); v == "" {
		t.Errorf("no libc370 version readable from %s", tc.LibcDir)
	} else {
		t.Logf("libc370 %s in %s", v, tc.LibcDir)
	}
}
