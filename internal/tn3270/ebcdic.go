package tn3270

// The host side of a TSO session is EBCDIC, code page 037.  cp037 (generated,
// cp037.go) maps every byte to its Unicode code point; CP037 is a bijection
// onto Latin-1, so the reverse map is total over U+0000..U+00FF.

var fromLatin1 [256]byte

func init() {
	for e, r := range cp037 {
		fromLatin1[r] = byte(e)
	}
}

// toEBCDIC converts one character for typing.  ok is false for a character
// CP037 cannot represent.
func toEBCDIC(r rune) (b byte, ok bool) {
	if r < 0 || r > 0xFF {
		return 0, false
	}
	return fromLatin1[r], true
}

// displayRune is what a screen position shows for an EBCDIC byte: control
// code points (and the null) display as a blank, as on a terminal, and so
// does the required space (0x41).
func displayRune(e byte) rune {
	r := cp037[e]
	if r < 0x20 || (r >= 0x7F && r <= 0x9F) || r == 0xA0 {
		return ' '
	}
	return r
}
