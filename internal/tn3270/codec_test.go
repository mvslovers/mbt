package tn3270

import (
	"bytes"
	"testing"
)

// ebc encodes test text; it fails the test on a character CP037 lacks.
func ebc(t *testing.T, s string) []byte {
	t.Helper()
	var out []byte
	for _, r := range s {
		b, ok := toEBCDIC(r)
		if !ok {
			t.Fatalf("no CP037 code for %q", r)
		}
		out = append(out, b)
	}
	return out
}

func TestAddressRoundTrip12Bit(t *testing.T) {
	for a := 0; a < 4096; a++ {
		b1, b2 := encodeAddr(a)
		if b1&0xC0 == 0 {
			t.Fatalf("address %d: 12-bit form %02X %02X looks like 14-bit", a, b1, b2)
		}
		if got := decodeAddr(b1, b2); got != a {
			t.Fatalf("address %d: encoded %02X %02X, decoded %d", a, b1, b2, got)
		}
	}
}

func TestAddressRoundTrip14Bit(t *testing.T) {
	for a := 4096; a < 16384; a++ {
		b1, b2 := encodeAddr(a)
		if got := decodeAddr(b1, b2); got != a {
			t.Fatalf("address %d: encoded %02X %02X, decoded %d", a, b1, b2, got)
		}
	}
	// a 14-bit address below 4096 is legal input too
	if got := decodeAddr(0x07, 0x7F); got != 1919 {
		t.Fatalf("14-bit 07 7F = %d, want 1919", got)
	}
}

func TestAddressKnownValues(t *testing.T) {
	// values seen in host records from MVS/CE
	for _, c := range []struct {
		a      int
		b1, b2 byte
	}{
		{0, 0x40, 0x40},
		{80, 0xC1, 0x50},   // row 2
		{1919, 0x5D, 0x7F}, // the last position
		{1840, 0x5C, 0xF0}, // row 24
	} {
		b1, b2 := encodeAddr(c.a)
		if b1 != c.b1 || b2 != c.b2 {
			t.Errorf("encode %d = %02X %02X, want %02X %02X", c.a, b1, b2, c.b1, c.b2)
		}
		if got := decodeAddr(c.b1, c.b2); got != c.a {
			t.Errorf("decode %02X %02X = %d, want %d", c.b1, c.b2, got, c.a)
		}
	}
}

func TestCP037(t *testing.T) {
	for _, c := range []struct {
		r rune
		e byte
	}{
		{' ', 0x40}, {'A', 0xC1}, {'J', 0xD1}, {'S', 0xE2}, {'Z', 0xE9},
		{'a', 0x81}, {'z', 0xA9}, {'0', 0xF0}, {'9', 0xF9},
		{'.', 0x4B}, {'-', 0x60}, {'=', 0x7E}, {'>', 0x6E}, {'*', 0x5C},
		{'[', 0xBA}, {']', 0xBB}, {'|', 0x4F}, {'^', 0xB0},
		{'¢', 0x4A}, {'¬', 0x5F}, {'\\', 0xE0}, {'{', 0xC0}, {'}', 0xD0},
	} {
		b, ok := toEBCDIC(c.r)
		if !ok || b != c.e {
			t.Errorf("toEBCDIC(%q) = %02X %v, want %02X", c.r, b, ok, c.e)
		}
	}
	// a bijection: every byte comes back
	for e := 0; e < 256; e++ {
		if b, ok := toEBCDIC(cp037[e]); !ok || int(b) != e {
			t.Fatalf("round trip of %02X gave %02X", e, b)
		}
	}
	if _, ok := toEBCDIC('€'); ok {
		t.Error("toEBCDIC('€') succeeded; CP037 has no euro sign")
	}
	if r := displayRune(0x00); r != ' ' {
		t.Errorf("null displays as %q", r)
	}
	if r := displayRune(0x15); r != ' ' { // NL
		t.Errorf("NL displays as %q", r)
	}
	if !bytes.Equal(ebc(t, "TIME-"), []byte{0xE3, 0xC9, 0xD4, 0xC5, 0x60}) {
		t.Error("TIME- encodes wrongly")
	}
}

func TestKeys(t *testing.T) {
	want := []byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8, 0xF9, 0x7A, 0x7B, 0x7C,
		0xC1, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9, 0x4A, 0x4B, 0x4C}
	for n := 1; n <= 24; n++ {
		k, err := PF(n)
		if err != nil || byte(k) != want[n-1] {
			t.Errorf("PF(%d) = %02X, %v; want %02X", n, byte(k), err, want[n-1])
		}
		if !k.valid() || k.short() {
			t.Errorf("PF%d: valid %v short %v", n, k.valid(), k.short())
		}
	}
	if _, err := PF(25); err == nil {
		t.Error("PF(25) succeeded")
	}
	for _, k := range []Key{Clear, PA1, PA2, PA3} {
		if !k.short() {
			t.Errorf("%v does not send a short read", k)
		}
	}
	if Enter.short() || byte(Enter) != 0x7D || byte(Clear) != 0x6D || byte(PA1) != 0x6C ||
		byte(PA2) != 0x6E || byte(PA3) != 0x6B {
		t.Error("ENTER/CLEAR/PA AIDs are wrong")
	}
	if Key(0x60).valid() || Key(0x88).valid() {
		t.Error("no-AID and query-reply AIDs count as keys")
	}
	if PF12.String() != "PF12" || Key(0xC9).String() != "PF21" || Clear.String() != "CLEAR" {
		t.Errorf("names: %v %v %v", PF12, Key(0xC9), Clear)
	}
}
