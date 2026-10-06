package tn3270

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

// step is one line of a recorded conversation (testdata/*.txt).
type step struct {
	kind byte   // 'S' host bytes, 'C' client telnet command, 'A' client record
	data []byte // raw bytes as on the wire
}

func loadScript(t *testing.T, path string) []step {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var steps []step
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		kind, hx, ok := strings.Cut(line, " ")
		if !ok || len(kind) != 1 {
			t.Fatalf("bad script line %q", line)
		}
		b, err := hex.DecodeString(hx)
		if err != nil {
			t.Fatalf("bad hex in %q: %v", line, err)
		}
		steps = append(steps, step{kind[0], b})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return steps
}

// hostRecords returns the 3270 records the host sent, telnet framing
// removed (IAC IAC undoubled, IAC EOR dropped).
func hostRecords(steps []step) [][]byte {
	var recs [][]byte
	for _, s := range steps {
		if s.kind != 'S' || len(s.data) < 2 || s.data[0] == tnIAC {
			continue
		}
		raw := bytes.TrimSuffix(s.data, []byte{tnIAC, tnEOR})
		recs = append(recs, bytes.ReplaceAll(raw, []byte{tnIAC, tnIAC}, []byte{tnIAC}))
	}
	return recs
}

func TestRecordedHostRecords(t *testing.T) {
	recs := hostRecords(loadScript(t, "testdata/mvsce-logon.txt"))
	if len(recs) < 20 {
		t.Fatalf("only %d host records in the recording", len(recs))
	}
	s := newScreen()
	var sawLogon, sawPassword, sawWelcome, sawReady, sawTime, sawLoggedOff bool
	for i, rec := range recs {
		res, err := s.process(rec)
		if err != nil {
			t.Fatalf("record %d (% X): %v", i, rec[:min(len(rec), 16)], err)
		}
		txt := strings.Join(s.lines(), "\n")
		sawLogon = sawLogon || strings.Contains(txt, "TSO Logon ===>")
		sawPassword = sawPassword || strings.Contains(txt, "ENTER CURRENT PASSWORD FOR TSTUSR0-")
		// the spike lost characters here: it left a stale field attribute
		// where a later write put a character
		if strings.Contains(txt, "WELCOME TO MVS COMMUNITY EDITION") &&
			strings.Contains(txt, strings.Repeat("*", 79)) {
			sawWelcome = true
		}
		sawReady = sawReady || hasLine(txt, "READY")
		sawTime = sawTime || strings.Contains(txt, "TIME-")
		sawLoggedOff = sawLoggedOff || strings.Contains(txt, "TSTUSR0 LOGGED OFF TSO AT")
		if rec[0] == cmdWSF && !res.query {
			t.Errorf("record %d: WSF Read Partition Query not recognised", i)
		}
	}
	for name, ok := range map[string]bool{"VTAM logon": sawLogon, "password prompt": sawPassword,
		"intact broadcast": sawWelcome, "READY": sawReady, "TIME": sawTime, "LOGGED OFF": sawLoggedOff} {
		if !ok {
			t.Errorf("never saw %s", name)
		}
	}
}

// Every prefix of every recorded record must parse without a panic; a cut
// inside an order is reported as errTruncated.
func TestTruncatedRecords(t *testing.T) {
	recs := hostRecords(loadScript(t, "testdata/mvsce-logon.txt"))
	recs = append(recs,
		[]byte{cmdW, 0xC3, orderSFE, 0x02, 0xC0, 0x60, 0x41, 0xF2},
		[]byte{cmdW, 0xC3, orderRA, 0x40, 0x40, orderGE, 0xAD},
		[]byte{cmdW, 0xC3, orderMF, 0x01, 0xC0, 0x60},
		[]byte{cmdWSF, 0x00, 0x00, sfOutbound3270, 0x00, cmdW, 0xC3, orderSBA, 0x40, 0x40},
	)
	truncated := 0
	for _, rec := range recs {
		for n := 1; n <= len(rec); n++ {
			_, err := newScreen().process(rec[:n])
			if errors.Is(err, errTruncated) {
				truncated++
			} else if err != nil {
				t.Fatalf("% X: %v", rec[:n], err)
			}
		}
	}
	if truncated == 0 {
		t.Error("no prefix was reported truncated")
	}
}

