package tn3270

import (
	"fmt"
	"strconv"
	"strings"
)

// Size is a screen size.  It is written COLSxROWS, as x3270's -oversize
// takes it: "80x43" is 43 rows of 80 columns, "160x62" 62 rows of 160.
type Size struct{ Cols, Rows int }

func (z Size) String() string { return fmt.Sprintf("%dx%d", z.Cols, z.Rows) }

// The alternate sizes of the 3278 models; model 2 has none beyond its 24x80.
var models = []struct {
	model int
	size  Size
}{
	{2, Size{80, 24}},
	{3, Size{80, 32}},
	{4, Size{80, 43}},
	{5, Size{132, 27}},
}

// maxPositions is what a 14-bit buffer address reaches.
const maxPositions = 16383

// ParseSize reads COLSxROWS.  The model sizes 80x24, 80x32, 80x43 and
// 132x27 are taken as they are; any other size is an oversize screen and
// must hold at least 80 columns and 24 rows and at most 16383 positions
// (what a 14-bit buffer address reaches).
func ParseSize(text string) (Size, error) {
	bad := func() (Size, error) {
		return Size{}, fmt.Errorf("screen size %q: write COLSxROWS -- 80x24, 80x32, 80x43, 132x27, or an oversize screen of at least 80x24 and at most %d positions (e.g. 160x62)", text, maxPositions)
	}
	c, r, ok := strings.Cut(strings.ToLower(strings.TrimSpace(text)), "x")
	if !ok {
		return bad()
	}
	cols, err1 := strconv.Atoi(c)
	rows, err2 := strconv.Atoi(r)
	if err1 != nil || err2 != nil {
		return bad()
	}
	z := Size{Cols: cols, Rows: rows}
	if err := z.check(); err != nil {
		return bad()
	}
	return z, nil
}

func (z Size) check() error {
	if z.Cols < Cols || z.Rows < Rows || z.Cols*z.Rows > maxPositions {
		return fmt.Errorf("tn3270: screen size %s: at least 80x24, at most %d positions", z, maxPositions)
	}
	return nil
}

// Model is the 3278 model a size presents itself as: the model of that
// size, or for an oversize screen the largest model it covers in both
// directions -- what x3270 does, a model number plus an oversize screen
// announced in the query reply.
func (z Size) Model() int {
	best := 2
	for _, m := range models {
		if m.size == z {
			return m.model
		}
		if z.Cols >= m.size.Cols && z.Rows >= m.size.Rows {
			best = m.model
		}
	}
	return best
}

// Oversize reports whether z is not the size of a 3278 model.
func (z Size) Oversize() bool {
	for _, m := range models {
		if m.size == z {
			return false
		}
	}
	return true
}

// TerminalType is the TERMINAL-TYPE a session of this size negotiates:
// IBM-3278-<model>-E.  An oversize screen goes as its model, as with x3270;
// the size itself travels in the query reply.
func (z Size) TerminalType() string { return fmt.Sprintf("IBM-3278-%d-E", z.Model()) }

// modelOf reads the model from a 3278/3279 terminal type, "IBM-3278-4-E"
// -> 4.  0: not one.
func modelOf(tt string) int {
	t := strings.TrimSuffix(strings.ToUpper(tt), "-E")
	if !strings.HasPrefix(t, "IBM-3278-") && !strings.HasPrefix(t, "IBM-3279-") {
		return 0
	}
	n, err := strconv.Atoi(t[len("IBM-327x-"):])
	if err != nil || n < 2 || n > 5 {
		return 0
	}
	return n
}
