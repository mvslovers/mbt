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
	// The DDs of the HMASMP step of SMPAPP in the order each system's
	// procedure lists them (HMASMP LVL 04.48). One of ours with such a name is
	// an override: JCL wants the overrides in this order and ahead of every
	// added DD, or the second DD of the same name is not an override and SMP
	// uses the procedure's. SMPREC has no library DDs. The two differ: MVS/CE
	// also lists the A* and AOS* distribution libraries (ASAMPLIB among
	// them) and UMODLIB/UMODOBJ, TK5 does not; ASAMPLIB is an override ahead
	// of LINKLIB there and an addition after SAMPLIB here.
	smpappProcedures = map[string][]string{
		"mvsce": strings.Fields(`
			SYSUT1 SYSUT2 SYSUT3 SYSUT4 SYSPRINT ASMPRINT CMPPRINT COPPRINT LKDPRINT
			E37PRINT UPDPRINT ZAPPRINT SMPOUT SMPLOG SMPTLIB SYSLIB SMPACDS SMPACRQ
			SMPCDS SMPCRQ SMPMTS SMPPTS SMPSTS SMPSCDS SMPWRK1 SMPWRK2 SMPWRK3
			SMPWRK4 SMPWRK5 ACMDLIB AGENLIB AHELP AIMAGE ALPALIB AMACLIB AMODGEN
			AOS00 AOS03 AOS04 AOS05 AOS06 AOS07 AOS11 AOS12 AOS20 AOS21 AOS24 AOS26
			AOS29 AOS32 AOSA0 AOSA1 AOSB0 AOSB3 AOSBN AOSC2 AOSC5 AOSC6 AOSCA AOSCD
			AOSCE AOSD0 AOSD7 AOSD8 AOSG0 AOSH1 AOSH3 AOST3 AOST4 AOSU0 APARMLIB
			APROCLIB ASAMPLIB ATCAMMAC ATSOMAC AUADS HASPSRC CMDLIB HELP IMAGELIB
			IMAGE LPALIB LINKLIB NUCLEUS MACLIB PARMLIB PROCLIB SAMPLIB SVCLIB
			TCOMMAC TELCMLIB UADS UMODLIB UMODOBJ VTAMLIB`),
		"tk5": strings.Fields(`
			SYSUT1 SYSUT2 SYSUT3 SYSUT4 SYSPRINT ASMPRINT CMPPRINT COPPRINT LKDPRINT
			E37PRINT UPDPRINT ZAPPRINT SMPOUT SMPLOG SMPTLIB SYSLIB SMPACDS SMPACRQ
			SMPCDS SMPCRQ SMPMTS SMPPTS SMPSTS SMPSCDS SMPWRK1 SMPWRK2 SMPWRK3
			SMPWRK4 SMPWRK5 CMDLIB HASPSRC HELP IMAGELIB IMAGE LPALIB LINKLIB
			NUCLEUS MACLIB PARMLIB PROCLIB SAMPLIB SVCLIB TCOMMAC TELCMLIB UADS
			VTAMLIB`),
	}

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
	return dsn, nil
}

// checkDerivedDD: a library without a DD name key gets its last qualifier,
// so that must be a name SMP can use. ddKey names the key that would give it
// another, "" if there is none.
func checkDerivedDD(dsn, what, ddKey string) error {
	hint := ""
	if ddKey != "" {
		hint = " -- or name one with " + ddKey
	}
	dd := ddnameOf(dsn)
	if !ddnameRE.MatchString(dd) {
		return errf("%s: '%s' yields the ddname '%s', which is not valid. SMP addresses the library by the last qualifier, so it must be a usable ddname%s.", what, dsn, dd, hint)
	}
	if reservedDDNames[dd] {
		return errf("%s: '%s' yields the ddname '%s', which the SMPAPP and SMPREC procedures already define themselves. The collision would be silent -- choose a different last qualifier%s.", what, dsn, dd, hint)
	}
	return nil
}

