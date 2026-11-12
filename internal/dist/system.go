package dist

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A system module (mbt#380) goes into a library of the SMP procedures --
// SYS1.LPALIB, say -- instead of the product's target. The JCLIN link-edits
// it there with IEWL from its distribution library (AOST4), and it travels as
// its assembler source: the install job assembles it with the target
// system's IFOX00 and macros and appends the object deck to the SYSMOD,
// right behind its ++MOD, before the RECEIVE. SMP sees a plain object deck.
//
// The DD names are the procedures' on purpose: a product library never uses
// one (descriptor.go), a system module always does.

// SystemModule is what the SYSMOD needs of a module of [module.NAME.smp].
type SystemModule struct {
	// Syslib is the DD name of the system library the module is linked
	// into, Distlib that of the distribution library SMP keeps it in; both
	// are SYS1.<DD name>.
	Syslib, Distlib string
	// Parm is the PARM of the IEWL step.
	Parm string
	// Source is the module's assembler source, relative to the root.
	Source string
}

// system is one system module of a Distribution, its source read.
type system struct {
	Name string
	SystemModule
	text string
}

var (
	// The system libraries a module may be linked into: load module
	// libraries that the SMPAPP procedures of MVS/CE and TK5 both define,
	// as SYS1.<DD name>. Measured: LPALIB.
	systemSyslibs = map[string]bool{"LPALIB": true, "LINKLIB": true, "SVCLIB": true}
	// systemNote: when a module in that library takes effect.
	systemNote = map[string]string{
		"LPALIB":  "It takes effect at the next IPL with CLPA.",
		"SVCLIB":  "It takes effect at the next IPL with CLPA.",
		"LINKLIB": "It is found from the link list after the next IPL.",
	}
)

// systemDistlib: a distribution library of load modules that the MVS/CE
// procedures list (AOS*, ALPALIB, ACMDLIB); the macro and text libraries
// (AMACLIB, ASAMPLIB, ...) are not.
func systemDistlib(dd string) bool {
	if dd != "ALPALIB" && dd != "ACMDLIB" && !strings.HasPrefix(dd, "AOS") {
		return false
	}
	for _, n := range smpappProcedures["mvsce"] {
		if n == dd {
			return true
		}
	}
	return false
}

// sysDSN is the data set behind a DD name of the procedures.
func sysDSN(dd string) string { return "SYS1." + dd }

// setSystems checks the system modules and reads their sources. One module
// at most travels as source: its ++MOD has to be the last statement of the
// SYSMOD, so the assembler can append the object deck to it.
func setSystems(d *Distribution, root string, modules []Module) error {
	own := map[string]string{}
	for _, dsn := range append(d.allocated(), d.received()...) {
		own[d.dd(dsn)] = dsn
	}
	for _, o := range d.SMP.Predecessor {
		own[o.DD] = o.DSN
	}
	load := 0
	for _, m := range modules {
		if m.System == nil {
			load++
			continue
		}
		s := m.System
		where := "[module." + m.Name + ".smp]"
		if !systemSyslibs[s.Syslib] {
			return errf("%s: syslib = '%s' -- a module can be linked into %s (the load module libraries both SMPAPP procedures define)", where, s.Syslib, strings.Join(sortedSet(systemSyslibs), ", "))
		}
		if !systemDistlib(s.Distlib) {
			return errf("%s: distlib = '%s' is not a distribution library of load modules of the SMP procedures (AOST4, AOS11, ALPALIB, ACMDLIB, ...)", where, s.Distlib)
		}
		for _, dd := range []string{s.Syslib, s.Distlib} {
			if dsn, clash := own[dd]; clash && dsn != sysDSN(dd) {
				return errf("%s: the DD name %s is also '%s' of this package; one DD name cannot name two data sets in one step", where, dd, dsn)
			}
		}
		if len(m.Aliases) > 0 {
			return errf("%s: a module linked into a system library cannot have aliases yet", where)
		}
		if len(d.Systems) > 0 {
			return errf("%s: %s is already shipped as source. One module at most can be: the install job appends the object deck to the last ++MOD of the SYSMOD, and a second one has not been measured", where, d.Systems[0].Name)
		}
		text, err := readSource(root, s.Source)
		if err != nil {
			return err
		}
		d.Systems = append(d.Systems, system{Name: m.Name, SystemModule: *s, text: text})
	}
	if len(d.Systems) > 0 && load == 0 {
		return errf("[distribution]: every shipped module goes into a system library; at least one has to go into the product's target %s (the COPYLOAD step of the JCLIN needs one)", d.SMP.Target)
	}
	return nil
}

func sortedSet(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// readSource reads an assembler source the install job carries inline: 80
// columns, plain ASCII, no tabs (a JCL stream does not expand them), and no
// line that would end the DD DATA stream it travels in.
func readSource(root, file string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return "", errf("%s: %v", file, err)
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	_, latin1, problems := checkMember(file, text)
	var lines []string
	for n, line := range splitLines(text) {
		if strings.Contains(line, "\t") {
			problems = append(problems, fmt.Sprintf("%s:%d: a tab -- expand it, the install job carries the source as card images", file, n+1))
		}
		if strings.HasPrefix(line, instreamDelimiter) {
			problems = append(problems, fmt.Sprintf("%s:%d: starts with '%s', which ends the DD DATA stream carrying the source", file, n+1, instreamDelimiter))
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	if latin1 {
		problems = append(problems, file+": characters outside plain ASCII; the source travels inside the install job")
	}
	if len(problems) > 0 {
		return "", errf("%s cannot travel in the install job:\n    %s", file, capProblems(problems, 20))
	}
	if len(lines) == 0 {
		return "", errf("%s is empty", file)
	}
	return strings.Join(lines, "\n"), nil
}

// systemDDs are the DD names of the system libraries the SMP steps need a
// card for: those a procedure lacks (AOST4 is not in TK5's SMPAPP, and the
// ACCEPT writes the module there). With procedure set only that one counts,
// else every known one. Ordered as MVS/CE's procedure lists them.
func systemDDs(d *Distribution) []string {
	procs := procedureNames()
	if d.SMP.Procedure != "" {
		procs = []string{d.SMP.Procedure}
	}
	want := map[string]bool{}
	for _, s := range d.Systems {
		for _, dd := range []string{s.Syslib, s.Distlib} {
			for _, p := range procs {
				if !inList(smpappProcedures[p], dd) {
					want[dd] = true
				}
			}
		}
	}
	var out []string
	for _, n := range smpappProcedures["mvsce"] {
		if want[n] {
			out = append(out, n)
			delete(want, n)
		}
	}
	return append(out, sortedSet(want)...)
}

func inList(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// linkStep is the JCLIN step of a system module: IEWL from the distribution
// library into the system library, the module replacing its namesake.
func linkStep(s system) []string {
	return []string{
		fmt.Sprintf("//%s EXEC PGM=IEWL,PARM='%s'", ljust(s.Name, 8), s.Parm),
		fmt.Sprintf("//%s DD  DISP=SHR,DSN=%s", ljust(s.Distlib, 8), sysDSN(s.Distlib)),
		fmt.Sprintf("//SYSLMOD  DD  DISP=SHR,DSN=%s", sysDSN(s.Syslib)),
		"//SYSLIN   DD  *",
		fmt.Sprintf("  INCLUDE %s(%s)", s.Distlib, s.Name),
		fmt.Sprintf("  NAME %s(R)", s.Name),
		"/*",
	}
}

// The steps that write the SYSMOD to &&MCS and assemble the source module
// behind it.
const (
	generStep = "GENER1"
	assemStep = "ASSEM"
)

// receiveSysmod is the RECV step's EXEC card and its SMPPTFIN, with what
// comes before them. Without a system module the SYSMOD is inline; with
// one, IEBGENER writes it to &&MCS and IFOX00 appends the object deck. The
// source is replaced by src when src is not nil (the card check of the job
// runs without it: the source is checked to 80 columns, the job to 71).
func receiveSysmod(d *Distribution, mcs, lastRecv string, src *string) string {
	mcs = strings.TrimRight(mcs, "\n")
	if len(d.Systems) == 0 {
		return fmt.Sprintf("//RECV    EXEC SMPREC,COND=(0,NE,%s)\n//SMPPTFIN DD  DATA,DLM=%s\n%s\n%s", lastRecv, instreamDelimiter, mcs, instreamDelimiter)
	}
	s := d.Systems[0]
	text := s.text
	if src != nil {
		text = *src
	}
	gate := "0,NE," + lastRecv
	lines := []string{
		fmt.Sprintf("//%s  EXEC PGM=IEBGENER,COND=(%s)", generStep, gate),
		"//SYSPRINT DD  SYSOUT=*",
		"//SYSIN    DD  DUMMY",
		"//SYSUT1   DD  DATA,DLM=" + instreamDelimiter,
		mcs,
		instreamDelimiter,
		"//SYSUT2   DD  DSN=&&MCS,DISP=(NEW,PASS),UNIT=SYSDA,",
		"//             SPACE=(CYL,(1,1)),DCB=(RECFM=FB,LRECL=80,BLKSIZE=3200)",
		"//*",
		"//* ---- assemble " + s.Name + " " + strings.Repeat("-", 52-len(s.Name)),
		"//* " + s.Name + " ships as its source and is assembled here, with this",
		"//* system's own assembler and macros. SYSGO appends the object deck",
		"//* to the SYSMOD, right behind its ++MOD, the last statement of it.",
		"//* The assembly has to end with return code 0, or nothing is received.",
		"//*",
		fmt.Sprintf("//%s   EXEC PGM=IFOX00,PARM='LOAD,NODECK',", assemStep),
		fmt.Sprintf("//             COND=((%s),(0,NE,%s))", gate, generStep),
		"//SYSPRINT DD  SYSOUT=*",
		"//SYSUT1   DD  UNIT=SYSDA,SPACE=(1700,(600,100))",
		"//SYSUT2   DD  UNIT=SYSDA,SPACE=(1700,(300,50))",
		"//SYSUT3   DD  UNIT=SYSDA,SPACE=(1700,(300,50))",
		"//SYSLIB   DD  DISP=SHR,DSN=SYS1.MACLIB,DCB=BLKSIZE=32720",
		"//         DD  DISP=SHR,DSN=SYS1.AMODGEN",
		"//SYSGO    DD  DSN=&&MCS,DISP=(MOD,PASS)",
		"//SYSIN    DD  DATA,DLM=" + instreamDelimiter,
	}
	if text != "" {
		lines = append(lines, text)
	}
	lines = append(lines, instreamDelimiter,
		fmt.Sprintf("//RECV    EXEC SMPREC,COND=((%s),(0,NE,%s),(0,NE,%s))", gate, generStep, assemStep),
		"//SMPPTFIN DD  DSN=&&MCS,DISP=(OLD,PASS)")
	return strings.Join(lines, "\n")
}

// systemSummary: the lines of the install job's header for the two steps.
func systemSummary(d *Distribution) []string {
	if len(d.Systems) == 0 {
		return nil
	}
	return []string{
		"//*   " + ljust(generStep, 9) + " write the SYSMOD to a temporary data set",
		"//*   " + ljust(assemStep, 9) + " assemble " + d.Systems[0].Name + " behind its ++MOD (IFOX00)",
	}
}

// systemScope: the install job's note on the system modules.
func systemScope(d *Distribution) string {
	if len(d.Systems) == 0 {
		return ""
	}
	var parts []string
	for _, s := range d.Systems {
		parts = append(parts, fmt.Sprintf("SMP links %s into %s, not into %s, and keeps it in %s. %s The APPLY ends with return code 4: the link edit reports IEW0342, because %s is not in %s yet (HMA2391 LINK SUCCESSFUL ... RETURN CODE=08).",
			s.Name, sysDSN(s.Syslib), d.SMP.Target, sysDSN(s.Distlib), systemNote[s.Syslib], s.Name, s.Syslib))
	}
	return "\n//*\n" + commentBlock(strings.Join(parts, " "))
}

// systemAcceptNote: the accept job's paragraph on the return codes of a
// system module; it follows a "//*" card.
func systemAcceptNote(d *Distribution) string {
	if len(d.Systems) == 0 {
		return ""
	}
	s := d.Systems[0]
	return "\n//* RETURN CODE 4 WITH " + s.Name + "\n" + commentBlock(fmt.Sprintf(
		"%s is not in %s when the APPLY links it there (new, or taken out by the DELETE of the level before): the link edit ends with return code 8 on IEW0342, SMP reports HMA2391 LINK SUCCESSFUL with RETURN CODE=08, and the APPLY ends with 4. The ACCEPT ends with 4 for the same reason, HMA2471 BLDL FAILED IN LIBRARY %s, followed by HMA2390 LINK SUCCESSFUL. Both are the normal end, and the CLEANUP still runs.",
		s.Name, sysDSN(s.Syslib), s.Distlib))
}
