package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mvslovers/mbt/internal/version"
)

// The sysroot carries no version marker; the version is the build stamp
// libc370 bakes into libc.a ("LIBC370 2.4.0 (ecfb39f)"), EBCDIC-encoded
// because it is a target string.  Tolerant about case and a leading 'v': the
// spelling has moved before.
var stampRE = regexp.MustCompile(`(?i)libc370\s+v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)*)`)

// StampVersion finds the libc370 version in the bytes of a libc.a: EBCDIC
// first, then Latin-1 in case a host-encoded stamp ever appears.
func StampVersion(data []byte) string {
	var e, l strings.Builder
	for _, b := range data {
		e.WriteRune(cp037[b])
		l.WriteRune(rune(b))
	}
	for _, s := range []string{e.String(), l.String()} {
		if m := stampRE.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

// InstalledLibc370 is the libc370 version in the sysroot, or "".
func (t *Toolchain) InstalledLibc370() string {
	data, err := os.ReadFile(filepath.Join(t.LibcDir, "libc.a"))
	if err != nil {
		return ""
	}
	return StampVersion(data)
}

var declaredVersionRE = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)*$`)

// CheckStatus is the outcome of comparing the installed libc370 with the
// project's [toolchain] libc370.
type CheckStatus int

const (
	CheckSkip    CheckStatus = iota // nothing to compare
	CheckOK                         // the declaration is satisfied
	CheckUnknown                    // declared, but the stamp is unreadable
	CheckDrift                      // exact only: newer than the pin
	CheckFail                       // older than the declaration
)

// CheckLibc370 compares as mbt v2 did (scripts/mbttoolchain.py): older than
// declared fails; newer is fine for a build and a warning for a release
// (exact), because release CI rebuilds with the pin (mbt#71); a stamp that
// cannot be read never fails a build.
func (t *Toolchain) CheckLibc370(want string, exact bool) (CheckStatus, string) {
	have := t.InstalledLibc370()
	if want == "" || !declaredVersionRE.MatchString(want) {
		why := "no version declared"
		if want != "" {
			why = fmt.Sprintf("declared as '%s'", want)
		}
		if have == "" {
			return CheckSkip, fmt.Sprintf("libc370: no version readable from the sysroot (%s)", why)
		}
		return CheckSkip, fmt.Sprintf("libc370 %s in %s (%s, nothing to check)", have, t.Sysroot, why)
	}
	if have == "" {
		return CheckUnknown, fmt.Sprintf("cannot read the libc370 version from %s/libc.a; skipping the [toolchain] check (want %s)", t.LibcDir, want)
	}
	hv, err1 := version.Parse(have)
	wv, err2 := version.Parse(want)
	if err1 != nil || err2 != nil {
		return CheckUnknown, fmt.Sprintf("cannot compare libc370 %s with the declared %s; skipping the [toolchain] check", have, want)
	}
	switch c := version.Compare(hv, wv); {
	case c < 0:
		return CheckFail, fmt.Sprintf("libc370 in the sysroot is %s, but project.toml requires >= %s; reinstall %s from libc370 v%s or newer", have, want, t.Sysroot, want)
	case c == 0:
		op := ">="
		if exact {
			op = "=="
		}
		return CheckOK, fmt.Sprintf("libc370 %s in %s (%s %s)", have, t.Sysroot, op, want)
	case exact:
		return CheckDrift, fmt.Sprintf("tagging against libc370 %s, but this build used %s; release CI will build with %s -- untested here", want, have, want)
	}
	return CheckOK, fmt.Sprintf("libc370 %s in %s (>= %s)", have, t.Sysroot, want)
}
