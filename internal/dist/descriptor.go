// Package dist builds the SMP4 installation package from a project's
// [distribution] table: the SYSMOD (inline in the install job), the
// allocation job, one XMIT per shipped source library, and a zip and tar.gz
// holding them with the README and the load library XMIT.
//
// It is mbt v2's scripts/mbtdist.py and scripts/mbt/distribution.py, ported
// function for function, with v2's messages: the generated jobs and SYSMOD
// must be identical, and every rule in them was measured on MVS (the comments
// in the v2 source carry the job numbers).
package dist

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Error is a malformed [distribution] or a SYSMOD that would be invalid.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

const (
	// An MCS statement is read to column 72; JCL, and everything generated
	// here, to 71.
	maxMCSCol  = 72
	maxCardCol = 71
	// More than 20 is IEF642I on the JOB card and the job never runs.
	maxProgrammerName = 20
	// The single step of the SMP procedures; overrides carry it.
	smpProcStep = "HMASMP"
	// Terminator of the //SMPPTFIN DD DATA stream carrying the SYSMOD.
	instreamDelimiter = "@@"
	// Placeholder prefix an operator replaces in the install job.
	xmitEditPrefix = "CHANGE.ME"
	maxDSN         = 44
)

var (
	// SMPAPP/SMPREC define SMPTLIB themselves.
	reservedDDNames = map[string]bool{"SMPTLIB": true}
	// DDs the SMPAPP procedure carries: an override of one must come first.
	smpappProcDDs = map[string]bool{"CMDLIB": true, "HELP": true, "MACLIB": true, "PARMLIB": true,
		"UMODLIB": true, "LINKLIB": true, "LPALIB": true, "PROCLIB": true, "SAMPLIB": true,
		"ASAMPLIB": true, "UMODOBJ": true}

	fmidRE      = regexp.MustCompile(`^[A-Z][A-Z0-9]{6}$`)
	ddnameRE    = regexp.MustCompile(`^[A-Z@#$][A-Z0-9@#$]{0,7}$`)
	qualifierRE = regexp.MustCompile(`^[A-Z@#$][A-Z0-9@#$-]{0,7}$`)
	memberRE    = regexp.MustCompile(`^[A-Z@#$][A-Z0-9@#$]{0,7}$`)
)

// ddnameOf: SMP addresses a library by the last qualifier of its name.
func ddnameOf(dsn string) string {
	p := strings.Split(dsn, ".")
	return p[len(p)-1]
}

// memberName derives a member from a host file name, as xmit370 does: the
// base name, one extension removed, upper-cased.
func memberName(path string) (string, error) {
	base := filepath.Base(path)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	name := strings.ToUpper(base)
	if !memberRE.MatchString(name) {
		return "", errf("%s: '%s' is not a valid MVS member name (1-8 of A-Z 0-9 @ # $, first character not a digit). Rename the file.", path, name)
	}
	return name, nil
}

func checkDSN(dsn, what string) (string, error) {
	if dsn == "" {
		return "", errf("%s: dataset name is empty", what)
	}
	if len(dsn) > maxDSN {
		return "", errf("%s: '%s' is %d characters, the MVS limit is %d", what, dsn, len(dsn), maxDSN)
	}
	for _, q := range strings.Split(dsn, ".") {
		if !qualifierRE.MatchString(q) {
			return "", errf("%s: '%s' has an invalid qualifier '%s' (1-8 of A-Z 0-9 @ # $, first character not a digit)", what, dsn, q)
		}
	}
	dd := ddnameOf(dsn)
	if !ddnameRE.MatchString(dd) {
		return "", errf("%s: '%s' yields the ddname '%s', which is not valid. SMP addresses the library by the last qualifier, so it must be a usable ddname.", what, dsn, dd)
	}
	if reservedDDNames[dd] {
		return "", errf("%s: '%s' yields the ddname '%s', which the SMPAPP and SMPREC procedures already define themselves. The collision would be silent -- choose a different last qualifier.", what, dsn, dd)
	}
	return dsn, nil
}

