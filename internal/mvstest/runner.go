// Package mvstest runs a project's test modules on MVS -- mbt v2's
// `make test-mvs` (scripts/mbttest.py): pack the built tests into a TESTLIB,
// RECEIVE it, allocate the fixture PDSes, submit one runner job with a batch
// and a TSO step per test, and read each step's verdict out of the spool.
//
// This file is the pure half -- the runner JCL and the reading of a spool --
// so it can be checked against mbt v2 without a mainframe.
package mvstest

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RunnerRegion: REGION=0M on an EXEC is not "unlimited" on MVS 3.8j.
const RunnerRegion = "8M"

// fixDLM: '/*' cannot end the IEBGENER instream data, a REXX exec starts
// with one.
const fixDLM = "$A"

// Fixture is one [[test.fixture]] block: its DD, its PDS, its members.
type Fixture struct {
	DD      string
	PDS     string
	Members [][2]string // name, text
}

// Parm is a test's program argument per leg; nil means none.
type Parm struct{ Batch, TSO *string }

// FixtureSpace sizes a fixture PDS to its members (#122): blocks counted per
// member, doubled for margin, never less than TRK(2,1,5).
func FixtureSpace(members [][2]string) []any {
	blocks := 0
	for _, m := range members {
		n := int(math.Ceil(float64(len(splitLines(m[1]))) / 39))
		if n < 1 {
			n = 1
		}
		blocks += n
	}
	dirblks := int(math.Ceil(float64(len(members))/6)) + 1
	if dirblks < 5 {
		dirblks = 5
	}
	tracks := int(math.Ceil(float64(blocks)/4)) + int(math.Ceil(float64(dirblks)/16))
	primary := 2 * tracks
	if primary < 2 {
		primary = 2
	}
	sec := primary / 2
	if sec < 1 {
		sec = 1
	}
	return []any{"TRK", primary, sec, dirblks}
}

// splitLines is Python's str.splitlines for the line breaks that occur here.
func splitLines(s string) []string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// ddCard is a DD DSN=...,DISP=SHR card, continued when it would pass col 71.
func ddCard(dd, dsn string) string {
	card := fmt.Sprintf("//%-8s DD DSN=%s,DISP=SHR", dd, dsn)
	if len(card) <= 71 {
		return card
	}
	return fmt.Sprintf("//%-8s DD DSN=%s,\n//             DISP=SHR", dd, dsn)
}

func fixtureDDs(fx map[string][]Fixture, test string) string {
	var b strings.Builder
	for _, f := range fx[test] {
		b.WriteString(ddCard(f.DD, f.PDS) + "\n")
	}
	return b.String()
}

// Step maps a runner step to its test and leg.
type Step struct{ Test, Leg string }

// FxStep maps a fixture-load step to what it loads.
type FxStep struct{ Test, DD, Member, PDS string }

