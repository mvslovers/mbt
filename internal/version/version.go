// Package version parses the ecosystem's versions and constraints, with mbt
// v2's rules (scripts/mbt/version.py): MAJOR.MINOR.PATCH with an optional
// "-dev" or "-rcN", ordered dev < rcN < release, and constraints joined by
// commas from ">=", "<" and "=".
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var semverRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-(dev|rc\d+))?$`)

// Version is a parsed version.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "", "dev", "rc1", ...
}

// Parse reads "1.2.3", "1.2.3-dev" or "1.2.3-rc2".
func Parse(s string) (Version, error) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("invalid semver: %q (expected MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-dev or MAJOR.MINOR.PATCH-rcN)", s)
	}
	v := Version{Pre: m[4]}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	return v, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// IsPre: a prerelease (-dev or -rcN).
func (v Version) IsPre() bool { return v.Pre != "" }

// VRM is the MVS dataset qualifier: 1.0.0 -> V1R0M0, 1.0.0-dev -> V1R0M0D,
// 3.3.1-rc1 -> V3R3M1R1.
func (v Version) VRM() string {
	s := fmt.Sprintf("V%dR%dM%d", v.Major, v.Minor, v.Patch)
	switch {
	case v.Pre == "dev":
		s += "D"
	case strings.HasPrefix(v.Pre, "rc"):
		s += "R" + v.Pre[2:]
	}
	return s
}

func (v Version) rank() [5]int {
	switch {
	case v.Pre == "":
		return [5]int{v.Major, v.Minor, v.Patch, 0, 0}
	case v.Pre == "dev":
		return [5]int{v.Major, v.Minor, v.Patch, -2, 0}
	}
	n, _ := strconv.Atoi(v.Pre[2:])
	return [5]int{v.Major, v.Minor, v.Patch, -1, n}
}

// Compare returns -1, 0 or 1.
func Compare(a, b Version) int {
	ra, rb := a.rank(), b.rank()
	for i := range ra {
		switch {
		case ra[i] < rb[i]:
			return -1
		case ra[i] > rb[i]:
			return 1
		}
	}
	return 0
}

// Satisfies checks a version against a constraint like ">=1.0.0,<2.0.0".
func Satisfies(v Version, constraint string) (bool, error) {
	for _, part := range strings.Split(constraint, ",") {
		part = strings.TrimSpace(part)
		var op, rest string
		switch {
		case strings.HasPrefix(part, ">="):
			op, rest = ">=", part[2:]
		case strings.HasPrefix(part, "<"):
			op, rest = "<", part[1:]
		case strings.HasPrefix(part, "="):
			op, rest = "=", part[1:]
		default:
			return false, fmt.Errorf("unknown constraint operator in: %q", part)
		}
		req, err := Parse(rest)
		if err != nil {
			return false, err
		}
		c := Compare(v, req)
		if (op == ">=" && c < 0) || (op == "<" && c >= 0) || (op == "=" && (c != 0 || v.Pre != req.Pre)) {
			return false, nil
		}
	}
	return true, nil
}

// AllowsPre: the constraint names a prerelease itself (">=1.0.0-dev"), so a
// prerelease may be picked for it.  "You only get prereleases if you ask."
func AllowsPre(constraint string) bool {
	for _, part := range strings.Split(constraint, ",") {
		s := strings.TrimLeft(strings.TrimSpace(part), "><=~^ ")
		if v, err := Parse(s); err == nil && v.IsPre() {
			return true
		}
	}
	return false
}

// Allowed: the resolver may pick v for the constraint -- it satisfies it,
// and it is not a prerelease under a constraint that names none.
func Allowed(v Version, constraint string) (bool, error) {
	if v.IsPre() && !AllowsPre(constraint) {
		return false, nil
	}
	return Satisfies(v, constraint)
}
