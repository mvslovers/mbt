package mvstest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/mvsmf"
)

// noAssertionRun builds a runner for tests, reads the verdicts out of the
// JES2 lines and returns what the end of run.go prints from them.
func noAssertionRun(t *testing.T, tests []string, jes string, files []mvsmf.SpoolFile) ([]string, int, []string, string) {
	t.Helper()
	_, steps, _, order, _ := GenRunner("//J JOB", tests, "T.TESTLIB", "", nil, nil, nil)
	spool := jes
	for _, f := range files {
		spool += "\n" + f.Text
	}
	rows := map[string]map[string]Verdict{}
	for _, s := range order {
		st := steps[s]
		if rows[st.Test] == nil {
			rows[st.Test] = map[string]Verdict{}
		}
		rc, status := StepRC(spool, "MBTTEST", s)
		rows[st.Test][st.Leg] = Verdict{rc, status}
	}
	files = append([]mvsmf.SpoolFile{{DD: "JESMSGLG", Step: "JES2", Text: jes}}, files...)
	none := NoAssertions(rows, steps, files, spool, nil)
	lines, failed, _ := Matrix(rows, nil, nil, none)
	bare := Unasserted(none)
	return lines, failed, bare, PassedLine(len(bare))
}

func cc(step string, rc string) string {
	return " IEF142I MBTTEST " + step + " - STEP WAS EXECUTED - COND CODE " + rc + "\n"
}

const matrixHead = "  TEST       BATCH          TSO           \n  ---------- -------------- --------------"

func checkRun(t *testing.T, name string, lines []string, failed int, bare []string, last string, wantLines string, wantFailed int, wantBare []string, wantLast string) {
	t.Helper()
	if got := strings.Join(lines, "\n"); got != matrixHead+"\n"+wantLines {
		t.Errorf("%s: matrix\n%s\nwant\n%s", name, got, matrixHead+"\n"+wantLines)
	}
	if failed != wantFailed || !reflect.DeepEqual(bare, wantBare) || last != wantLast {
		t.Errorf("%s: failed %d, bare %q, last %q; want %d, %q, %q", name, failed, bare, last, wantFailed, wantBare, wantLast)
	}
}

// A PARM-gated test run without its PARM skips itself and ends CC 0: both
// legs are marked and named, and the last line says so (#288).
func TestNoAssertionsMarked(t *testing.T) {
	lines, failed, bare, last := noAssertionRun(t, []string{"TSTGATE"}, cc("B01", "0000")+cc("T01", "0000"), []mvsmf.SpoolFile{
		{DD: "SYSPRINT", Step: "B01", Text: "TSTGATE: no PARM='RUN', skipped\n"},
		{DD: "SYSTSPRT", Step: "T01", Text: "TSTGATE: no PARM='RUN', skipped\nREADY\n"},
	})
	checkRun(t, "none asserted", lines, failed, bare, last,
		"  TSTGATE    ok CC 0 (none) ok CC 0 (none)", 0,
		[]string{"TSTGATE (batch)", "TSTGATE (tso)"}, "all test steps passed, 2 without assertions")
}

// Only the legs without an assertion are marked; a PASS: in one leg does
// not count for the other.
func TestNoAssertionsMixed(t *testing.T) {
	lines, failed, bare, last := noAssertionRun(t, []string{"TSTA", "TSTGATE"},
		cc("B01", "0000")+cc("B02", "0000")+cc("T01", "0000")+cc("T02", "0000"), []mvsmf.SpoolFile{
			{DD: "SYSPRINT", Step: "B01", Text: "  PASS: one\n  PASS: two\n"},
			{DD: "SYSPRINT", Step: "B02", Text: "PASS: gate open\n"},
			{DD: "SYSPRINT", Step: "T01", Text: "PASS: one\n"},
			{DD: "SYSTSPRT", Step: "T02", Text: "READY\n"},
		})
	checkRun(t, "mixed", lines, failed, bare, last,
		"  TSTA       ok CC 0        ok CC 0       \n"+
			"  TSTGATE    ok CC 0        ok CC 0 (none)", 0,
		[]string{"TSTGATE (tso)"}, "all test steps passed, 1 without assertions")
}

// Every leg asserted something: the output is what it was before #288.
func TestNoAssertionsAllAsserted(t *testing.T) {
	lines, failed, bare, last := noAssertionRun(t, []string{"TSTA"}, cc("B01", "0000")+cc("T01", "0000"), []mvsmf.SpoolFile{
		{DD: "SYSPRINT", Step: "B01", Text: "PASS: one\n"},
		{DD: "SYSTSPRT", Step: "T01", Text: "FAIL: soft, still rc 0\n"},
	})
	checkRun(t, "all asserted", lines, failed, bare, last,
		"  TSTA       ok CC 0        ok CC 0       ", 0, nil, "all test steps passed")
}

// A failed leg is a failure, whatever it printed: it is never marked.
func TestNoAssertionsFailedLeg(t *testing.T) {
	jes := cc("B01", "0008") + " IEF450I MBTTEST T01 - ABEND S0C4 U0000\n"
	lines, failed, bare, _ := noAssertionRun(t, []string{"TSTBAD"}, jes, []mvsmf.SpoolFile{
		{DD: "SYSPRINT", Step: "B01", Text: "nothing\n"},
	})
	checkRun(t, "failed", lines, failed, bare, "",
		"  TSTBAD     FAIL CC 8      FAIL ABEND S0C4", 2, nil, "")
}

// A leg one of whose files could not be read has an unknown count, not a
// zero one; and a spool whose files name no step marks only when it holds
// no assertion at all.
func TestNoAssertionsUnreadAndUnattributed(t *testing.T) {
	_, steps, _, _, _ := GenRunner("//J JOB", []string{"TSTA"}, "T.TESTLIB", "", nil, nil, nil)
	rows := map[string]map[string]Verdict{"TSTA": {"batch": {0, "CC"}, "tso": {0, "CC"}}}
	none := NoAssertions(rows, steps, []mvsmf.SpoolFile{
		{DD: "SYSPRINT", Step: "B01", Failed: true},
		{DD: "SYSTSPRT", Step: "T01", Text: "READY\n"},
	}, "", []string{"SYSPRINT: HTTP 500"})
	if got := Unasserted(none); !reflect.DeepEqual(got, []string{"TSTA (tso)"}) {
		t.Errorf("unread batch leg: %q, want only the tso leg", got)
	}
	flat := []mvsmf.SpoolFile{{DD: "SYSPRINT", Text: "skipped\n"}}
	if got := Unasserted(NoAssertions(rows, steps, flat, "skipped\n", nil)); !reflect.DeepEqual(got, []string{"TSTA (batch)", "TSTA (tso)"}) {
		t.Errorf("no step names, no assertions: %q", got)
	}
	if got := NoAssertions(rows, steps, flat, "PASS: one\n", nil); got != nil {
		t.Errorf("no step names, one assertion: %v, want nothing marked", got)
	}
	if got := NoAssertions(rows, steps, flat, "skipped\n", []string{"SYSPRINT: HTTP 500"}); got != nil {
		t.Errorf("no step names, part of the spool unread: %v, want nothing marked", got)
	}
}