// GenRunner builds the runner JCL: fixture loads first, then a batch step per
// test, then a TSO step per test, each COND=EVEN so one failure blocks none.
// linklib "" means no production library is deployed.
func GenRunner(jobcard string, tests []string, testlib, linklib string, fx map[string][]Fixture, parms map[string]Parm) (string, map[string]Step, map[string]FxStep, []string, []string) {
	steplib := fmt.Sprintf("//STEPLIB  DD DSN=%s,DISP=SHR\n", testlib)
	if linklib != "" {
		steplib += fmt.Sprintf("//         DD DSN=%s,DISP=SHR\n", linklib)
	}
	lines := []string{jobcard}
	steps := map[string]Step{}
	fxSteps := map[string]FxStep{}
	var stepOrder, fxOrder []string
	i := 0
	for _, t := range tests {
		for _, f := range fx[t] {
			for _, m := range f.Members {
				i++
				name := fmt.Sprintf("FX%03d", i)
				fxSteps[name] = FxStep{t, f.DD, m[0], f.PDS}
				fxOrder = append(fxOrder, name)
				lines = append(lines, fmt.Sprintf("//%s  EXEC PGM=IEBGENER", name), "//SYSPRINT DD SYSOUT=*", "//SYSIN    DD DUMMY",
					ddCard("SYSUT2", fmt.Sprintf("%s(%s)", f.PDS, m[0])), "//SYSUT1   DD *,DLM="+fixDLM)
				lines = append(lines, splitLines(m[1])...)
				lines = append(lines, fixDLM)
			}
		}
	}
	for n, t := range tests {
		b := fmt.Sprintf("B%02d", n+1)
		parm := ""
		if p, ok := parms[t]; ok && p.Batch != nil {
			parm = fmt.Sprintf(",PARM='%s'", *p.Batch)
		}
		lines = append(lines, fmt.Sprintf("//%-8sEXEC PGM=%s,COND=EVEN,REGION=%s%s", b, t, RunnerRegion, parm), strings.TrimRight(steplib, "\n"))
		if d := fixtureDDs(fx, t); d != "" {
			lines = append(lines, strings.TrimRight(d, "\n"))
		}
		lines = append(lines, "//SYSPRINT DD SYSOUT=*", "//SYSTERM  DD SYSOUT=*", "//SYSTSPRT DD SYSOUT=*", "//SYSUDUMP DD SYSOUT=*")
		steps[b] = Step{t, "batch"}
		stepOrder = append(stepOrder, b)
	}
	for n, t := range tests {
		s := fmt.Sprintf("T%02d", n+1)
		lines = append(lines, fmt.Sprintf("//%-8sEXEC PGM=IKJEFT01,DYNAMNBR=50,REGION=%s,COND=EVEN", s, RunnerRegion), strings.TrimRight(steplib, "\n"))
		if d := fixtureDDs(fx, t); d != "" {
			lines = append(lines, strings.TrimRight(d, "\n"))
		}
		lines = append(lines, "//SYSTSPRT DD SYSOUT=*", "//SYSPRINT DD SYSOUT=*", "//SYSTERM  DD SYSOUT=*", "//SYSTSIN  DD *")
		arg := ""
		if p, ok := parms[t]; ok && p.TSO != nil {
			arg = fmt.Sprintf(" '%s'", *p.TSO)
		}
		lines = append(lines, fmt.Sprintf(" CALL '%s(%s)'%s", testlib, t, arg), "/*")
		steps[s] = Step{t, "tso"}
		stepOrder = append(stepOrder, s)
	}
	return strings.Join(lines, "\n") + "\n", steps, fxSteps, stepOrder, fxOrder
}

// NoRC: the step has no verdict in the spool at all.
const NoRC = "NO RC"

// StepRC reads one step's verdict: rc 9999 for an ABEND, -1 when absent.
func StepRC(spool, job, step string) (int, string) {
	if m := regexp.MustCompile(fmt.Sprintf(`IEF450I\s+%s\s+%s\s+-\s+ABEND\s+(\S+)`, job, step)).FindStringSubmatch(spool); m != nil {
		return 9999, "ABEND " + m[1]
	}
	if m := regexp.MustCompile(fmt.Sprintf(`IEF142I\s+%s\s+%s\s+-\s+STEP WAS EXECUTED\s+-\s+COND CODE\s+(\d+)`, job, step)).FindStringSubmatch(spool); m != nil {
		rc, _ := strconv.Atoi(m[1]) // "0012" is twelve, not octal ten
		return rc, "CC"
	}
	if regexp.MustCompile(fmt.Sprintf(`IEF272I\s+%s\s+%s\s+-\s+STEP WAS NOT EXECUTED`, job, step)).MatchString(spool) {
		return -1, "NOT EXECUTED"
	}
	return -1, NoRC
}

// Verdict is one leg's (rc, status); rc -1 is "none".
type Verdict struct {
	RC     int
	Status string
}

var jesDDs = map[string]bool{"JESJCLIN": true, "JESMSGLG": true, "JESJCL": true, "JESYSMSG": true}

// verdictsAtRisk: only a lost JES DD can have swallowed a step's verdict.
func verdictsAtRisk(errs []string) bool {
	for _, e := range errs {
		if jesDDs[strings.SplitN(e, ":", 2)[0]] {
			return true
		}
	}
	return false
}

// Matrix renders the per-test table; it returns the lines, the failed and
// the unread (??) cell counts.
func Matrix(rows map[string]map[string]Verdict, spoolErrs []string, fxFailed map[string]bool) ([]string, int, int) {
	unknown := verdictsAtRisk(spoolErrs)
	lines := []string{fmt.Sprintf("  %-10s %-14s %-14s", "TEST", "BATCH", "TSO"),
		fmt.Sprintf("  %s %s %s", strings.Repeat("-", 10), strings.Repeat("-", 14), strings.Repeat("-", 14))}
	var tests []string
	for t := range rows {
		tests = append(tests, t)
	}
	sort.Strings(tests)
	failed, unread := 0, 0
	for _, t := range tests {
		var cells []string
		for _, leg := range []string{"batch", "tso"} {
			v, ok := rows[t][leg]
			if !ok {
				v = Verdict{-1, "MISSING"}
			}
			if unknown && v.Status == NoRC {
				unread++
				cells = append(cells, "??   "+v.Status)
				continue
			}
			okv := v.RC == 0
			if !okv {
				failed++
			}
			cell := "FAIL "
			if okv {
				cell = "ok "
			}
			if v.RC == -1 || v.RC == 9999 {
				cell += v.Status
			} else {
				cell += fmt.Sprintf("CC %d", v.RC)
			}
			cells = append(cells, cell)
		}
		row := fmt.Sprintf("  %-10s %-14s %-14s", t, cells[0], cells[1])
		if fxFailed[t] {
			row += " fixture not loaded"
		}
		lines = append(lines, row)
	}
	return lines, failed, unread
}

