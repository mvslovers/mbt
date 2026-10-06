package mvstest

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
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
	jcl, steps, fxs, stepOrder, fxOrder := GenRunner(jc, []string{"TSTLOAD", "TSTJCL", "TSTP"}, "IBMUSER.REXX370.V1R0M0D.TESTLIB", "REXX370.DEV.LINKLIB", fx, parms)
	if jcl != g.JCL {
		t.Errorf("runner JCL differs from v2:\n--- got\n%s\n--- want\n%s", jcl, g.JCL)
	}
	if jcl2, _, _, _, _ := GenRunner(jc, []string{"TSTP"}, "T.TESTLIB", "", nil, nil); jcl2 != g.JCL2 {
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
	l, f, u := Matrix(rows, nil, map[string]bool{"TSTLOAD": true})
	check("matrix", l, f, u, g.Matrix)
	l, f, u = Matrix(rows, []string{"JESMSGLG: HTTP 500"}, nil)
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
