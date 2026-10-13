package tn3270

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// COLSxROWS, as x3270's -oversize: the model sizes, oversize screens, and
// what is refused.
func TestParseSize(t *testing.T) {
	for in, want := range map[string]struct {
		z     Size
		model int
		tt    string
		over  bool
	}{
		"80x24":   {Size{80, 24}, 2, "IBM-3278-2-E", false},
		"80x32":   {Size{80, 32}, 3, "IBM-3278-3-E", false},
		"80X43":   {Size{80, 43}, 4, "IBM-3278-4-E", false},
		"132x27":  {Size{132, 27}, 5, "IBM-3278-5-E", false},
		"160x62":  {Size{160, 62}, 5, "IBM-3278-5-E", true}, // covers 132x27
		"100x50":  {Size{100, 50}, 4, "IBM-3278-4-E", true}, // 80x43, not 132 wide
		"80x30":   {Size{80, 30}, 2, "IBM-3278-2-E", true},
		" 81x24 ": {Size{81, 24}, 2, "IBM-3278-2-E", true},
	} {
		z, err := ParseSize(in)
		if err != nil || z != want.z || z.Model() != want.model || z.TerminalType() != want.tt || z.Oversize() != want.over {
			t.Errorf("%q: %v %v model %d %s oversize %v", in, z, err, z.Model(), z.TerminalType(), z.Oversize())
		}
	}
	for _, in := range []string{"", "43x80x", "24", "x", "79x24", "80x23", "200x82", "abcx24", "80*24"} {
		if _, err := ParseSize(in); err == nil || !strings.Contains(err.Error(), "COLSxROWS") {
			t.Errorf("%q: got %v", in, err)
		}
	}
	if modelOf("IBM-3279-4-E") != 4 || modelOf("IBM-3278-2") != 2 || modelOf("IBM-3278-6-E") != 0 || modelOf("IBM-DYNAMIC") != 0 {
		t.Error("modelOf")
	}
}

// Erase/Write Alternate switches to the alternate size, Erase/Write and the
// Erase/Reset structured field without its flag back to 24x80.
func TestAlternateSize(t *testing.T) {
	s := newScreenSize(Size{80, 43})
	if s.rows != Rows || s.cols != Cols {
		t.Fatalf("starts at %dx%d", s.cols, s.rows)
	}
	a1, a2 := encodeAddr(42 * 80)
	for _, cmd := range []byte{cmdEWA, snaEWA} {
		if _, err := s.process(append([]byte{cmd, 0xC3, orderSBA, a1, a2}, ebc(t, "LAST ROW")...)); err != nil {
			t.Fatal(err)
		}
		l := s.lines()
		if len(l) != 43 || l[42] != "LAST ROW" {
			t.Fatalf("%02X: %d rows, last %q", cmd, len(l), l[len(l)-1])
		}
	}
	for _, cmd := range []byte{cmdEW, snaEW} {
		s.reset(true)
		if _, err := s.process([]byte{cmd, 0xC3}); err != nil || len(s.lines()) != 24 {
			t.Fatalf("%02X: %d rows, %v", cmd, len(s.lines()), err)
		}
	}
	// Erase/Reset: X'80' the alternate size, 0 the default
	if _, err := s.process([]byte{cmdWSF, 0x00, 0x04, sfEraseReset, 0x80}); err != nil || s.rows != 43 {
		t.Fatalf("erase/reset alternate: %d rows, %v", s.rows, err)
	}
	if _, err := s.process([]byte{cmdWSF, 0x00, 0x04, sfEraseReset, 0x00}); err != nil || s.rows != 24 {
		t.Fatalf("erase/reset default: %d rows, %v", s.rows, err)
	}
	// a model 2 has no other alternate size
	m2 := newScreen()
	if _, err := m2.process([]byte{cmdEWA, 0xC3}); err != nil || len(m2.lines()) != 24 || len(m2.lines()[0]) != 0 {
		t.Fatalf("model 2 EWA: %v", err)
	}
}

// 62 rows of 160 columns: 9920 positions, addresses past 4095 are 14-bit
// both ways, and RA/EUA wrap at the larger buffer.
func TestOversizeScreen(t *testing.T) {
	s := newScreenSize(Size{160, 62})
	in := 9000 // row 56, column 40
	b1, b2 := byte(in>>8), byte(in&0xFF)
	rec := []byte{cmdEWA, 0xC3, orderSBA, b1, b2, orderSF, 0x40, orderIC}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if s.cursor != in+1 || s.size() != 9920 {
		t.Fatalf("cursor %d, size %d", s.cursor, s.size())
	}
	if err := s.typeText("HI", false); err != nil {
		t.Fatal(err)
	}
	l := s.lines()
	if len(l) != 62 || l[56] != strings.Repeat(" ", 41)+"HI" {
		t.Fatalf("%d rows, row 57 %q", len(l), l[56])
	}
	// every inbound address is 14-bit on a buffer larger than 4096
	got, _ := s.readModified(byte(Enter), false)
	c := in + 3
	want := append([]byte{byte(Enter), byte(c >> 8), byte(c), orderSBA, byte((in + 1) >> 8), byte(in + 1)}, ebc(t, "HI")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("read modified\n got % X\nwant % X", got, want)
	}
	if rb, _ := s.readBuffer(aidNone); len(rb) != 3+9920+1 || rb[1] != byte(c>>8) || rb[2] != byte(c) {
		t.Fatalf("read buffer %d bytes, cursor % X", len(rb), rb[1:3])
	}
	// RA to its own address fills the whole 9920 positions
	if _, err := s.process([]byte{cmdEWA, 0xC3, orderRA, 0x00, 0x00, 0x5C}); err != nil {
		t.Fatal(err)
	}
	for r, l := range s.lines() {
		if l != strings.Repeat("*", 160) {
			t.Fatalf("RA: row %d = %q", r, l)
		}
	}
	// a 43x80 buffer (3440 positions) still answers with 12-bit addresses
	m4 := newScreenSize(Size{80, 43})
	m4.reset(true)
	m4.cursor = 3000
	if got, _ := m4.readModified(byte(Enter), false); got[1]&0xC0 == 0 {
		t.Fatalf("model 4 cursor address % X is 14-bit", got[1:3])
	}
}

