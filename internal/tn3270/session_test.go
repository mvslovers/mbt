package tn3270

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// readUnit reads one telnet unit as it travels on the wire: an option
// command, a subnegotiation, or a record up to and including IAC EOR.
func readUnit(r *bufio.Reader) ([]byte, error) {
	var u []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return u, err
		}
		u = append(u, b)
		if b != tnIAC {
			continue
		}
		c, err := r.ReadByte()
		if err != nil {
			return u, err
		}
		u = append(u, c)
		switch {
		case c == tnEOR:
			return u, nil
		case len(u) == 2 && (c == tnDO || c == tnDONT || c == tnWILL || c == tnWONT):
			o, err := r.ReadByte()
			return append(u, o), err
		case len(u) == 2 && c == tnSB:
			for {
				x, err := r.ReadByte()
				if err != nil {
					return u, err
				}
				u = append(u, x)
				if n := len(u); n >= 4 && u[n-2] == tnIAC && x == tnSE {
					return u, nil
				}
			}
		}
	}
}

// serveScript plays the host side of a recorded conversation on a local
// listener: it sends the S steps and checks what the client sends against
// the C and A steps.  The result arrives on the channel once the client has
// closed the connection.
func serveScript(t *testing.T, steps []step) (string, <-chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		defer l.Close()
		c, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for i, st := range steps {
			if st.kind == 'S' {
				if _, err := c.Write(st.data); err != nil {
					done <- fmt.Errorf("step %d: %v", i, err)
					return
				}
				continue
			}
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			u, err := readUnit(r)
			if err != nil {
				done <- fmt.Errorf("step %d: waiting for % X: %v", i, st.data, err)
				return
			}
			ok := bytes.Equal(u, st.data)
			if st.kind == 'A' && len(st.data) == 1 { // the AID alone: the record carries input
				ok = len(u) > 0 && u[0] == st.data[0]
			}
			if !ok {
				done <- fmt.Errorf("step %d: client sent % X, script has % X", i, u, st.data)
				return
			}
		}
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
		if u, err := readUnit(r); err == nil || len(u) > 0 {
			done <- fmt.Errorf("client sent % X after the script ended", u)
			return
		}
		done <- nil
	}()
	return l.Addr().String(), done
}

func scriptResult(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fake host: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("fake host did not finish")
	}
}

// timeTwice runs TIME twice with no CLEAR in between.  The second ENTER
// must carry the second command alone: TSO resets the MDT of the first with
// its output, and a client that ignores that sends "TIME TIME" (in the
// recording, the A record after the second TIME has a single TIME).
func timeTwice(t *testing.T, s *Session, wait time.Duration) {
	t.Helper()
	for n := 1; n <= 2; n++ {
		if err := s.Type("TIME"); err != nil {
			t.Fatal(err)
		}
		if err := s.Key(Enter); err != nil {
			t.Fatal(err)
		}
		err := s.WaitScreen(func(txt string) bool {
			return strings.Count(txt, "TIME-") >= n && strings.Count(txt, "READY") >= n+1 // one READY came with the logon
		}, wait)
		if err != nil {
			t.Fatalf("TIME #%d: %v", n, err)
		}
		if txt := s.Text(); strings.Contains(txt, "INVALID") {
			t.Fatalf("TIME #%d:\n%s", n, txt)
		}
	}
}

var testOptions = Options{Settle: 50 * time.Millisecond, KeyTimeout: 10 * time.Second}

// The password used against the fake host, and its EBCDIC hex as the trace
// would print it.
const (
	fakePassword    = "SECRET"
	fakePasswordHex = "E2 C5 C3 D9 C5 E3"
)