// isProcedureDD: a DD of the SMPAPP/SMPREC procedures of MVS/CE or TK5 (the
// union of what was measured on both), or one SMP or JCL reads by name.
func isProcedureDD(name string) bool {
	if reservedDD(name) {
		return true
	}
	for _, order := range smpappProcedures {
		for _, n := range order {
			if n == name {
				return true
			}
		}
	}
	return false
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

// The record format of a library delivered as SMP elements. Only fixed 80
// is accepted: ++MAC does not take a variable-length member (measured), and
// IEBCOPY refuses to re-block between the staging, distribution and target
// libraries, so all three carry exactly this.
const (
	elemRecfm     = "FB"
	elemLrecl     = 80
	elemBlksize   = 3120
	elemMinDirBlk = 20
)

// Element is one member of a library delivered as SMP elements.
type Element struct {
	// Name is the member, and the element name SMP knows it by.
	Name string
	// DD is the ddname of the TXLIB card pointing at the member.
	DD string
}

// Library is a host directory shipped as its own XMIT. Without Element the
// XMIT is received straight into Target, outside SMP. With Element ("MAC" or
// "SRC") it is received into Stage and SMP installs every member from there
// into Target as an element of its own, DistLib being the library SMP keeps
// the accepted copy in.
type Library struct {
	Dir, Target    string
	Element        string
	DistLib, Stage string
	Elements       []Element
	records        int
	needsLatin1    bool
}

// delivers: the library is installed by SMP, member by member.
func (l Library) delivers() bool { return l.Element != "" }

// received is the dataset the library's XMIT is restored into.
func (l Library) received() string {
	if l.delivers() {
		return l.Stage
	}
	return l.Target
}

// elementSpace sizes a library delivered as elements: twice what the first
// version needs, so the next one fits, and enough directory blocks for the
// members with their ISPF statistics (about six entries to a block).
func (l Library) elementSpace() (primary, secondary, dir int) {
	const bytesPerCyl = 400000
	primary = (2*l.records*elemLrecl + bytesPerCyl - 1) / bytesPerCyl
	if primary < 5 {
		primary = 5
	}
	secondary = primary / 2
	if secondary < 2 {
		secondary = 2
	}
	dir = len(l.Elements)/3 + 10
	if dir < elemMinDirBlk {
		dir = elemMinDirBlk
	}
	return
}

// SMP is the SMP4 side of the descriptor.
type SMP struct {
	FMID, System, LKLib, Target, DistLib string
	// TargetVolume and DlibVolume are the volumes the allocation job puts the
	// target and the distribution libraries on (the defaults of its TGTVOL and
	// DLBVOL symbolics).
	TargetVolume, DlibVolume string
	// Procedure names the SMPAPP layout of the system the package installs
	// on (a key of smpappProcedures); empty if the descriptor does not say.
	Procedure      string
	Prereq, Delete []string
	AcceptFMID     bool
	// Predecessor are the libraries the deleted level installed into, by the
	// DD names SMP recorded for its elements, sorted by DD name.
	Predecessor []OldLibrary
}

// OldLibrary is one library of the level a SYSMOD deletes.
type OldLibrary struct{ DD, DSN string }

// Distribution is the parsed [distribution].
type Distribution struct {
	SMP       SMP
	Libraries []Library
	Readme    string
	Extra     []string
	// Warnings are what Parse accepts but the author should read.
	Warnings []string
	// dds are the DD names given by a *_dd key, by data set; every other
	// data set is addressed by its last qualifier.
	dds map[string]string
}

// dd is the DD name SMP and the jobs know the data set by.
func (d *Distribution) dd(dsn string) string {
	if n, ok := d.dds[dsn]; ok {
		return n
	}
	return ddnameOf(dsn)
}

// applyLibraries are the libraries the APPLY and ACCEPT steps name: the
// SYSMOD's own, then those of the element libraries.
func (d *Distribution) applyLibraries() []string {
	out := []string{d.SMP.Target, d.SMP.LKLib, d.SMP.DistLib}
	for _, l := range d.delivered() {
		out = append(out, l.Target, l.DistLib)
	}
	return out
}

func (d *Distribution) allocated() []string {
	out := []string{d.SMP.Target, d.SMP.DistLib}
	for _, l := range d.Libraries {
		if l.delivers() {
			out = append(out, l.Target, l.DistLib)
		}
	}
	return out
}

func (d *Distribution) received() []string {
	out := []string{d.SMP.LKLib}
	for _, l := range d.Libraries {
		out = append(out, l.received())
	}
	return out
}

// delivered are the libraries SMP installs element by element.
func (d *Distribution) delivered() []Library {
	var out []Library
	for _, l := range d.Libraries {
		if l.delivers() {
			out = append(out, l)
		}
	}
	return out
}

func (d *Distribution) hasSRC() bool {
	for _, l := range d.delivered() {
		if l.Element == "SRC" {
			return true
		}
	}
	return false
}

// The volumes of an MVS/CE system, used when the descriptor names none. TK5 is
// not the default: it ships with its products installed, and whoever installs
// SMP packages there sets TGTVOL=TK5RES and DLBVOL=TK5DLB.
const (
	DefaultTargetVolume = "MVSRES"
	DefaultDlibVolume   = "SMP000"
)

var volserRE = regexp.MustCompile(`^[A-Z0-9@#$]{1,6}$`)

var (
	distKeys = []string{"extra", "library", "readme", "smp"}
	smpKeys  = []string{"accept_fmid", "delete", "distlib", "distlib_dd", "dlib_volume", "fmid", "lklib", "lklib_dd", "predecessor", "prereq", "procedure", "system", "target", "target_dd", "target_volume"}
	libKeys  = []string{"dir", "distlib", "distlib_dd", "element", "stage", "target", "target_dd"}
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
	dds := map[string]string{}
	var warnings []string
	// ddFor settles the DD name of one library: the <key>_dd key if there is
	// one, else the last qualifier of dsn.
	ddFor := func(cfg map[string]any, key, dsn, where string) error {
		what := where + " " + key
		v, ok := cfg[key+"_dd"]
		if !ok {
			if err := checkDerivedDD(dsn, what, key+"_dd"); err != nil {
				return err
			}
			if dd := ddnameOf(dsn); isProcedureDD(dd) {
				warnings = append(warnings, fmt.Sprintf("%s '%s' is addressed by the DD name %s, a DD of the SMP procedures (SMPAPP on MVS/CE and TK5). SMP stores the DD name with every element it installs, so a later run of the standard procedures resolves it to the system library of that name, not to '%s'. Set %s_dd to a name of your own.", what, dsn, dd, dsn, key))
			}
			return nil
		}
		dd := strings.ToUpper(pyStr(v))
		if !ddnameRE.MatchString(dd) {
			return errf("%s: %s_dd '%s' is not a valid ddname (1-8 of A-Z 0-9 @ # $, first character not a digit)", where, key, pyStr(v))
		}
		if isProcedureDD(dd) {
			return errf("%s: %s_dd '%s' is a DD of the SMP procedures (SMPAPP, SMPREC) or one that JCL or SMP reads by name. A library under that name would replace the system library of the same name in the step, and SMP would store the name with every element. Choose a name of your own.", where, key, dd)
		}
		if prev, ok := dds[dsn]; ok && prev != dd {
			return errf("%s: '%s' is given the DD names %s and %s", where, dsn, prev, dd)
		}
		dds[dsn] = dd
		return nil
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
	smp := SMP{FMID: fmid, System: strings.ToUpper(pyStr(sys)), Prereq: prereq, Delete: del, AcceptFMID: true,
		TargetVolume: DefaultTargetVolume, DlibVolume: DefaultDlibVolume}
	for _, f := range []struct {
		key string
		dst *string
	}{{"target_volume", &smp.TargetVolume}, {"dlib_volume", &smp.DlibVolume}} {
		if v, ok := smpCfg[f.key]; ok {
			vol := strings.ToUpper(pyStr(v))
			if !volserRE.MatchString(vol) {
				return nil, errf("[distribution.smp]: %s '%s' is not a volume serial: 1 to 6 characters, A-Z 0-9 @ # $", f.key, pyStr(v))
			}
			*f.dst = vol
		}
	}
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
	for _, f := range []struct{ key, dsn string }{{"lklib", smp.LKLib}, {"target", smp.Target}, {"distlib", smp.DistLib}} {
		if err := ddFor(smpCfg, f.key, f.dsn, "[distribution.smp]"); err != nil {
			return nil, err
		}
	}
	if a, ok := smpCfg["accept_fmid"]; ok {
		smp.AcceptFMID = pyTruthy(a)
	}
	if pv, ok := smpCfg["procedure"]; ok {
		smp.Procedure = pyStr(pv)
		if _, ok := smpappProcedures[smp.Procedure]; !ok {
			return nil, errf("[distribution.smp]: procedure '%s' is not one of %s", smp.Procedure, strings.Join(procedureNames(), ", "))
		}
	}
	if smp.LKLib == smp.Target {
		return nil, errf("[distribution.smp]: lklib and target are both '%s'. The staging library the APPLY reads from must be a different dataset than the library it installs into.", smp.LKLib)
	}
	d := &Distribution{SMP: smp, dds: dds}
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
		lib := Library{Dir: pyStr(dir), Target: t}
		if ev, ok := lc["element"]; ok {
			lib.Element = strings.ToUpper(pyStr(ev))
			if lib.Element != "MAC" && lib.Element != "SRC" {
				return nil, errf("[[distribution.library]] %s: element = '%s' -- only MAC and SRC are supported", lib.Dir, pyStr(ev))
			}
			if lib.DistLib, err = elementDSN(lc, "distlib", "A", lib, expand); err != nil {
				return nil, err
			}
			if lib.Stage, err = elementDSN(lc, "stage", "T", lib, expand); err != nil {
				return nil, err
			}
			where := "[[distribution.library]] " + lib.Dir
			if err := ddFor(lc, "target", lib.Target, where); err != nil {
				return nil, err
			}
			if err := ddFor(lc, "distlib", lib.DistLib, where); err != nil {
				return nil, err
			}
			if err := checkDerivedDD(lib.Stage, where+" stage", ""); err != nil {
				return nil, err
			}
		} else {
			for _, k := range []string{"stage", "target_dd", "distlib_dd"} {
				if _, ok := lc[k]; ok {
					return nil, errf("[[distribution.library]] %s: %s only means something with element = \"MAC\" or \"SRC\"", lib.Dir, k)
				}
			}
			if err := checkDerivedDD(lib.Target, "[[distribution.library]] "+lib.Dir+" target", ""); err != nil {
				return nil, err
			}
		}
		d.Libraries = append(d.Libraries, lib)
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
		dd := d.dd(dsn)
		if prev, ok := seen[dd]; ok && prev != dsn {
			return nil, errf("'%s' and '%s' both yield the ddname '%s'. SMP addresses libraries by ddname, so one of them would be unreachable. Give them different last qualifiers, or name the ddname with target_dd, distlib_dd or lklib_dd.", dsn, prev, dd)
		}
		seen[dd] = dsn
	}
	if err := distinctDSNs(d); err != nil {
		return nil, err
	}
	if err := parsePredecessor(d, smpCfg["predecessor"]); err != nil {
		return nil, err
	}
	if _, err := applyDDs(d); err != nil {
		return nil, err
	}
	d.Warnings = warnings
	return d, nil
}

// elementDSN is the distribution or staging library of an element library:
// the key as given, else the target with the last qualifier prefixed.
func elementDSN(lc map[string]any, key, prefix string, l Library, expand func(any, string) (string, error)) (string, error) {
	what := "[[distribution.library]] " + key
	if v, ok := lc[key]; ok {
		return expand(v, what)
	}
	i := strings.LastIndex(l.Target, ".")
	if i < 0 || len(prefix+l.Target[i+1:]) > 8 {
		return "", errf("%s of '%s': no default for the target '%s' (%s + its last qualifier would pass 8 characters, or it has no qualifier to put it beside) -- name it with %s", what, l.Dir, l.Target, prefix, key)
	}
	return checkDSN(l.Target[:i+1]+prefix+l.Target[i+1:], what)
}

// distinctDSNs: a dataset named twice is one dataset used for two things, and
// nothing says so.
func distinctDSNs(d *Distribution) error {
	seen := map[string]string{}
	for _, l := range d.Libraries {
		if !l.delivers() {
			continue
		}
		for _, x := range []struct{ dsn, role string }{{l.Target, "target"}, {l.DistLib, "distlib"}, {l.Stage, "stage"}} {
			if prev, ok := seen[x.dsn]; ok {
				return errf("[[distribution.library]] %s: %s '%s' is already the %s", l.Dir, x.role, x.dsn, prev)
			}
			seen[x.dsn] = x.role + " of " + l.Dir
		}
	}
	for _, x := range []struct{ role, dsn string }{{"lklib", d.SMP.LKLib}, {"target", d.SMP.Target}, {"distlib", d.SMP.DistLib}} {
		if prev, ok := seen[x.dsn]; ok {
			return errf("[distribution.smp] %s '%s' is already the %s", x.role, x.dsn, prev)
		}
	}
	return nil
}

// readByNameDD: a DD that is not a library -- SMP's own files, the output
// files of the procedures' steps and the catalogs and libraries JCL reads by
// name. The library DDs of the procedures (LINKLIB, SAMPLIB, ...) are not
// among them: they are what a predecessor installed through.
func readByNameDD(name string) bool {
	switch name {
	case "JOBLIB", "STEPLIB", "JOBCAT", "STEPCAT":
		return true
	}
	return strings.HasPrefix(name, "SMP") || strings.HasPrefix(name, "SYS") || strings.HasSuffix(name, "PRINT")
}

// parsePredecessor reads [smp.predecessor]: DD name = data set name, one per
// library of the level `delete` removes. SMP records the DD name with every
// element, so the DELETE finds the element only if the job has a card of that
// name for the old data set.
func parsePredecessor(d *Distribution, v any) error {
	if v == nil {
		return nil
	}
	const where = "[distribution.smp] predecessor"
	t, ok := v.(map[string]any)
	if !ok {
		return errf("%s must be a table of DD name = \"data set name\"", where)
	}
	if len(t) == 0 {
		return nil
	}
	if len(d.SMP.Delete) == 0 {
		return errf("%s needs a non-empty delete: without a deleted level nothing reads the old libraries, and the cards would only name data sets the job has no use for", where)
	}
	own := map[string]string{}
	for _, dsn := range d.applyLibraries() {
		own[d.dd(dsn)] = dsn
	}
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]string{}
	for _, k := range keys {
		dd := strings.ToUpper(k)
		if !ddnameRE.MatchString(dd) {
			return errf("%s: '%s' is not a valid ddname (1-8 of A-Z 0-9 @ # $, first character not a digit)", where, k)
		}
		if readByNameDD(dd) {
			return errf("%s: '%s' is a DD that SMP or JCL reads by name, not a library. The old library is addressed by the DD name SMP recorded for it (LINKLIB, for example)", where, dd)
		}
		str, ok := t[k].(string)
		if !ok {
			return errf("%s: %s must be a data set name in quotes", where, dd)
		}
		if strings.Contains(strings.ToUpper(str), "@VRM@") {
			return errf("%s: %s = '%s' -- write the real name of the old data set; @VRM@ stands for this level's version, not the predecessor's", where, dd, str)
		}
		dsn, err := checkDSN(strings.ToUpper(str), where+" "+dd)
		if err != nil {
			return err
		}
		if prev, dup := seen[dd]; dup && prev != dsn {
			return errf("%s: the ddname '%s' is given '%s' and '%s'", where, dd, prev, dsn)
		}
		seen[dd] = dsn
		if cur, clash := own[dd]; clash && cur != dsn {
			return errf("%s: '%s' and '%s' both yield the ddname '%s'. One ddname cannot name two data sets in one step, and the DELETE would land in the new library. Give the new level a ddname of its own (target_dd, distlib_dd, lklib_dd).", where, dsn, cur, dd)
		}
		d.SMP.Predecessor = append(d.SMP.Predecessor, OldLibrary{dd, dsn})
	}
	return nil
}
