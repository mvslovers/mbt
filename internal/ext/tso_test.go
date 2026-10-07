package ext

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeTSO struct {
	log      []string
	screen   []string
	loggedOn bool
}

func (f *fakeTSO) Logon() error {
	f.log = append(f.log, "logon")
	f.loggedOn = true
	f.screen = []string{"READY"}
	return nil
}
func (f *fakeTSO) Logoff() error {
	f.log = append(f.log, "logoff")
	f.loggedOn = false
	return nil
}
func (f *fakeTSO) Type(s string) error { f.log = append(f.log, "type "+s); return nil }
func (f *fakeTSO) Key(k string) error {
	f.log = append(f.log, "key "+k)
	if k == "enter" {
		f.screen = append(f.screen, "HELLO FROM REXX", "READY")
	}
	return nil
}
func (f *fakeTSO) Expect(text string, _ time.Duration) error {
	f.log = append(f.log, "expect "+text)
	if strings.Contains(strings.Join(f.screen, "\n"), text) {
		return nil
	}
	return errors.New("not there")
}
func (f *fakeTSO) Screen() []string   { return f.screen }
func (f *fakeTSO) Cursor() (int, int) { return 3, 1 }

func TestTSOTest(t *testing.T) {
	v := setup(t, map[string]string{
		"test/tso/rexxsay.lua": `
return function(t)
  t:logon()
  t:type("CALL '" .. t.testlib .. "(TSTSAY)'"):enter()
  t:expect("HELLO FROM REXX", { timeout = 3 }):expect("READY")
  local r, c = t:cursor()
  assert(r == 3 and c == 1 and #t:screen() == 3)
  t:pf(3)
end`,
		"test/tso/broken.lua": `
return function(t)
  t:logon()
  t:expect("NEVER")
end`,
		"test/tso/notafunc.lua": `return 42`,
	})
	tests, err := TSOTests(v.root)
	if err != nil || len(tests) != 3 || tests[0][0] != "BROKEN" || tests[2][1] != "test/tso/rexxsay.lua" {
		t.Fatalf("%v %v", tests, err)
	}
	e := v.mustLoad(t)
	f := &fakeTSO{}
	if err := e.TSOTest("test/tso/rexxsay.lua", f, map[string]any{"testlib": "IBMUSER.X.TESTLIB"}); err != nil {
		t.Fatal(err)
	}
	want := "logon|type CALL 'IBMUSER.X.TESTLIB(TSTSAY)'|key enter|expect HELLO FROM REXX|expect READY|key pf3|logoff"
	if strings.Join(f.log, "|") != want {
		t.Errorf("log:\n%s", strings.Join(f.log, "|"))
	}
	// a failing expect: FAIL with the screen, and still logged off
	f = &fakeTSO{}
	err = e.TSOTest("test/tso/broken.lua", f, nil)
	if err == nil || !strings.Contains(err.Error(), `expected "NEVER"`) || !strings.Contains(err.Error(), "READY") || f.loggedOn {
		t.Errorf("%v loggedOn=%v", err, f.loggedOn)
	}
	if err := e.TSOTest("test/tso/notafunc.lua", &fakeTSO{}, nil); err == nil || !strings.Contains(err.Error(), "must return a function(t)") {
		t.Errorf("%v", err)
	}
}
