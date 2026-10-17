package tn3270

import (
	"context"
	"strings"
	"testing"
	"time"
)

// tk5Steps loads the recorded TK5 logon.  With late > 0 the host stays quiet
// that long between the Hercules banner and TK5's own logon screen, the way
// a slower host does now and then.
func tk5Steps(t *testing.T, late time.Duration) []step {
	t.Helper()
	steps := loadScript(t, "testdata/tk5-logon.txt")
	if late == 0 {
		return steps
	}
	for i, st := range steps {
		// the banner: the first host record, an Erase/Write with WCC 42
		if st.kind == 'S' && len(st.data) > 2 && st.data[0] == 0xF5 && st.data[1] == 0x42 {
			out := append([]step{}, steps[:i+1]...)
			out = append(out, step{kind: 'W', wait: late})
			return append(out, steps[i+1:]...)
		}
	}
	t.Fatal("no banner in testdata/tk5-logon.txt")
	return nil
}

func tk5Logon(t *testing.T, steps []step, opt Options) {
	t.Helper()
	addr, done := serveScript(t, steps)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Dial(ctx, addr, opt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LogonTSO(ctx, "herc01", fakePassword); err != nil {
		// the fake host usually knows better: what the client sent instead
		select {
		case herr := <-done:
			t.Fatalf("%v\nfake host: %v", err, herr)
		case <-time.After(2 * time.Second):
			t.Fatal(err)
		}
	}
	if txt := s.Text(); !hasLine(txt, "READY") || isISPFPrimary(txt) {
		t.Fatalf("after logon:\n%s", txt)
	}
	if err := s.Close(); err != nil { // logs off: CLEAR, LOGOFF, LOGGED OFF
		t.Fatal(err)
	}
	scriptResult(t, done)
}

// The recorded TK5 logon: TK5's screen follows the banner at once, the logon
// proc starts ISPF and =X leaves it for READY (mbt#272), Close logs off.
func TestReplayTK5Logon(t *testing.T) { tk5Logon(t, tk5Steps(t, 0), testOptions) }

// TK5's logon screen arrives later than Settle after the Hercules banner
// (mbt#276): LogonTSO has to wait for it, not press ENTER into the gap,
// which TK5 answers with INPUT NOT RECOGNIZED.
func TestReplayTK5LateLogonScreen(t *testing.T) {
	var trace strings.Builder
	opt := testOptions
	opt.Trace = &trace
	tk5Logon(t, tk5Steps(t, 4*testOptions.Settle), opt)
	// the trace says that LogonTSO waited and what the host did (mbt#279)
	tr := trace.String()
	for _, want := range []string{"logon: no logon prompt on the screen, waiting up to", "logon: the host wrote again"} {
		if !strings.Contains(tr, want) {
			t.Errorf("trace lacks %q", want)
		}
	}
	if strings.Contains(tr, "the host stayed quiet") {
		t.Error("trace says the host stayed quiet, but TK5's screen came within the wait")
	}
}