// checkCardText rejects what will not survive an 80-column EBCDIC card deck:
// a line past column 71, a tab, a character outside printable ASCII.
func checkCardText(text, what string) error {
	for n, line := range splitLines(text) {
		if strings.Contains(line, "\t") {
			return errf("%s: line %d contains a tab. Expand it -- tabs do not survive the transfer to an 80-column card deck.", what, n+1)
		}
		for _, ch := range line {
			if ch < 0x20 || ch > 0x7E {
				return errf("%s: line %d contains the non-ASCII or control character %s (U+%04X). It has no EBCDIC mapping; use plain ASCII.", what, n+1, pyCharRepr(ch), ch)
			}
		}
		if l := len(strings.TrimRight(line, " \t\r\n\f\v")); l > maxCardCol {
			return errf("%s: line %d reaches column %d, past %d:\n    %s\nAn MCS card is only read to column %d.", what, n+1, l, maxCardCol, line, maxCardCol)
		}
	}
	return nil
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func pyCharRepr(r rune) string { return fmt.Sprintf("'%c'", r) }

// Library is a host directory shipped as its own XMIT, outside SMP.
type Library struct {
	Dir, Target string
}

func (l Library) targetDD() string { return ddnameOf(l.Target) }

// SMP is the SMP4 side of the descriptor.
type SMP struct {
	FMID, System, LKLib, Target, DistLib string
	Prereq, Delete                       []string
	AcceptFMID                           bool
}

// Distribution is the parsed [distribution].
type Distribution struct {
	SMP       SMP
	Libraries []Library
	Readme    string
	Extra     []string
}

func (d *Distribution) allocated() []string { return []string{d.SMP.Target, d.SMP.DistLib} }

func (d *Distribution) received() []string {
	out := []string{d.SMP.LKLib}
	for _, l := range d.Libraries {
		out = append(out, l.Target)
	}
	return out
}

var (
	distKeys = []string{"extra", "library", "readme", "smp"}
	smpKeys  = []string{"accept_fmid", "delete", "distlib", "fmid", "lklib", "prereq", "system", "target"}
	libKeys  = []string{"dir", "distlib", "target"}
)

func rejectUnknown(section map[string]any, known []string, where string) error {
	k := map[string]bool{}
	for _, s := range known {
		k[s] = true
	}
	var unknown []string
	for key := range section {
		if !k[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return errf("unknown %s key(s): %s (known: %s)", where, strings.Join(unknown, ", "), strings.Join(known, ", "))
}

func require(section map[string]any, key, where string) (any, error) {
	v, ok := section[key]
	if !ok {
		return nil, errf("%s: '%s' is required", where, key)
	}
	return v, nil
}

func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}

func pyTruthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case string:
		return x != ""
	case nil:
		return false
	}
	return true
}

// list reads a TOML array; an array of tables decodes as []map[string]any.
func list(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, len(x))
		for i, t := range x {
			out[i] = t
		}
		return out
	}
	return nil
}

