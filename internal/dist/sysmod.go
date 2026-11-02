package dist

import (
	"fmt"
	"regexp"
	"sort"
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
	)
	out = append(out, elementStatements(d)...)
	out = append(out,
		"++JCLIN .",
		fmt.Sprintf("//%s JOB 1,'%s JCLIN',MSGLEVEL=1,CLASS=A", smp.FMID, upperCut(product, 20)))
	out = append(out, copyStep("COPYLOAD", d.dd(smp.DistLib), smp.DistLib, d.dd(smp.Target), smp.Target, modules)...)
	for _, m := range modules {
		lines, err := modStatement(m, d.dd(smp.LKLib), d.dd(smp.DistLib), aliases[m])
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

// procedureNames are the keys of smpappProcedures, sorted.
func procedureNames() []string {
	var out []string
	for n := range smpappProcedures {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// arrangeApplyDDs: the DDs the procedure has first, in the procedure's order,
// then the additions -- the only order in which an override is an override.
// The libraries of the elements follow the SYSMOD's own, then one TXLIB card
// per element: APPLY and ACCEPT read an element from its TXLIB again
// (HMA2832 DDCARD MISSING otherwise).
func arrangeApplyDDs(d *Distribution, order []string) []string {
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	type dd struct {
		pos  int
		line string
	}
	var over []dd
	var add []string
	type card struct{ name, dsn string }
	var cards []card
	for _, dsn := range d.applyLibraries() {
		cards = append(cards, card{d.dd(dsn), dsn})
	}
	// the deleted level's libraries: a card of the old name and data set, or
	// the DELETE resolves the name in the procedure (LINKLIB is SYS1.LINKLIB)
	for _, o := range d.SMP.Predecessor {
		cards = append(cards, card{o.DD, o.DSN})
	}
	have := map[card]bool{}
	for _, c := range cards {
		if have[c] {
			continue
		}
		have[c] = true
		name, dsn := c.name, c.dsn
		line := fmt.Sprintf("//%s.%s DD  DISP=SHR,DSN=%s", smpProcStep, ljust(name, 8), dsn)
		if p, ok := pos[name]; ok {
			over = append(over, dd{p, line})
		} else {
			add = append(add, line)
		}
	}
	sort.SliceStable(over, func(i, j int) bool { return over[i].pos < over[j].pos })
	add = append(add, txCards(d, smpProcStep+".")...)
	lines := make([]string, 0, len(over)+len(add))
	for _, o := range over {
		lines = append(lines, o.line)
	}
	return append(lines, add...)
}

// applyDDs: the block for the procedure the descriptor names. Without one it
// is arranged for every known procedure and used if they agree; if they do
// not (a DD one procedure has and the other lacks, such as ASAMPLIB), no block
// is right for both systems and guessing is the silent failure.
func applyDDs(d *Distribution) ([]string, error) {
	if d.SMP.Procedure != "" {
		return arrangeApplyDDs(d, smpappProcedures[d.SMP.Procedure]), nil
	}
	var first []string
	for i, name := range procedureNames() {
		lines := arrangeApplyDDs(d, smpappProcedures[name])
		if i > 0 && strings.Join(lines, "\n") != strings.Join(first, "\n") {
			return nil, errf("[distribution.smp]: the DD overrides of the SMPAPP step depend on the system: the %s procedures list different DDs, and the right order of %s is not the same on both. Set `procedure` (%s) to the system the package installs on.",
				"MVS/CE and TK5", strings.Join(ddNames(d), ", "), strings.Join(procedureNames(), ", "))
		}
		first = lines
	}
	return first, nil
}

// ddNames are the ddnames of the libraries the APPLY step names, sorted.
func ddNames(d *Distribution) []string {
	var names []string
	for _, dsn := range d.applyLibraries() {
		names = append(names, d.dd(dsn))
	}
	for _, o := range d.SMP.Predecessor {
		names = append(names, o.DD)
	}
	sort.Strings(names)
	return names
}

// renderApplyDDs: the block of applyDDs. The descriptor parser has already
// refused one that has none.
func renderApplyDDs(d *Distribution) string {
	lines, err := applyDDs(d)
	if err != nil {
		panic(err)
	}
	return strings.Join(lines, "\n")
}

// allocDCB: the DCB and space of one allocated library. SMP's own libraries
// hold load modules; the element libraries are fixed 80, sized for their
// content, and the same for the target and the distribution copy.
func allocDCB(d *Distribution, dsn string) (space, dcb string) {
	for _, l := range d.delivered() {
		if dsn == l.Target || dsn == l.DistLib {
			p, s, dir := l.elementSpace()
			return fmt.Sprintf("SPACE=(CYL,(%d,%d,%d))", p, s, dir),
				fmt.Sprintf("DCB=(DSORG=PO,RECFM=%s,LRECL=%d,BLKSIZE=%d)", elemRecfm, elemLrecl, elemBlksize)
		}
	}
	return "SPACE=(CYL,(5,2,20))", "DCB=(DSORG=PO,RECFM=U,BLKSIZE=15040)"
}

// volSymbol: the symbolic of the allocation job's PROC that names the volume
// of an allocated library, DLBVOL for a distribution library and TGTVOL for a
// target library.
func volSymbol(d *Distribution, dsn string) string {
	if dsn == d.SMP.DistLib {
		return "&DLBVOL"
	}
	for _, l := range d.delivered() {
		if dsn == l.DistLib {
			return "&DLBVOL"
		}
	}
	return "&TGTVOL"
}

// renderAllocDDs: NEW,CATLG,DELETE in cylinders, no DELETE step anywhere. Every
// library is on a volume of its own symbolic, never wherever SYSDA picks.
func renderAllocDDs(d *Distribution) string {
	var lines []string
	for _, dsn := range d.allocated() {
		space, dcb := allocDCB(d, dsn)
		lines = append(lines,
			fmt.Sprintf("//%s DD  DSN=%s,DISP=(NEW,CATLG,DELETE),", ljust(d.dd(dsn), 8), dsn),
			"//            UNIT=SYSDA,VOL=SER="+volSymbol(d, dsn)+","+space+",",
			"//            "+dcb)
	}
	return strings.Join(lines, "\n")
}

// renderAllocJob: the allocation job, its PROC defaulting TGTVOL and DLBVOL to
// the volumes of the descriptor.
func renderAllocJob(d *Distribution, jobcard, name, ver, instJob string) (string, error) {
	return render("smpalloc.jcl.tpl", map[string]string{
		"JOBCARD": jobcard, "PRODUCT": name, "VERSION": ver, "FMID": d.SMP.FMID,
		"INSTALL_JOB": instJob, "ALLOC_DDS": renderAllocDDs(d),
		"TGTVOL": d.SMP.TargetVolume, "DLBVOL": d.SMP.DlibVolume,
	})
}

type recv struct{ step, dsn, placeholder, file string }

func receivePlan(d *Distribution, files map[string]string) []recv {
	var plan []recv
	for i, dsn := range d.received() {
		f, ok := files[dsn]
		if !ok {
			f = "?"
		}
		plan = append(plan, recv{fmt.Sprintf("RECV%d", i+1), dsn, xmitEditPrefix + "." + d.dd(dsn), f})
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
			"    /* VOLUME('"+xmitEditPrefix+"') */ -",
			fmt.Sprintf("    DATASET('%s')", r.dsn),
			"/*")
	}
	return strings.Join(lines, "\n")
}

// renderAcceptStep: the ACCEPT of the accept job. With a prerequisite
// (prereq) it carries BYPASS(REQ): APPLY checks a requisite against the CDS,
// ACCEPT against the ACDS, and a prerequisite that is applied but can never
// be accepted (a usermod whose own requisite is not accepted) would make the
// ACCEPT end RC 12 for good. It covers this one ACCEPT S(<FMID>) only; APPLY
// CHECK and APPLY, in the install job, keep checking.
func renderAcceptStep(d *Distribution) string {
	bypass := ""
	if len(d.SMP.Prereq) > 0 {
		bypass = " BYPASS(REQ)"
	}
	return "//*\n" +
		"//* ---- ACCEPT -------------------------------------------------------\n" +
		"//* Accepting the FMID once fills the distribution library, so a\n" +
		"//* later RESTORE has a previous level to go back to. Without it\n" +
		"//* a RESTORE would DELETE the module instead of reverting it.\n" +
		"//* Service (PTFs) is deliberately never accepted.\n" +
		"//*\n" +
		"//ACCEPT  EXEC SMPAPP\n" +
		renderApplyDDs(d) + "\n" +
		"//SMPCNTL  DD  *\n" +
		fmt.Sprintf(" ACCEPT S(%s)%s DIS(WRITE) .\n", d.SMP.FMID, bypass) +
		"/*"
}

// bypassNote: the part of the accept job's header that says why the ACCEPT
// bypasses REQ and what the expected end is; empty without prereq. It
// follows a "//*" card, hence the leading newline.
func bypassNote(d *Distribution) string {
	if len(d.SMP.Prereq) == 0 {
		return ""
	}
	return "\n//* BYPASS(REQ) ON THE ACCEPT\n" +
		"//*   This SYSMOD names a prerequisite (++VER ... REQ). APPLY checked\n" +
		"//*   it against the CDS and still does. ACCEPT checks a requisite\n" +
		"//*   again, against the ACDS, and a prerequisite that is applied but\n" +
		"//*   cannot be accepted (a usermod whose own requisite is not\n" +
		"//*   accepted) would fail this step for good. So the ACCEPT bypasses\n" +
		"//*   REQ, for this one SYSMOD only.\n" +
		"//*   The expected end of this job is RC 04, with the message\n" +
		"//*     HMA3590     --- <id> REQ BYPASSED\n" +
		"//*   for each prerequisite."
}

// renderAcceptJob: the job that accepts the FMID and then scratches the
// staging libraries SMP read it from.
func renderAcceptJob(d *Distribution, jobcard, name, ver, instJob string) (string, error) {
	cleanup, err := renderCleanupStep(d, "ACCEPT.HMASMP")
	if err != nil {
		return "", err
	}
	what := stagingWord(d)
	return render("smpaccept.jcl.tpl", map[string]string{
		"JOBCARD": jobcard, "PRODUCT": name, "VERSION": ver, "FMID": d.SMP.FMID,
		"INSTALL_JOB": instJob, "STAGING": what, "BYPASS_NOTE": bypassNote(d),
		"ACCEPT_STEP": renderAcceptStep(d), "CLEANUP_STEP": cleanup,
	})
}

func stagingWord(d *Distribution) string {
	if len(d.delivered()) > 0 {
		return "libraries"
	}
	return "library"
}

// stagingDeletes: the DELETE statements that remove the staging libraries.
func stagingDeletes(d *Distribution) []string {
	dels := []string{fmt.Sprintf("DELETE %s NONVSAM SCRATCH PURGE", d.SMP.LKLib)}
	for _, l := range d.delivered() {
		dels = append(dels, fmt.Sprintf("DELETE %s NONVSAM SCRATCH PURGE", l.Stage))
	}
	return dels
}

// renderInstallEnd: the end of the install job when the ACCEPT is a job of
// its own. It says what to do next and how to remove the staging libraries
// for someone who never accepts.
func renderInstallEnd(d *Distribution, acceptJob string) string {
	w, stay := stagingWord(d), "stays"
	if w == "libraries" {
		stay = "stay"
	}
	lines := []string{
		"//*",
		"//* ---- WHAT NEXT ----------------------------------------------------",
		"//* The function is APPLIED, not accepted: a RESTORE can still take it",
		"//* out again. Try the product. When you want to keep this level,",
		"//* submit",
		"//*     " + acceptJob,
		"//* ACCEPT is the point without a way back, so the decision is yours.",
		"//* That job scratches the staging " + w + ", which " + stay + " until then.",
		"//*",
		"//* If you RESTORE it, or never ACCEPT it, scratch the staging",
		"//* " + w + " yourself, with IDCAMS:",
	}
	for _, dl := range stagingDeletes(d) {
		lines = append(lines, "//*     "+dl)
	}
	return strings.Join(lines, "\n")
}

func renderCleanupStep(d *Distribution, after string) (string, error) {
	for _, a := range d.allocated() {
		if a == d.SMP.LKLib {
			return "", errf("refusing to generate a cleanup step for %s: it is one of the libraries SMP installs into", d.SMP.LKLib)
		}
	}
	cond := "0,NE"
	// RC 4 is how a step ends with a ++SRC (HMA4441) and, on the ACCEPT with
	// prereq, by design (REQ BYPASSED).
	if d.hasSRC() || (after == "ACCEPT.HMASMP" && len(d.SMP.Prereq) > 0) {
		cond = "4,LT"
	}
	lines := []string{
		"//*",
		"//* ---- remove the staging library -----------------------------------",
		"//* Only reached when everything above worked. Comment the step out",
		"//* if you would rather keep the shipped modules on the system.",
		"//*",
		fmt.Sprintf("//CLEANUP EXEC PGM=IDCAMS,COND=(%s,%s)", cond, after),
		"//SYSPRINT DD  SYSOUT=*",
		"//SYSIN    DD  *",
	}
	for _, dl := range stagingDeletes(d) {
		lines = append(lines, "  "+dl)
	}
	lines = append(lines, "/*", "//")
	return strings.Join(lines, "\n"), nil
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