// The query reply announces the alternate size in Usable Area and as the
// Implicit Partition's alternate; the default stays 24x80.
func TestQueryReplySizes(t *testing.T) {
	m4 := queryReply(Size{80, 43})
	// Usable Area: width 80, height 43, buffer 3440
	if !bytes.Contains(m4, []byte{0x81, 0x81, 0x01, 0x00, 0x00, 80, 0x00, 43}) || !bytes.Contains(m4, []byte{0x09, 0x0C, 0x0D, 0x70}) {
		t.Fatalf("model 4 usable area: % X", m4)
	}
	if !bytes.HasSuffix(m4, []byte{0x0B, 0x01, 0x00, 0x00, 80, 0x00, 24, 0x00, 80, 0x00, 43}) {
		t.Fatalf("model 4 implicit partition: % X", m4)
	}
	ov := queryReply(Size{160, 62})
	if !bytes.Contains(ov, []byte{0x81, 0x81, 0x01, 0x00, 0x00, 160, 0x00, 62}) || !bytes.Contains(ov, []byte{0x09, 0x0C, 0x26, 0xC0}) {
		t.Fatalf("oversize usable area: % X", ov)
	}
	if !bytes.HasSuffix(ov, []byte{0x00, 80, 0x00, 24, 0x00, 160, 0x00, 62}) {
		t.Fatalf("oversize implicit partition: % X", ov)
	}
	if len(m4) != len(queryReply(Size{80, 24})) {
		t.Fatal("the reply's length depends on the size")
	}
}

// A model 4 session end to end against a fake host: the terminal type it
// negotiates, the query reply it sends, the 43 rows after Erase/Write
// Alternate, and CLEAR going back to 24x80.
func TestModel4Session(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan [2][]byte, 1)
	ready := ebc(t, "READY")
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		_, _ = c.Write([]byte{tnIAC, tnDO, optTType})
		_, _ = readUnit(r) // WILL TTYPE
		_, _ = c.Write([]byte{tnIAC, tnSB, optTType, ttypeSEND, tnIAC, tnSE})
		ttype, _ := readUnit(r)
		// partition id X'FF', doubled on the wire
		_, _ = c.Write([]byte{cmdWSF, 0x00, 0x05, sfReadPartition, tnIAC, tnIAC, rpQuery, tnIAC, tnEOR})
		reply, _ := readUnit(r)
		got <- [2][]byte{ttype, reply}
		a1, a2 := encodeAddr(42 * 80)
		rec := append([]byte{cmdEWA, 0xC3, orderSBA, a1, a2, orderSF, 0x40}, ready...)
		_, _ = c.Write(append(append(rec, orderSF, 0xC1, orderIC), tnIAC, tnEOR))
		_, _ = readUnit(r) // CLEAR
		_, _ = c.Write([]byte{cmdEW, 0xC3, tnIAC, tnEOR})
		_, _ = readUnit(r)
	}()
	opt := testOptions
	opt.Size = Size{80, 43}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Dial(ctx, l.Addr().String(), opt)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	neg := <-got
	if !bytes.Contains(neg[0], []byte("IBM-3278-4-E")) {
		t.Fatalf("terminal type % X", neg[0])
	}
	if want := append(queryReply(Size{80, 43}), tnIAC, tnEOR); !bytes.Equal(neg[1], want) {
		t.Fatalf("query reply\n got % X\nwant % X", neg[1], want)
	}
	if err := s.Expect("READY", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if sc := s.Screen(); len(sc) != 43 || sc[42] != " READY" || s.Size() != (Size{80, 43}) {
		t.Fatalf("%d rows, last %q, size %v", len(sc), sc[len(sc)-1], s.Size())
	}
	if r, c := s.Cursor(); r != 42 || c != 7 {
		t.Fatalf("cursor %d,%d", r, c)
	}
	if err := s.SetCursor(42, 79); err != nil {
		t.Fatal(err)
	}
	if err := s.SendKey(Clear); err != nil {
		t.Fatal(err)
	}
	if s.Size() != (Size{80, 24}) {
		t.Fatalf("after CLEAR: %v", s.Size())
	}
}