// Parse builds a Distribution from a parsed project.toml, or nil without a
// [distribution].  vrm replaces @VRM@ in every dataset name.
func Parse(raw map[string]any, vrm string) (*Distribution, error) {
	dist, _ := raw["distribution"].(map[string]any)
	if len(dist) == 0 {
		return nil, nil
	}
	if err := rejectUnknown(dist, distKeys, "[distribution]"); err != nil {
		return nil, err
	}
	expand := func(v any, what string) (string, error) {
		return checkDSN(strings.ToUpper(strings.ReplaceAll(pyStr(v), "@VRM@", vrm)), what)
	}
	smpAny, err := require(dist, "smp", "[distribution]")
	if err != nil {
		return nil, err
	}
	smpCfg, _ := smpAny.(map[string]any)
	if err := rejectUnknown(smpCfg, smpKeys, "[distribution.smp]"); err != nil {
		return nil, err
	}
	fv, err := require(smpCfg, "fmid", "[distribution.smp]")
	if err != nil {
		return nil, err
	}
	fmid := strings.ToUpper(pyStr(fv))
	if !fmidRE.MatchString(fmid) {
		return nil, errf("[distribution.smp]: fmid '%s' must be exactly 7 characters, alphanumeric, starting with a letter (e.g. TUFS110)", fmid)
	}
	var prereq, del []string
	for _, p := range list(smpCfg["prereq"]) {
		s := strings.ToUpper(pyStr(p))
		if !fmidRE.MatchString(s) {
			return nil, errf("[distribution.smp]: prereq '%s' is not a valid 7-character SYSMOD id", s)
		}
		prereq = append(prereq, s)
	}
	for _, p := range list(smpCfg["delete"]) {
		s := strings.ToUpper(pyStr(p))
		if !fmidRE.MatchString(s) {
			return nil, errf("[distribution.smp]: delete '%s' is not a valid 7-character SYSMOD id", s)
		}
		del = append(del, s)
	}
	for _, d := range del {
		if d == fmid {
			return nil, errf("[distribution.smp]: delete names this SYSMOD's own fmid '%s'. A function cannot delete itself -- a new level needs a new id, and that id is what deletes the one before it.", fmid)
		}
	}
	sys, err := require(smpCfg, "system", "[distribution.smp]")
	if err != nil {
		return nil, err
	}
	smp := SMP{FMID: fmid, System: strings.ToUpper(pyStr(sys)), Prereq: prereq, Delete: del, AcceptFMID: true}
	for _, f := range []struct {
		key string
		dst *string
	}{{"lklib", &smp.LKLib}, {"target", &smp.Target}, {"distlib", &smp.DistLib}} {
		v, err := require(smpCfg, f.key, "[distribution.smp]")
		if err != nil {
			return nil, err
		}
		if *f.dst, err = expand(v, "[distribution.smp] "+f.key); err != nil {
			return nil, err
		}
	}
	if a, ok := smpCfg["accept_fmid"]; ok {
		smp.AcceptFMID = pyTruthy(a)
	}
	if smp.LKLib == smp.Target {
		return nil, errf("[distribution.smp]: lklib and target are both '%s'. The staging library the APPLY reads from must be a different dataset than the library it installs into.", smp.LKLib)
	}
	d := &Distribution{SMP: smp}
	for _, l := range list(dist["library"]) {
		lc, _ := l.(map[string]any)
		if err := rejectUnknown(lc, libKeys, "[[distribution.library]]"); err != nil {
			return nil, err
		}
		dir, err := require(lc, "dir", "[[distribution.library]]")
		if err != nil {
			return nil, err
		}
		tv, err := require(lc, "target", "[[distribution.library]]")
		if err != nil {
			return nil, err
		}
		t, err := expand(tv, "[[distribution.library]] target")
		if err != nil {
			return nil, err
		}
		d.Libraries = append(d.Libraries, Library{Dir: pyStr(dir), Target: t})
	}
	if r, ok := dist["readme"]; ok && r != nil {
		d.Readme = pyStr(r)
	}
	for _, e := range list(dist["extra"]) {
		d.Extra = append(d.Extra, pyStr(e))
	}
	// Two of our datasets must never share a ddname: one would be unreachable.
	seen := map[string]string{}
	for _, dsn := range append(d.allocated(), d.received()...) {
		dd := ddnameOf(dsn)
		if prev, ok := seen[dd]; ok && prev != dsn {
			return nil, errf("'%s' and '%s' both yield the ddname '%s'. SMP addresses libraries by ddname, so one of them would be unreachable. Give them different last qualifiers.", dsn, prev, dd)
		}
		seen[dd] = dsn
	}
	return d, nil
}