// formScreen: an unprotected input field at 0 (attribute 0x40) running to a
// protected field at 40, the cursor in the input field.
func formScreen(t *testing.T) *screen {
	t.Helper()
	s := newScreen()
	a1, a2 := encodeAddr(40)
	rec := []byte{cmdEW, 0xC3, orderSF, 0x40, orderIC, orderSBA, a1, a2, orderSF, 0x60}
	rec = append(rec, ebc(t, "LABEL")...)
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResetMDT(t *testing.T) {
	s := formScreen(t)
	if err := s.typeText("TIME", false); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x7D, 0x40, 0xC5, orderSBA, 0x40, 0xC1}, ebc(t, "TIME")...)
	if got, _ := s.readModified(0x7D, false); !bytes.Equal(got, want) {
		t.Fatalf("read modified\n got % X\nwant % X", got, want)
	}
	// a Write without reset-MDT keeps the field modified
	if _, err := s.process([]byte{cmdW, 0xC2}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.readModified(0x7D, false); !bytes.Equal(got, want) {
		t.Fatalf("after WCC C2\n got % X\nwant % X", got, want)
	}
	// reset-MDT: the field is still on the screen, but no longer typed
	if _, err := s.process([]byte{cmdW, 0xC3}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.readModified(0x7D, false); !bytes.Equal(got, []byte{0x7D, 0x40, 0xC5}) {
		t.Fatalf("after WCC C3 got % X, want 7D 40 C5", got)
	}
	if !strings.HasPrefix(s.lines()[0], " TIME") {
		t.Errorf("screen row 1 = %q", s.lines()[0])
	}
}

func TestReadModifiedUnformatted(t *testing.T) {
	s := newScreen()
	if _, err := s.process([]byte{cmdEW, 0xC3}); err != nil {
		t.Fatal(err)
	}
	if err := s.typeText("TIME", false); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x7D, 0x40, 0xC4}, ebc(t, "TIME")...)
	if got, _ := s.readModified(0x7D, false); !bytes.Equal(got, want) {
		t.Fatalf("got % X, want % X", got, want)
	}
	if got, _ := s.readModified(byte(Clear), true); !bytes.Equal(got, []byte{0x6D}) {
		t.Fatalf("short read = % X, want 6D", got)
	}
}

func TestReadBuffer(t *testing.T) {
	s := formScreen(t)
	got, _ := s.readBuffer(aidNone)
	if len(got) != 3+bufSize+2 { // two attributes, each SF + byte
		t.Fatalf("read buffer is %d bytes, want %d", len(got), 3+bufSize+2)
	}
	if !bytes.Equal(got[:5], []byte{aidNone, 0x40, 0xC1, orderSF, 0x40}) {
		t.Fatalf("read buffer starts % X", got[:5])
	}
}

func TestReadPartitionQuery(t *testing.T) {
	s := newScreen()
	res, err := s.process([]byte{cmdWSF, 0x00, 0x05, sfReadPartition, 0xFF, rpQuery})
	if err != nil || !res.query {
		t.Fatalf("query not recognised: %+v %v", res, err)
	}
	// the reply the session sent to MVS/CE in the recording
	want, _ := hex.DecodeString("88000781808081A60017818101000050001800000A02E50002006F090C0780001181A600000B01000050001800500018")
	if got := queryReply(); !bytes.Equal(got, want) {
		t.Fatalf("query reply\n got % X\nwant % X", got, want)
	}
}

