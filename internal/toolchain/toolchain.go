// Package toolchain finds the cc370 toolchain and the libc370 sysroot the way
// mbt v2's mk/mbt.mk does, and answers the questions the link depends on.
package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Toolchain is the resolved cc370 installation.
type Toolchain struct {
	CC, AS, LD, AR string // commands as given (on PATH, or absolute)
	Sysroot        string // <bindir>/../cc370, symlinks followed
	LibcDir        string // where libc.a is
	// CRTInLibc: libc.a defines @@CRT0 (libc370 >= 2.3.0, libc370#159).
	CRTInLibc bool
	// EntryAutocall: ld370 pulls an unreferenced entry out of an archive by
	// name (cc370 >= 1.2.0, cc370#107).
	EntryAutocall bool
	// CC370RT: <sysroot>/lib/libcc370rt.a exists (cc370 >= 1.1.0).
	CC370RT  bool
	Version  string // cc370's, from the driver banner
	Warnings []string
}

// Find resolves the toolchain.  Tool names default to cc370/as370/ld370/ar370
// and can be overridden (MBT_CC and friends, as make's CC= did).
func Find() (*Toolchain, error) {
	t := &Toolchain{
		CC: env("MBT_CC", "cc370"), AS: env("MBT_AS", "as370"),
		LD: env("MBT_LD", "ld370"), AR: env("MBT_AR", "ar370"),
	}
	bin, err := exec.LookPath(t.CC)
	if err != nil {
		return nil, fmt.Errorf("%s not found on PATH", t.CC)
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	abs, _ := filepath.Abs(real)
	t.Sysroot = filepath.Clean(filepath.Join(filepath.Dir(abs), "..", "cc370"))

	// libc370 in cc370's own tree, or (cc370 >= 1.2.0) the second sysroot.
	for _, d := range []string{filepath.Join(t.Sysroot, "lib"), filepath.Join(t.Sysroot, "libc370", "lib")} {
		if exists(filepath.Join(d, "libc.a")) || exists(filepath.Join(d, "crt0.o")) {
			t.LibcDir = d
			break
		}
	}
	if t.LibcDir == "" {
		home, _ := os.UserHomeDir()
		t.Warnings = append(t.Warnings, fmt.Sprintf(
			"no libc370 beside %s (looked in %s/lib and %s/libc370/lib); falling back to %s/.local/cc370",
			bin, t.Sysroot, t.Sysroot, home))
		t.Sysroot = filepath.Join(home, ".local", "cc370")
		t.LibcDir = filepath.Join(t.Sysroot, "lib")
	}

	if out, err := exec.Command(t.AR, "t", filepath.Join(t.LibcDir, "libc.a")).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == "@@CRT0" {
				t.CRTInLibc = true
				break
			}
		}
	}
	if out, err := exec.Command(t.CC, "--version").Output(); err == nil {
		first := strings.SplitN(string(out), "\n", 2)[0]
		if f := strings.Fields(first); len(f) >= 2 {
			t.Version = f[1]
			t.EntryAutocall = atLeast(t.Version, 1, 2)
		}
	}
	if !t.CRTInLibc && exists(filepath.Join(t.LibcDir, "crt0.o")) {
		t.Warnings = append(t.Warnings, fmt.Sprintf(
			"%s/libc.a has no @@CRT0 (libc370 < 2.3.0); linking crt0.o/crt1.o from the sysroot", t.LibcDir))
	}
	t.CC370RT = exists(filepath.Join(t.Sysroot, "lib", "libcc370rt.a"))
	return t, nil
}

// LibDirs is the -L list for ld370: cc370's lib, and libc370's when it is
// elsewhere.
func (t *Toolchain) LibDirs() []string {
	dirs := []string{"-L" + filepath.Join(t.Sysroot, "lib")}
	if t.LibcDir != filepath.Join(t.Sysroot, "lib") {
		dirs = append(dirs, "-L"+t.LibcDir)
	}
	return dirs
}

// LinkInputs are the sysroot files a link reads; a newer one relinks (#103).
func (t *Toolchain) LinkInputs() []string {
	c := []string{filepath.Join(t.LibcDir, "libc.a"), filepath.Join(t.Sysroot, "lib", "libcc370rt.a"), filepath.Join(t.LibcDir, "crtm.o")}
	if !t.CRTInLibc {
		c = append(c, filepath.Join(t.LibcDir, "crt0.o"), filepath.Join(t.LibcDir, "crt1.o"))
	}
	var out []string
	for _, f := range c {
		if exists(f) {
			out = append(out, f)
		}
	}
	return out
}

func atLeast(v string, major, minor int) bool {
	p := strings.SplitN(strings.SplitN(v, "-", 2)[0], ".", 3)
	if len(p) < 2 {
		return false
	}
	ma, err1 := strconv.Atoi(p[0])
	mi, err2 := strconv.Atoi(p[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return ma > major || (ma == major && mi >= minor)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
