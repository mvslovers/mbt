package tn3270

import "fmt"

// Key is an attention key: its value is the AID byte the terminal sends.
type Key byte

// The attention keys.  PF13-PF24 are reached through PF.
const (
	Enter Key = 0x7D
	Clear Key = 0x6D
	PA1   Key = 0x6C
	PA2   Key = 0x6E
	PA3   Key = 0x6B
	PF1   Key = 0xF1
	PF2   Key = 0xF2
	PF3   Key = 0xF3
	PF4   Key = 0xF4
	PF5   Key = 0xF5
	PF6   Key = 0xF6
	PF7   Key = 0xF7
	PF8   Key = 0xF8
	PF9   Key = 0xF9
	PF10  Key = 0x7A
	PF11  Key = 0x7B
	PF12  Key = 0x7C

	aidNone  = 0x60 // no AID: the answer to a host Read command
	aidQuery = 0x88 // structured field: the query reply
)

var pfAIDs = [24]Key{
	PF1, PF2, PF3, PF4, PF5, PF6, PF7, PF8, PF9, PF10, PF11, PF12,
	0xC1, 0xC2, 0xC3, 0xC4, 0xC5, 0xC6, 0xC7, 0xC8, 0xC9, 0x4A, 0x4B, 0x4C,
}

// PF returns program function key n, 1 to 24.
func PF(n int) (Key, error) {
	if n < 1 || n > 24 {
		return 0, fmt.Errorf("tn3270: there is no PF%d", n)
	}
	return pfAIDs[n-1], nil
}

// short reports whether the key sends a short read: the AID byte alone, no
// cursor address and no data.
func (k Key) short() bool {
	return k == Clear || k == PA1 || k == PA2 || k == PA3
}

func (k Key) valid() bool {
	switch k {
	case Enter, Clear, PA1, PA2, PA3:
		return true
	}
	for _, p := range pfAIDs {
		if k == p {
			return true
		}
	}
	return false
}

func (k Key) String() string {
	switch k {
	case Enter:
		return "ENTER"
	case Clear:
		return "CLEAR"
	case PA1:
		return "PA1"
	case PA2:
		return "PA2"
	case PA3:
		return "PA3"
	}
	for i, p := range pfAIDs {
		if k == p {
			return fmt.Sprintf("PF%d", i+1)
		}
	}
	return fmt.Sprintf("AID %02X", byte(k))
}
