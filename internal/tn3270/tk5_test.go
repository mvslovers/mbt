package tn3270

import (
	"context"
	"encoding/hex"
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
	i := tk5Banner(t, steps)
	out := append([]step{}, steps[:i+1]...)
	out = append(out, step{kind: 'W', wait: late})
	return append(out, steps[i+1:]...)
}

// tk5Banner is the index of the Hercules banner: the first host record, an
// Erase/Write with WCC 42.  TK5's logon screen follows it, then the userid.
func tk5Banner(t *testing.T, steps []step) int {
	t.Helper()
	for i, st := range steps {
		if st.kind == 'S' && len(st.data) > 2 && st.data[0] == 0xF5 && st.data[1] == 0x42 {
			if i+2 >= len(steps) || steps[i+1].kind != 'S' || steps[i+2].kind != 'A' {
				t.Fatal("testdata/tk5-logon.txt: not banner, logon screen, userid")
			}
			return i
		}
	}
	t.Fatal("no banner in testdata/tk5-logon.txt")
	return 0
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
	if strings.Contains(tr, "logon: the host stayed quiet") {
		t.Error("trace says the host stayed quiet, but TK5's screen came within the wait")
	}
}

// After LOGOFF, VTAM writes its logon screen back to the terminal.  Closing
// before that write turns it into an I/O error on the terminal (IEA000I
// 0C0,IOE), and the next logon there gets no logon screen until it sends
// input (mbt#281).  So LogoffTSO waits for the host's next write.
func TestReplayTK5LogoffWaitsForScreen(t *testing.T) {
	steps := tk5Steps(t, 0)
	uss := steps[tk5Banner(t, steps)+1] // TK5's logon screen
	steps = append(steps, step{kind: 'W', wait: 2 * testOptions.Settle}, uss)
	var trace strings.Builder
	opt := testOptions
	opt.Trace = &trace
	addr, done := serveScript(t, steps)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Dial(ctx, addr, opt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LogonTSO(ctx, "herc01", fakePassword); err != nil {
		t.Fatal(err)
	}
	if err := s.LogoffTSO(); err != nil {
		t.Fatal(err)
	}
	if txt := s.Text(); !strings.Contains(txt, "Logon ===>") {
		t.Errorf("LogoffTSO returned before VTAM's logon screen came; the screen:\n%s", txt)
	}
	if !strings.Contains(trace.String(), "logoff: the host wrote again") {
		t.Error("trace lacks the logoff's wait for the host")
	}
	_ = s.Close()
	scriptResult(t, done)
}

// A terminal left needing input: the banner, then silence; our ENTER wakes
// VTAM, which sends its logon screen and then rejects the empty input
// (INPUT NOT RECOGNIZED, recorded on the TK5 stand).  LogonTSO must type the
// userid there instead of giving up (mbt#281).
func TestReplayTK5InputNotRecognized(t *testing.T) {
	steps := tk5Steps(t, 0)
	b := tk5Banner(t, steps)
	enter, _ := hex.DecodeString("7D4040FFEF")
	inr, _ := hex.DecodeString("F5C31140401311C36F1D60C9D5D7E4E340D5D6E340D9C5C3D6C7D5C9E9C5C43CC5E8001D40FFEF")
	herc01 := []byte{0xC8, 0xC5, 0xD9, 0xC3, 0xF0, 0xF1}
	out := append([]step{}, steps[:b+1]...)
	out = append(out, step{kind: 'A', data: enter}, steps[b+1], step{kind: 'S', data: inr}, step{kind: 'H', data: herc01})
	out = append(out, steps[b+3:]...)
	tk5Logon(t, out, testOptions)
}
