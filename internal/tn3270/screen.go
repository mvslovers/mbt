package tn3270

import (
	"errors"
	"fmt"
	"strings"
)

// Screen geometry: a model 2 terminal, 24 rows of 80 columns.  Nothing else
// is supported -- the query reply and the terminal type both say model 2.
const (
	Rows    = 24
	Cols    = 80
	bufSize = Rows * Cols
)

// Field attribute bits (the attribute byte as it travels in the data stream).
const (
	attrProtected  = 0x20
	attrDisplay    = 0x0C // 0x0C: non-display
	attrNonDisplay = 0x0C
	attrMDT        = 0x01 // modified data tag
)

// Errors from typing.
var (
	ErrProtected = errors.New("tn3270: cursor is in a protected field")
	ErrNoInput   = errors.New("tn3270: no unprotected field left to type into")
)

// screen is the presentation space: one byte per position, plus the field
// attribute where a field starts.  It knows nothing about the connection.
type screen struct {
	buf  [bufSize]byte  // EBCDIC; 0 = null
	attr [bufSize]int16 // -1: a character position; else the field attribute
	// secret marks positions filled by TypeSecret.  They never show in
	// lines() and the session does not trace a record while any is left.
	// A host write to the position, or an erase, clears the mark.
	secret [bufSize]bool
	cursor int
}

func newScreen() *screen {
	s := &screen{}
	s.erase()
	return s
}

// erase clears the buffer and the fields, as Erase/Write or CLEAR does.
func (s *screen) erase() {
	for i := range s.buf {
		s.buf[i] = 0
		s.attr[i] = -1
		s.secret[i] = false
	}
	s.cursor = 0
}

func (s *screen) setChar(pos int, b byte) {
	s.buf[pos] = b
	s.attr[pos] = -1
	s.secret[pos] = false
}

func (s *screen) setAttr(pos int, a byte) {
	s.buf[pos] = 0
	s.attr[pos] = int16(a)
	s.secret[pos] = false
}

func (s *screen) formatted() bool {
	for _, a := range s.attr {
		if a >= 0 {
			return true
		}
	}
	return false
}

// fieldOf returns the address of the attribute of the field holding pos, or
// -1 on an unformatted screen.
func (s *screen) fieldOf(pos int) int {
	for k := 0; k < bufSize; k++ {
		p := (pos - k + bufSize) % bufSize
		if s.attr[p] >= 0 {
			return p
		}
	}
	return -1
}

func (s *screen) protected(pos int) bool {
	f := s.fieldOf(pos)
	return f >= 0 && s.attr[f]&attrProtected != 0
}

// nextUnprotected returns the first character position of the next
// unprotected field, searching from pos itself (an attribute at pos counts).
// -1: there is none.
func (s *screen) nextUnprotected(pos int) int {
	for k := 0; k < bufSize; k++ {
		p := (pos + k) % bufSize
		if s.attr[p] >= 0 && s.attr[p]&attrProtected == 0 {
			next := (p + 1) % bufSize
			if s.attr[next] >= 0 { // an empty field: keep looking
				continue
			}
			return next
		}
	}
	return -1
}

// resetMDT clears every modified data tag: nothing counts as typed any more.
func (s *screen) resetMDT() {
	for i, a := range s.attr {
		if a >= 0 {
			s.attr[i] = a &^ attrMDT
		}
	}
}

// typeText puts text at the cursor as an operator would.  On a formatted
// screen the cursor must be in an unprotected field; reaching the end of a
// field skips to the next unprotected one (the auto-skip a real terminal
// does for a protected-numeric neighbour, applied always -- a test types
// into one field at a time).
func (s *screen) typeText(text string, secret bool) error {
	codes := make([]byte, 0, len(text))
	for _, r := range text {
		b, ok := toEBCDIC(r)
		if !ok {
			return fmt.Errorf("tn3270: %q has no CP037 code", r)
		}
		codes = append(codes, b)
	}
	if !s.formatted() {
		for _, b := range codes {
			s.buf[s.cursor] = b
			s.secret[s.cursor] = secret
			s.cursor = (s.cursor + 1) % bufSize
		}
		return nil
	}
	for i, b := range codes {
		if s.attr[s.cursor] >= 0 || s.protected(s.cursor) {
			if i == 0 {
				return ErrProtected
			}
			next := s.nextUnprotected(s.cursor)
			if next < 0 {
				return ErrNoInput
			}
			s.cursor = next
		}
		s.buf[s.cursor] = b
		s.secret[s.cursor] = secret
		if f := s.fieldOf(s.cursor); f >= 0 {
			s.attr[f] |= attrMDT
		}
		s.cursor = (s.cursor + 1) % bufSize
	}
	return nil
}

