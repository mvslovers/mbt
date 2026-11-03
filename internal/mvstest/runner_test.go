package mvstest

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// testdata/v2.json was rendered by mbt v2's scripts/mbttest.py.
type golden struct {
	JCL          string `json:"jcl"`
	JCL2         string `json:"jcl_nolink"`
	Steps        map[string][]string
	Fx           map[string][]string
	Spool        string
	Rows         map[string]map[string][]any
	Matrix       []any
	MatrixUnread []any `json:"matrix_unread"`
	FxFail       []any
	JobFail      map[string][]any
	Space        []any
	Readback     []string
}

func load(t *testing.T) golden {
	data, err := os.ReadFile("testdata/v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func s(v string) *string { return &v }

func TestRunnerMatchesV2(t *testing.T) {
	g := load(t)
	fx := map[string][]Fixture{
		"TSTLOAD": {{DD: "SYSEXEC", PDS: "IBMUSER.REXX370.FIX.TSTLOAD.SYSEXEC", Members: [][2]string{{"HELLO", "/* REXX */\nsay 'hi'\n"}, {"LONGNAMEX", "x\n"}}}},
		"TSTJCL":  {{DD: "SYSPROC", PDS: "IBMUSERX.REXX370X.FIX.TSTJCLXX.SYSPROC1", Members: [][2]string{{"A", "a\r\nb\n"}}}},
	}
	parms := map[string]Parm{"TSTLOAD": {Batch: s("0"), TSO: s("1")}, "TSTP": {TSO: s("x y")}}
	jc := "//MBTTEST JOB (A),'MBT TEST',\n//          CLASS=A,MSGCLASS=H,\n//          MSGLEVEL=(1,1),\n//          NOTIFY=&SYSUID"
	jcl, steps, fxs, stepOrder, fxOrder := GenRunner(jc, []string{"TSTLOAD", "TSTJCL", "TSTP"}, "IBMUSER.REXX370.V1R0M0D.TESTLIB", "REXX370.DEV.LINKLIB", fx, parms, nil)
	// the one intended difference (#261): fixtures go in as DD DATA, which
	// v2's DD * could not do for a member holding JCL
	want := strings.ReplaceAll(g.JCL, "//SYSUT1   DD *,DLM=$A", "//SYSUT1   DD DATA,DLM=$A")
	if jcl != want {
		t.Errorf("runner JCL differs from v2:\n--- got\n%s\n--- want\n%s", jcl, want)
	}
	if jcl2, _, _, _, _ := GenRunner(jc, []string{"TSTP"}, "T.TESTLIB", "", nil, nil, nil); jcl2 != g.JCL2 {
		t.Errorf("runner JCL without a LINKLIB differs:\n%s\n---\n%s", jcl2, g.JCL2)
	}
	for k, v := range g.Steps {
		if steps[k] != (Step{v[0], v[1]}) {
			t.Errorf("step %s = %v, want %v", k, steps[k], v)
		}
	}
	for k, v := range g.Fx {
		if fxs[k] != (FxStep{v[0], v[1], v[2], v[3]}) {
			t.Errorf("fx step %s = %v, want %v", k, fxs[k], v)
		}
	}

	rows := map[string]map[string]Verdict{}
	for _, st := range stepOrder {
		s := steps[st]
		if rows[s.Test] == nil {
			rows[s.Test] = map[string]Verdict{}
		}
		rc, status := StepRC(g.Spool, "MBTTEST", st)
		rows[s.Test][s.Leg] = Verdict{rc, status}
	}
	for test, legs := range g.Rows {
		for leg, v := range legs {
			want := Verdict{int(v[0].(float64)), v[1].(string)}
			if rows[test][leg] != want {
				t.Errorf("%s/%s = %v, want %v", test, leg, rows[test][leg], want)
			}
		}
	}
	check := func(name string, lines []string, failed, unread int, want []any) {
		var wl []string
		for _, l := range want[0].([]any) {
			wl = append(wl, l.(string))
		}
		if !reflect.DeepEqual(lines, wl) || failed != int(want[1].(float64)) || unread != int(want[2].(float64)) {
			t.Errorf("%s:\n%q %d %d\nwant\n%q %v %v", name, lines, failed, unread, wl, want[1], want[2])
		}
	}
	l, f, u := Matrix(rows, nil, map[string]bool{"TSTLOAD": true}, nil)
	check("matrix", l, f, u, g.Matrix)
	l, f, u = Matrix(rows, []string{"JESMSGLG: HTTP 500"}, nil, nil)
	check("matrix with a lost JES DD", l, f, u, g.MatrixUnread)

	h, d := FixtureFailures(g.Spool, "MBTTEST", fxs, fxOrder, nil)
	if fmt.Sprint([]any{h, d}) != fmt.Sprint(g.FxFail) {
		t.Errorf("fixture failures:\n%v %q\nwant %v", h, d, g.FxFail)
	}
}

// A COND CODE with a leading zero is decimal: fmt.Sscan read 0012 as octal
// 10, which only the comparison against a real spool (JOB01382) showed.
func TestStepRCIsDecimal(t *testing.T) {
	if rc, st := StepRC(" IEF142I MBTTEST T14 - STEP WAS EXECUTED - COND CODE 0012", "MBTTEST", "T14"); rc != 12 || st != "CC" {
		t.Errorf("got %d %s, want 12 CC", rc, st)
	}
}

func TestJobFailureMatchesV2(t *testing.T) {
	g := load(t)
	norc := map[string]map[string]Verdict{"X": {"batch": {-1, NoRC}, "tso": {-1, NoRC}}}
	cases := map[string][3]any{
		"jcl":      {"UNKNOWN", " IEF452I MBTTEST JOB NOT RUN - JCL ERROR\n   12 IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD\n", []string(nil)},
		"timeout":  {"TIMEOUT", "", []string(nil)},
		"readback": {"CC", "partial", []string{"JESMSGLG: HTTP 500", "JESYSMSG: HTTP 500"}},
		"none":     {"CC", "x", []string(nil)},
	}
	for k, c := range cases {
		h, d := JobFailure(c[0].(string), c[1].(string), norc, "MBTTEST", "JOB00009", 120, "build/r.jcl", "build/r.spool", c[2].([]string))
		if fmt.Sprint([]any{h, d}) != fmt.Sprint(g.JobFail[k]) {
			t.Errorf("%s:\n%v %q\nwant %v", k, h, d, g.JobFail[k])
		}
	}
	if !reflect.DeepEqual(ReadbackDetails([]string{"a", "a", "a", "a", "a", "a", "a"}), g.Readback) {
		t.Errorf("readback details differ")
	}
	sp := FixtureSpace([][2]string{{"A", stringsRepeat("x\n", 100)}, {"B", "y"}})
	if fmt.Sprint(sp) != fmt.Sprint(g.Space) {
		t.Errorf("fixture space %v, want %v", sp, g.Space)
	}
}

func stringsRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out[:len(out)-1]
}

// A test with COBOL sources gets a SYSOUT DD in both steps (DISPLAY);
// the others keep the JCL mbt 2 wrote.
func TestRunnerSYSOUTForCOBOL(t *testing.T) {
	jcl, _, _, _, _ := GenRunner("//J JOB", []string{"CTEST", "COBTEST"}, "T.TESTLIB", "", nil, nil, map[string]bool{"COBTEST": true})
	if n := strings.Count(jcl, "//SYSOUT   DD SYSOUT=*"); n != 2 {
		t.Errorf("%d SYSOUT DDs:\n%s", n, jcl)
	}
	b := jcl[strings.Index(jcl, "PGM=COBTEST"):]
	if !strings.Contains(b[:strings.Index(b, "//T01")], "//SYSOUT ") {
		t.Errorf("no SYSOUT in COBTEST's batch step:\n%s", jcl)
	}
	tso := jcl[strings.Index(jcl, "//T02"):]
	if i, j := strings.Index(tso, "//SYSOUT "), strings.Index(tso, "//SYSTSIN"); i < 0 || i > j {
		t.Errorf("SYSOUT must come before the instream SYSTSIN:\n%s", tso)
	}
}

// mbt#95: an EXEC card past column 71 is rejected by JES2 with a message
// that says nothing about PARM. The card is continued instead; what does
// not fit even then is refused before anything is uploaded.
func TestLongBatchParm(t *testing.T) {
	longest := func(card string) int {
		n := 0
		for _, l := range strings.Split(card, "\n") {
			if len(l) > n {
				n = len(l)
			}
		}
		return n
	}
	run := func(test, parm string) string {
		jcl, _, _, _, _ := GenRunner("//J JOB", []string{test}, "T.TESTLIB", "", nil, map[string]Parm{test: {Batch: s(parm)}}, nil)
		return jcl
	}
	// fits on one card: unchanged
	if jcl := run("TSTPRM", "X"); !strings.Contains(jcl, "//B01     EXEC PGM=TSTPRM,COND=EVEN,REGION=8M,PARM='X'\n") {
		t.Errorf("short parm:\n%s", jcl)
	}
	// the case of the issue: 72 columns
	jcl := run("TSTPRM", "IBMUSER.FTPD.TSTPRM")
	want := "//B01     EXEC PGM=TSTPRM,COND=EVEN,REGION=8M,\n//             PARM='IBMUSER.FTPD.TSTPRM'\n"
	if !strings.Contains(jcl, want) {
		t.Errorf("continued card missing:\n%s", jcl)
	}
	// a full data set name
	dsn := "IBMUSER." + strings.Repeat("A", 36)
	jcl = run("TSTPRM", dsn)
	if n := longest(jcl); n > 71 {
		t.Errorf("a card is %d columns:\n%s", n, jcl)
	}
	if !strings.Contains(jcl, "PARM='"+dsn+"'") {
		t.Errorf("parm lost:\n%s", jcl)
	}
}

func TestCheckParms(t *testing.T) {
	ok := map[string]Parm{"TSTPRM": {Batch: s(strings.Repeat("A", 49)), TSO: s("x")}}
	if err := CheckParms([]string{"TSTPRM"}, ok); err != nil {
		t.Errorf("49 chars fit on the continued card: %v", err)
	}
	bad := map[string]Parm{"TSTPRM": {Batch: s(strings.Repeat("A", 50))}}
	err := CheckParms([]string{"TSTPRM"}, bad)
	if err == nil || !strings.Contains(err.Error(), "TSTPRM") || !strings.Contains(err.Error(), "parm") || !strings.Contains(err.Error(), "50") || !strings.Contains(err.Error(), "49") {
		t.Errorf("batch parm too long: %v", err)
	}
	tso := map[string]Parm{"TSTPRM": {TSO: s(strings.Repeat("A", 69))}}
	if err := CheckParms([]string{"TSTPRM"}, tso); err != nil {
		t.Errorf("69 chars fit on the continued CALL: %v", err)
	}
	tso["TSTPRM"] = Parm{TSO: s(strings.Repeat("A", 70))}
	if err := CheckParms([]string{"TSTPRM"}, tso); err == nil || !strings.Contains(err.Error(), "TSO") {
		t.Errorf("tso parm too long: %v", err)
	}
	if err := CheckParms([]string{"A"}, nil); err != nil {
		t.Errorf("no parms: %v", err)
	}
}

// mbt#95, TSO leg: TSO reads columns 1-72 of a SYSTSIN record (measured on
// MVS/CE: a keyword in columns 74-80 was ignored); a longer CALL is
// continued with a trailing '-', which TSO joined (same run).
func TestLongTSOParm(t *testing.T) {
	run := func(parm string) []string {
		jcl, _, _, _, _ := GenRunner("//J JOB", []string{"TSTPRM"}, "IBMUSER.FTPD.V1R0M0D.TESTLIB", "", nil, map[string]Parm{"TSTPRM": {TSO: s(parm)}}, nil)
		return strings.Split(jcl, "\n")
	}
	has := func(ls []string, l string) bool {
		for _, x := range ls {
			if x == l {
				return true
			}
		}
		return false
	}
	if ls := run("X"); !has(ls, " CALL 'IBMUSER.FTPD.V1R0M0D.TESTLIB(TSTPRM)' 'X'") {
		t.Errorf("short arg: %q", ls)
	}
	dsn := "IBMUSER." + strings.Repeat("A", 36)
	ls := run(dsn)
	if !has(ls, " CALL 'IBMUSER.FTPD.V1R0M0D.TESTLIB(TSTPRM)' -") || !has(ls, " '"+dsn+"'") {
		t.Errorf("continued CALL missing: %q", ls)
	}
	for _, l := range ls {
		if len(l) > 72 && strings.HasPrefix(l, " ") {
			t.Errorf("SYSTSIN record of %d columns: %q", len(l), l)
		}
	}
}
