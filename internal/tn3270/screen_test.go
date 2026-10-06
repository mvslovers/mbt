package tn3270

import (
	"errors"
	"strings"
	"testing"
)

func TestTypeProtected(t *testing.T) {
	s := formScreen(t)
	s.cursor = 42 // inside LABEL
	if err := s.typeText("X", false); !errors.Is(err, ErrProtected) {
		t.Fatalf("typing into a protected field: %v", err)
	}
	s.cursor = 40 // on the attribute
	if err := s.typeText("X", false); !errors.Is(err, ErrProtected) {
		t.Fatalf("typing onto an attribute: %v", err)
	}
	s.cursor = 1
	if err := s.typeText("€", false); err == nil {
		t.Fatal("typing a character CP037 lacks succeeded")
	}
}

// Filling a field skips to the next unprotected one.
func TestTypeAutoSkip(t *testing.T) {
	s := newScreen()
	a1, a2 := encodeAddr(3)
	b1, b2 := encodeAddr(6)
	rec := []byte{cmdEW, 0xC3, orderSF, 0x40, orderIC, orderSBA, a1, a2, orderSF, 0x60, orderSBA, b1, b2, orderSF, 0x40}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.typeText("ABCD", false); err != nil {
		t.Fatal(err)
	}
	if got := s.lines()[0]; got != " AB    CD" {
		t.Fatalf("row 1 = %q", got)
	}
	got, _ := s.readModified(0x7D, false)
	want := append([]byte{0x7D, 0x40, 0xC9, orderSBA, 0x40, 0xC1}, ebc(t, "AB")...)
	want = append(want, orderSBA, 0x40, 0xC7)
	want = append(want, ebc(t, "CD")...)
	if string(got) != string(want) {
		t.Fatalf("read modified\n got % X\nwant % X", got, want)
	}
}

func TestTab(t *testing.T) {
	s := newScreen()
	a1, a2 := encodeAddr(3)
	b1, b2 := encodeAddr(6)
	rec := []byte{cmdEW, 0xC3, orderSF, 0x40, orderIC, orderSBA, a1, a2, orderSF, 0x60, orderSBA, b1, b2, orderSF, 0x40}
	if _, err := s.process(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.tab(); err != nil || s.cursor != 7 {
		t.Fatalf("tab: cursor %d, %v; want 7", s.cursor, err)
	}
	if err := s.tab(); err != nil || s.cursor != 1 {
		t.Fatalf("tab wrap: cursor %d, %v; want 1", s.cursor, err)
	}
}

// Secret input never shows, even in a displayable field, and the record
// carrying it says so.
func TestSecretInput(t *testing.T) {
	s := formScreen(t)
	if err := s.typeText("PASSWD", true); err != nil {
		t.Fatal(err)
	}
	if txt := strings.Join(s.lines(), "\n"); strings.Contains(txt, "PASSWD") || strings.Contains(txt, "P") {
		t.Fatalf("secret input on the screen: %q", s.lines()[0])
	}
	if _, secret := s.readModified(0x7D, false); !secret {
		t.Fatal("read modified with secret input not flagged")
	}
	if _, secret := s.readBuffer(aidNone); !secret {
		t.Fatal("read buffer with secret input not flagged")
	}
	// a host write over the positions clears the mark
	if _, err := s.process(append([]byte{cmdW, 0xC3, orderSBA, 0x40, 0xC1}, ebc(t, "OK....")...)); err != nil {
		t.Fatal(err)
	}
	if _, secret := s.readBuffer(aidNone); secret {
		t.Fatal("secret mark survived a host write")
	}
	if !strings.HasPrefix(s.lines()[0], " OK....") {
		t.Fatalf("row 1 = %q", s.lines()[0])
	}
}
