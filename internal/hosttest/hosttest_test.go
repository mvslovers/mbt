package hosttest

import (
	"bytes"
	"testing"
)

// A test that passed without a single PASS: or FAIL: line is marked in its
// row and named in a warning, and the last line says how many (#288).
func TestReportNoAssertions(t *testing.T) {
	head := "\n  TEST       RESULT       ASSERTIONS    \n  ---------- ------------ --------------\n"
	cases := []struct {
		name string
		rows map[string]row
		ok   bool
		want string
	}{
		{"none asserted", map[string]row{
			"TSTGATE": {true, "rc=0", 0, 0},
		}, true, head + `  TSTGATE    ok  rc=0     no assertions

  1 host test(s) | assertions: 0 PASS, 0 FAIL
[mbt] WARNING: 1 host test(s) passed without a single assertion: TSTGATE
[mbt] all host tests passed, 1 without assertions
`},
		{"mixed", map[string]row{
			"TSTA":    {true, "rc=0", 3, 0},
			"TSTGATE": {true, "rc=0", 0, 0},
			"TSTRC":   {true, "rc=0", 0, 0},
		}, true, head + `  TSTA       ok  rc=0     3 pass / 0 fail
  TSTGATE    ok  rc=0     no assertions
  TSTRC      ok  rc=0     no assertions

  3 host test(s) | assertions: 3 PASS, 0 FAIL
[mbt] WARNING: 2 host test(s) passed without a single assertion: TSTGATE, TSTRC
[mbt] all host tests passed, 2 without assertions
`},
		// every test asserted something: the output mbt printed before #288
		{"all asserted", map[string]row{
			"TSTA": {true, "rc=0", 3, 0},
			"TSTB": {true, "rc=0", 1, 0},
		}, true, head + `  TSTA       ok  rc=0     3 pass / 0 fail
  TSTB       ok  rc=0     1 pass / 0 fail

  2 host test(s) | assertions: 4 PASS, 0 FAIL
[mbt] all host tests passed
`},
		// a failed test is a failure, with or without assertions
		{"failed", map[string]row{
			"TSTA":   {true, "rc=0", 2, 0},
			"TSTBAD": {false, "rc=8", 0, 0},
			"TSTCC":  {false, "COMPILE", 0, 0},
		}, false, head + `  TSTA       ok  rc=0     2 pass / 0 fail
  TSTBAD     FAIL rc=8    0 pass / 0 fail
  TSTCC      FAIL COMPILE 0 pass / 0 fail

  3 host test(s) | assertions: 2 PASS, 0 FAIL
[mbt] 2 host test(s) FAILED
`},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if ok := report(&b, c.rows, nil); ok != c.ok || b.String() != c.want {
			t.Errorf("%s: ok=%v\n%s\nwant ok=%v\n%s", c.name, ok, b.String(), c.ok, c.want)
		}
	}
}
