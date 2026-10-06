package dist

import (
	"fmt"
	"regexp"
	"strings"
)

func ljust(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// copyStep is the IEBCOPY step of the JCLIN: a copy, not a link edit, so SMP
// copies the finished module instead of re-binding it.
func copyStep(step, distlibDD, distlibDSN, targetDD, targetDSN string, members []string) []string {
	lines := []string{
		fmt.Sprintf("//%s EXEC PGM=IEBCOPY", ljust(step, 8)),
		fmt.Sprintf("//%s DD  DISP=SHR,DSN=%s", ljust(distlibDD, 8), distlibDSN),
		fmt.Sprintf("//%s DD  DISP=SHR,DSN=%s", ljust(targetDD, 8), targetDSN),
		"//SYSIN    DD  *",
		fmt.Sprintf("  COPY INDD=%s,OUTDD=%s", distlibDD, targetDD),
	}
	lines = append(lines, selectMemberCards(members)...)
	return append(lines, "/*")
}

// selectMemberCards: SELECT MEMBER=(...), wrapped below column 71.
func selectMemberCards(members []string) []string {
	head := "  SELECT MEMBER=("
	var lines []string
	cur := head
	for i, m := range members {
		piece := m + ","
		if i == len(members)-1 {
			piece = m + ")"
		}
		if len(cur)+len(piece) > maxCardCol-1 {
			lines = append(lines, cur)
			cur = strings.Repeat(" ", len(head)) + piece
		} else {
			cur += piece
		}
	}
	return append(lines, cur)
}

// modStatement: ++MOD for one load module, TALIAS on a card of its own.
func modStatement(mod, lklibDD, distlibDD string, aliases []string) ([]string, error) {
	head := fmt.Sprintf("++MOD(%s) LKLIB(%s) DISTLIB(%s)", mod, lklibDD, distlibDD)
	if len(aliases) == 0 {
		return []string{head + " ."}, nil
	}
	talias := fmt.Sprintf("      TALIAS(%s) .", strings.Join(aliases, ","))
	if len(talias) > maxCardCol {
		return nil, errf("module %s: TALIAS(%s) does not fit on one card (%d columns, limit %d); splitting it over several cards has not been measured (mbt#112)", mod, strings.Join(aliases, ","), len(talias), maxCardCol)
	}
	return []string{head, talias}, nil
}

// AssembleMCS builds the SYSMOD as card images.
func AssembleMCS(d *Distribution, modules []string, product, version string, aliases map[string][]string) (string, error) {
	smp := d.SMP
	out := []string{fmt.Sprintf("++FUNCTION(%s) .", smp.FMID)}
	ver := fmt.Sprintf("++VER(%s)", smp.System)
	if len(smp.Prereq) > 0 {
		ver += fmt.Sprintf(" REQ(%s)", strings.Join(smp.Prereq, ","))
	}
	if len(smp.Delete) > 0 {
		ver += fmt.Sprintf(" DELETE(%s)", strings.Join(smp.Delete, ","))
	}
	out = append(out, ver,
		ljust(fmt.Sprintf("   /* %s %s", product, version), 65)+"*/",
		ljust("   /* Load modules are copied from the LKLIB, not re-bound.", 65)+"*/ .",
		"++JCLIN .",
		fmt.Sprintf("//%s JOB 1,'%s JCLIN',MSGLEVEL=1,CLASS=A", smp.FMID, upperCut(product, 20)))
	out = append(out, copyStep("COPYLOAD", ddnameOf(smp.DistLib), smp.DistLib, ddnameOf(smp.Target), smp.Target, modules)...)
	for _, m := range modules {
		lines, err := modStatement(m, ddnameOf(smp.LKLib), ddnameOf(smp.DistLib), aliases[m])
		if err != nil {
			return "", err
		}
		out = append(out, lines...)
	}
	text := strings.Join(out, "\n") + "\n"
	if err := checkCardText(text, "generated SYSMOD "+smp.FMID); err != nil {
		return "", err
	}
	for n, line := range out {
		if strings.HasPrefix(line, instreamDelimiter) {
			return "", errf("generated SYSMOD %s: line %d starts with '%s', which terminates the //SMPPTFIN DD DATA stream carrying it -- the rest of the SYSMOD would be read as JCL:\n    %s", smp.FMID, n+1, instreamDelimiter, line)
		}
	}
	return text, nil
}

// upperCut: Python's s.upper()[:n] on ASCII.
func upperCut(s string, n int) string {
	s = strings.ToUpper(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// renderApplyDDs: procedure overrides first, additions after -- the only
// order in which an override is an override.
func renderApplyDDs(d *Distribution) string {
	var over, add []string
	for _, dsn := range []string{d.SMP.Target, d.SMP.LKLib, d.SMP.DistLib} {
		dd := ddnameOf(dsn)
		line := fmt.Sprintf("//%s.%s DD  DISP=SHR,DSN=%s", smpProcStep, ljust(dd, 8), dsn)
		if smpappProcDDs[dd] {
			over = append(over, line)
		} else {
			add = append(add, line)
		}
	}
	return strings.Join(append(over, add...), "\n")
}

// renderAllocDDs: NEW,CATLG,DELETE in cylinders, no DELETE step anywhere.
func renderAllocDDs(d *Distribution) string {
	var lines []string
	for _, dsn := range d.allocated() {
		lines = append(lines,
			fmt.Sprintf("//%s DD  DSN=%s,DISP=(NEW,CATLG,DELETE),", ljust(ddnameOf(dsn), 8), dsn),
			"//            UNIT=SYSDA,SPACE=(CYL,(5,2,20)),",
			"//            DCB=(DSORG=PO,RECFM=U,BLKSIZE=15040)")
	}
	return strings.Join(lines, "\n")
}

type recv struct{ step, dsn, placeholder, file string }

func receivePlan(d *Distribution, files map[string]string) []recv {
	var plan []recv
	for i, dsn := range d.received() {
		f, ok := files[dsn]
		if !ok {
			f = "?"
		}
		plan = append(plan, recv{fmt.Sprintf("RECV%d", i+1), dsn, xmitEditPrefix + "." + ddnameOf(dsn), f})
	}
	return plan
}

func renderReceiveSteps(plan []recv) string {
	lines := []string{
		"//* The RECEIVE targets are deleted first: TSO RECEIVE refuses to",
		"//* merge into an existing dataset, so without this the job would",
		"//* run exactly once. SET MAXCC=0 keeps a first run, where none of",
		"//* them exists yet, from failing on the DELETEs.",
		"//*",
		"//DELOLD  EXEC PGM=IDCAMS",
		"//SYSPRINT DD  SYSOUT=*",
		"//SYSIN    DD  *",
	}
	for _, r := range plan {
		lines = append(lines, fmt.Sprintf("  DELETE %s NONVSAM SCRATCH PURGE", r.dsn))
	}
	lines = append(lines, "  SET MAXCC=0", "/*")
	for _, r := range plan {
		lines = append(lines,
			"//*",
			"//* "+r.file,
			"//*   -> "+r.dsn,
			fmt.Sprintf("//%s EXEC PGM=IKJEFT01,DYNAMNBR=20", ljust(r.step, 7)),
			"//SYSTSPRT DD  SYSOUT=*",
			"//SYSTSIN  DD  *",
			fmt.Sprintf("  RECEIVE INDSN('%s') -", r.placeholder),
			fmt.Sprintf("    DATASET('%s')", r.dsn),
			"/*")
	}
	return strings.Join(lines, "\n")
}

// acceptCond: relaxed to RC 4 only when a DELETE is in play (an upgrade's
// APPLY ends RC 04 on HMA2461, measured).
func acceptCond(d *Distribution) string {
	if len(d.SMP.Delete) > 0 {
		return "(4,LT,APPLY.HMASMP)"
	}
	return "(0,NE,APPLY.HMASMP)"
}

func renderCleanupStep(d *Distribution, after string) (string, error) {
	for _, a := range d.allocated() {
		if a == d.SMP.LKLib {
			return "", errf("refusing to generate a cleanup step for %s: it is one of the libraries SMP installs into", d.SMP.LKLib)
		}
	}
	return strings.Join([]string{
		"//*",
		"//* ---- remove the staging library -----------------------------------",
		"//* Only reached when everything above worked. Comment the step out",
		"//* if you would rather keep the shipped modules on the system.",
		"//*",
		fmt.Sprintf("//CLEANUP EXEC PGM=IDCAMS,COND=(0,NE,%s)", after),
		"//SYSPRINT DD  SYSOUT=*",
		"//SYSIN    DD  *",
		fmt.Sprintf("  DELETE %s NONVSAM SCRATCH PURGE", d.SMP.LKLib),
		"/*",
		"//",
	}, "\n"), nil
}

var notQualifier = regexp.MustCompile(`[^A-Z0-9@#$]`)

// jobName: the product as a qualifier cut so the 3-character suffix survives
// the 8-character limit (#130).
func jobName(product, suffix string) string {
	q := notQualifier.ReplaceAllString(strings.ToUpper(product), "")
	if len(q) > 8 {
		q = q[:8]
	}
	if n := 8 - len(suffix); len(q) > n {
		q = q[:n]
	}
	return q + strings.ToUpper(suffix)
}

// jobcard for a job the operator submits; MSGCLASS=H so a failed install
// stays findable.
func jobcard(jobname, programmer string) (string, error) {
	jn := strings.ToUpper(jobname)
	if len(jn) > 8 {
		jn = jn[:8]
	}
	if len(programmer) > maxProgrammerName {
		return "", errf("job card programmer name '%s' is %d characters; more than %d is IEF642I EXCESSIVE PARAMETER LENGTH and the job never runs", programmer, len(programmer), maxProgrammerName)
	}
	return fmt.Sprintf("//%s JOB (SYS),'%s',\n//             CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1),\n//             REGION=4096K", ljust(jn, 8), programmer), nil
}