// TestReplayLogon drives the recorded MVS/CE conversation: banner, VTAM,
// logon, password, broadcast, READY, TIME, TIME, CLEAR, LOGOFF.
func TestReplayLogon(t *testing.T) {
	addr, done := serveScript(t, loadScript(t, "testdata/mvsce-logon.txt"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var trace bytes.Buffer
	opt := testOptions
	opt.Trace = &trace
	s, err := Dial(ctx, addr, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Text(), "Hercules") {
		t.Fatalf("first screen:\n%s", s.Text())
	}
	if err := s.LogonTSO(ctx, "tstusr0", fakePassword); err != nil {
		t.Fatal(err)
	}
	if txt := s.Text(); !strings.Contains(txt, "WELCOME TO MVS COMMUNITY EDITION") || !hasLine(txt, "READY") {
		t.Fatalf("after logon:\n%s", txt)
	}
	timeTwice(t, s, 5*time.Second)
	if err := s.LogoffTSO(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	scriptResult(t, done)

	tr := trace.String()
	if strings.Contains(tr, fakePasswordHex) {
		t.Fatal("the trace shows the password")
	}
	for _, want := range []string{"telnet -> SB TTYPE IS IBM-3278-2-E", "telnet -> WILL EOR",
		"holds secret input, not shown", "-> 48 bytes: 88 00 07 81 80"} {
		if !strings.Contains(tr, want) {
			t.Errorf("trace lacks %q", want)
		}
	}
}

// Close logs off when the caller did not.
func TestReplayCloseLogsOff(t *testing.T) {
	addr, done := serveScript(t, loadScript(t, "testdata/mvsce-logon.txt"))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Dial(ctx, addr, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LogonTSO(ctx, "TSTUSR0", fakePassword); err != nil {
		t.Fatal(err)
	}
	timeTwice(t, s, 5*time.Second)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	scriptResult(t, done)
}

// lineWrite is a host record in the shape TSO line mode uses: protected
// output lines from the top, an input field after the last.
func lineWrite(t *testing.T, lines ...string) []byte {
	rec := []byte{cmdEW, 0xC3}
	for i, l := range lines {
		a1, a2 := encodeAddr(i * Cols)
		rec = append(rec, orderSBA, a1, a2, orderSF, 0xC8)
		rec = append(rec, ebc(t, l)...)
	}
	return append(rec, orderSF, 0x40, orderIC, tnIAC, tnEOR)
}

// IKJ56425I ends the logon with *UseridInUseError, and the client answers
// the IKJ56400A prompt that follows with LOGOFF rather than leaving the
// terminal inside an unfinished logon.  The two lines are what MVS/CE
// showed (measured); the record that carries them is built here, its bytes
// were not recorded.
func TestLogonUseridInUse(t *testing.T) {
	steps := loadScript(t, "testdata/mvsce-logon.txt")
	// up to and including the ENTER after the userid
	n := 0
	for i, st := range steps {
		if st.kind == 'A' && st.data[0] == 0x7D {
			n++
			if n == 2 {
				steps = steps[:i+1]
				break
			}
		}
	}
	prompt := "IKJ56400A ENTER LOGON OR LOGOFF-"
	steps = append(steps, step{'S', lineWrite(t, "IKJ56425I LOGON REJECTED, USERID TSTUSR0 IN USE", prompt)})
	// LOGOFF typed into the field after the prompt: row 2, after the
	// output attribute, the text and the input attribute
	in := Cols + 1 + len(prompt) + 1
	c1, c2 := encodeAddr(in + len("LOGOFF"))
	f1, f2 := encodeAddr(in)
	logoff := append([]byte{0x7D, c1, c2, orderSBA, f1, f2}, ebc(t, "LOGOFF")...)
	steps = append(steps,
		step{'A', append(logoff, tnIAC, tnEOR)},
		step{'S', []byte{cmdEW, 0xC3, tnIAC, tnEOR}})
	addr, done := serveScript(t, steps)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Dial(ctx, addr, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	err = s.LogonTSO(ctx, "TSTUSR0", fakePassword)
	var inUse *UseridInUseError
	if !errors.As(err, &inUse) || inUse.User != "TSTUSR0" || !strings.Contains(err.Error(), "C U=TSTUSR0") {
		t.Fatalf("got %v", err)
	}
	if err := s.Close(); err != nil { // not logged on: no LOGOFF
		t.Fatal(err)
	}
	scriptResult(t, done)
}

// The host's Read Modified and Read Buffer are answered with AID 60.
func TestHostRead(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	next := make(chan []byte)   // test -> host: send this record
	answer := make(chan []byte) // host -> test: what the client answered
	go func() {
		c, err := l.Accept()
		if err != nil {
			close(answer)
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		_, _ = c.Write([]byte{cmdEW, 0xC3, orderSF, 0x40, orderIC, tnIAC, tnEOR})
		for rec := range next {
			_, _ = c.Write(append(rec, tnIAC, tnEOR))
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			u, _ := readUnit(r)
			answer <- u
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Dial(ctx, l.Addr().String(), testOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Type("HI"); err != nil {
		t.Fatal(err)
	}
	ask := func(rec []byte) []byte {
		next <- rec
		if err := s.WaitUnlock(2 * time.Second); err != nil { // not locked: reads until quiet
			t.Fatal(err)
		}
		return <-answer
	}
	got := ask([]byte{cmdRM})
	if want := []byte{aidNone, 0x40, 0xC3, orderSBA, 0x40, 0xC1, 0xC8, 0xC9, tnIAC, tnEOR}; !bytes.Equal(got, want) {
		t.Fatalf("Read Modified answered % X, want % X", got, want)
	}
	got = ask([]byte{cmdRB})
	if len(got) != 3+bufSize+1+2 || !bytes.Equal(got[:7], []byte{aidNone, 0x40, 0xC3, orderSF, 0x41, 0xC8, 0xC9}) {
		t.Fatalf("Read Buffer answered %d bytes: % X ...", len(got), got[:min(len(got), 8)])
	}
	close(next)
}

func TestDialRejectsOtherModels(t *testing.T) {
	_, err := Dial(context.Background(), "127.0.0.1:1", Options{TerminalType: "IBM-3278-4-E"})
	if err == nil || !strings.Contains(err.Error(), "model 2") {
		t.Fatalf("got %v", err)
	}
}

// A record that straddles a read deadline is completed by the next read,
// not lost; option negotiation answers once and refuses TN3270E.
func TestTelnetFraming(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	cc, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	host, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	tn := newTelnet(cc, "IBM-3278-2-E", nil)

	write := func(b ...byte) {
		if _, err := host.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write(tnIAC, tnDO, optTN3270E, tnIAC, tnWILL, optTN3270E,
		tnIAC, tnDO, optTType, tnIAC, tnDO, optTType,
		tnIAC, tnSB, optTType, ttypeSEND, tnIAC, tnSE,
		cmdEW, 0xC3, 0xC1, tnIAC)
	if _, err := tn.readRecord(time.Now().Add(100 * time.Millisecond)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("half a record: %v", err)
	}
	if !tn.pending() {
		t.Fatal("half a record is not pending")
	}
	write(tnIAC, 0xC2, tnIAC, tnEOR) // IAC IAC: a data byte FF
	rec, err := tn.readRecord(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{cmdEW, 0xC3, 0xC1, 0xFF, 0xC2}; !bytes.Equal(rec, want) {
		t.Fatalf("record % X, want % X", rec, want)
	}
	if tn.pending() {
		t.Fatal("still pending after a complete record")
	}

	want := []byte{tnIAC, tnWONT, optTN3270E, tnIAC, tnDONT, optTN3270E, tnIAC, tnWILL, optTType,
		tnIAC, tnSB, optTType, ttypeIS}
	want = append(want, "IBM-3278-2-E"...)
	want = append(want, tnIAC, tnSE)
	got := make([]byte, len(want)+16)
	_ = host.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := readAtLeast(host, got, len(want))
	if !bytes.Equal(got[:n], want) {
		t.Fatalf("client answered\n% X\nwant\n% X", got[:n], want)
	}

	// writeRecord doubles IAC and appends IAC EOR
	if err := tn.writeRecord([]byte{0x7D, 0xFF, 0x40}); err != nil {
		t.Fatal(err)
	}
	n, _ = readAtLeast(host, got, 6)
	if want := []byte{0x7D, 0xFF, 0xFF, 0x40, tnIAC, tnEOR}; !bytes.Equal(got[:n], want) {
		t.Fatalf("writeRecord sent % X, want % X", got[:n], want)
	}
}

// readAtLeast reads until n bytes are in buf, then once more briefly to
// catch anything extra.
func readAtLeast(c net.Conn, buf []byte, n int) (int, error) {
	got := 0
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	for got < n {
		k, err := c.Read(buf[got:])
		got += k
		if err != nil {
			return got, err
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	k, _ := c.Read(buf[got:])
	return got + k, nil
}