func TestOrders(t *testing.T) {
	s := newScreen()
	// RA to the current address fills the whole buffer
	if _, err := s.process([]byte{cmdEW, 0xC3, orderRA, 0x40, 0x40, 0x5C}); err != nil {
		t.Fatal(err)
	}
	for r, l := range s.lines() {
		if l != strings.Repeat("*", Cols) {
			t.Fatalf("RA: row %d = %q", r, l)
		}
	}
	// SFE with a 3270 attribute, then EUA nulls the unprotected field only
	a1, a2 := encodeAddr(10)
	e1, e2 := encodeAddr(20)
	rec := []byte{cmdW, 0xC3, orderSBA, 0x40, 0x40, orderSFE, 0x02, 0x41, 0xF2, 0xC0, 0x60, // protected
		orderSBA, a1, a2, orderSFE, 0x01, 0xC0, 0x40, // unprotected
		orderSBA, 0x40, 0xC1, orderEUA, e1, e2}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if got := s.lines()[0]; got != " *********          "+strings.Repeat("*", 60) {
		t.Fatalf("EUA: row 1 = %q", got)
	}
	// PT moves into the unprotected field; IC puts the cursor there; a
	// character overwrites an attribute
	rec = []byte{cmdW, 0xC2, orderSBA, 0x40, 0x40, orderPT, orderIC}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if s.cursor != 11 {
		t.Fatalf("PT/IC: cursor %d, want 11", s.cursor)
	}
	rec = append([]byte{cmdW, 0xC2, orderSBA, a1, a2}, ebc(t, "X")...)
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if s.attr[10] >= 0 || !strings.HasPrefix(s.lines()[0], " *********X") {
		t.Fatalf("a character did not replace the attribute: %q", s.lines()[0])
	}
	// MF makes field 0 -- now the only field, so the whole screen --
	// non-display: every character vanishes
	rec = []byte{cmdW, 0xC2, orderSBA, 0x40, 0x40, orderMF, 0x01, 0xC0, 0x6C}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.lines(), ""); got != "" {
		t.Fatalf("MF: screen still shows %q", got)
	}
	// SA is skipped, GE stores the next byte
	rec = []byte{cmdEW, 0xC3, orderSA, 0x41, 0xF2, orderGE, 0xC1}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if s.lines()[0] != "A" {
		t.Fatalf("SA/GE: row 1 = %q", s.lines()[0])
	}
}

func TestEraseUnprotected(t *testing.T) {
	s := formScreen(t)
	if err := s.typeText("ABC", false); err != nil {
		t.Fatal(err)
	}
	res, err := s.process([]byte{cmdEAU})
	if err != nil || !res.restore {
		t.Fatalf("EAU: %+v %v", res, err)
	}
	if got := s.lines()[0]; got != strings.Repeat(" ", 41)+"LABEL" {
		t.Fatalf("EAU: row 1 = %q", got)
	}
	if s.cursor != 1 {
		t.Fatalf("EAU: cursor %d, want 1", s.cursor)
	}
	if got, _ := s.readModified(0x7D, false); len(got) != 3 {
		t.Fatalf("EAU left a modified field: % X", got)
	}
}

func TestHostReadCommands(t *testing.T) {
	for _, c := range []struct {
		rec       []byte
		mod, rbuf bool
	}{
		{[]byte{cmdRM}, true, false},
		{[]byte{snaRM}, true, false},
		{[]byte{cmdRMA}, true, false},
		{[]byte{cmdRB}, false, true},
		{[]byte{snaRB}, false, true},
		{[]byte{cmdWSF, 0x00, 0x05, sfReadPartition, 0x00, cmdRB}, false, true},
		{[]byte{cmdWSF, 0x00, 0x05, sfReadPartition, 0x00, cmdRM}, true, false},
	} {
		res, err := newScreen().process(c.rec)
		if err != nil || res.readMod != c.mod || res.readBuffer != c.rbuf {
			t.Errorf("% X: %+v %v", c.rec, res, err)
		}
	}
	if _, err := newScreen().process([]byte{0x99}); err == nil {
		t.Error("an unknown command was accepted")
	}
}