// tab moves the cursor to the next unprotected field, as the Tab key does.
func (s *screen) tab() error {
	if !s.formatted() {
		s.cursor = 0
		return nil
	}
	start := s.cursor
	if f := s.fieldOf(start); f >= 0 {
		start = (start + 1) % bufSize
	}
	// skip to the next attribute after the cursor, then find an input field
	for k := 0; k < bufSize; k++ {
		p := (start + k) % bufSize
		if s.attr[p] >= 0 {
			if next := s.nextUnprotected(p); next >= 0 {
				s.cursor = next
				return nil
			}
			break
		}
	}
	return ErrNoInput
}

// readModified is the inbound record for an AID: the AID, the cursor
// address, then the modified fields -- each as SBA plus its non-null
// characters.  An unformatted screen sends all its non-null characters.  A
// short read (CLEAR, PA keys) is the AID byte alone.
// secret reports whether the record carries input typed with TypeSecret.
func (s *screen) readModified(aid byte, short bool) (rec []byte, secret bool) {
	if short {
		return []byte{aid}, false
	}
	c1, c2 := encodeAddr(s.cursor)
	out := []byte{aid, c1, c2}
	if !s.formatted() {
		for p, b := range s.buf {
			if b != 0 {
				out = append(out, b)
				secret = secret || s.secret[p]
			}
		}
		return out, secret
	}
	for i, a := range s.attr {
		if a < 0 || a&attrMDT == 0 {
			continue
		}
		start := (i + 1) % bufSize
		a1, a2 := encodeAddr(start)
		out = append(out, orderSBA, a1, a2)
		for p := start; s.attr[p] < 0; p = (p + 1) % bufSize {
			if s.buf[p] != 0 {
				out = append(out, s.buf[p])
				secret = secret || s.secret[p]
			}
		}
	}
	return out, secret
}

// readBuffer is the answer to Read Buffer: AID, cursor, then every position
// -- SF plus the attribute where a field starts, the byte (nulls included)
// elsewhere.
func (s *screen) readBuffer(aid byte) (rec []byte, secret bool) {
	c1, c2 := encodeAddr(s.cursor)
	out := make([]byte, 0, 3+bufSize+64)
	out = append(out, aid, c1, c2)
	for i := 0; i < bufSize; i++ {
		if s.attr[i] >= 0 {
			out = append(out, orderSF, byte(s.attr[i]))
			continue
		}
		out = append(out, s.buf[i])
		secret = secret || s.secret[i]
	}
	return out, secret
}

// lines renders the screen: one string per row, trailing blanks trimmed.
// Attribute positions, nulls, non-display fields and secret input show as
// blanks.
func (s *screen) lines() []string {
	out := make([]string, Rows)
	var sb strings.Builder
	field := s.fieldOf(bufSize - 1) // the field the first position belongs to
	for r := 0; r < Rows; r++ {
		sb.Reset()
		for c := 0; c < Cols; c++ {
			p := r*Cols + c
			ch := ' '
			if s.attr[p] >= 0 {
				field = p
			} else if s.buf[p] != 0 && !s.secret[p] &&
				(field < 0 || s.attr[field]&attrDisplay != attrNonDisplay) {
				ch = displayRune(s.buf[p])
			}
			sb.WriteRune(ch)
		}
		out[r] = strings.TrimRight(sb.String(), " ")
	}
	return out
}