const maxSpoolErrors = 5

// ReadbackDetails lists failed readback requests and where the RCs still are.
func ReadbackDetails(errs []string) []string {
	shown := append([]string{}, errs...)
	if len(shown) > maxSpoolErrors {
		shown = append(shown[:maxSpoolErrors], fmt.Sprintf("... and %d more", len(errs)-maxSpoolErrors))
	}
	return append(shown,
		"this is the readback failing, not the job -- the tests may well have passed",
		"the return codes are on the console: IEFACTRT writes the per-step RC to SYSLOG,",
		"  IEFACTRT B05     /TSTEXPIR/00:00:00.02/00:00:00.05/00000/MBTTEST",
		"  "+strings.Repeat(" ", 51)+"^^^^^ step RC",
		"and $HASP165 carries the job-level MAX COND CODE")
}

type fxBad struct {
	step   string
	rc     int
	status string
}

func fixtureBadSteps(spool, job string, fxOrder []string, errs []string) []fxBad {
	unknown := verdictsAtRisk(errs)
	var bad []fxBad
	for _, s := range fxOrder {
		rc, st := StepRC(spool, job, s)
		if rc == 0 || (unknown && st == NoRC) {
			continue
		}
		bad = append(bad, fxBad{s, rc, st})
	}
	return bad
}

// FixtureFailedTests: the tests one of whose fixture members did not load.
func FixtureFailedTests(spool, job string, fx map[string]FxStep, fxOrder []string, errs []string) map[string]bool {
	out := map[string]bool{}
	for _, b := range fixtureBadSteps(spool, job, fxOrder, errs) {
		out[fx[b.step].Test] = true
	}
	return out
}

var x37 = regexp.MustCompile(`\bS[0-9A-F]37\b`)

// FixtureFailures names the fixture loads that failed; "" when none did.
func FixtureFailures(spool, job string, fx map[string]FxStep, fxOrder []string, errs []string) (string, []string) {
	bad := fixtureBadSteps(spool, job, fxOrder, errs)
	if len(bad) == 0 {
		return "", nil
	}
	tset := map[string]bool{}
	for _, b := range bad {
		tset[fx[b.step].Test] = true
	}
	var tests []string
	for t := range tset {
		tests = append(tests, t)
	}
	sort.Strings(tests)
	var details, run []string
	runSt := ""
	flush := func() {
		if len(run) == 0 {
			return
		}
		span := run[0]
		if len(run) > 1 {
			span = run[0] + "-" + run[len(run)-1]
		}
		dset := map[string]bool{}
		for _, s := range run {
			dset[fx[s].DD] = true
		}
		var dds []string
		for d := range dset {
			dds = append(dds, d)
		}
		sort.Strings(dds)
		details = append(details, fmt.Sprintf("%s %s: %d member(s) not loaded (%s)", span, strings.Join(dds, "/"), len(run), runSt))
		run = nil
	}
	sawX37 := false
	for _, b := range bad {
		f := fx[b.step]
		if b.rc == -1 {
			if len(run) > 0 && runSt != b.status {
				flush()
			}
			runSt = b.status
			run = append(run, b.step)
			continue
		}
		flush()
		verdict := fmt.Sprintf("CC %d", b.rc)
		if b.rc == 9999 {
			verdict = b.status
		}
		details = append(details, fmt.Sprintf("%s %s(%s) into %s: %s", b.step, f.DD, f.Member, f.PDS, verdict))
		sawX37 = sawX37 || (b.rc == 9999 && x37.MatchString(b.status))
	}
	flush()
	if sawX37 {
		details = append(details, "an x37 ABEND is the fixture PDS out of space")
	}
	details = append(details, "the test results of "+strings.Join(tests, ", ")+" are not about the tests")
	return "fixture load failed for " + strings.Join(tests, ", "), details
}
