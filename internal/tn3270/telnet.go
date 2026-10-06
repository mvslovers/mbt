package tn3270

import (
	"bufio"
	"fmt"
	"net"
	"time"
)

// Telnet as TN3270 (RFC 1576) uses it: terminal type, binary in both
// directions, end-of-record framing.  TN3270E (RFC 2355) is refused -- the
// host then falls back to plain TN3270, which is all a TSO session needs.

const (
	tnSE   = 0xF0
	tnEOR  = 0xEF
	tnSB   = 0xFA
	tnWILL = 0xFB
	tnWONT = 0xFC
	tnDO   = 0xFD
	tnDONT = 0xFE
	tnIAC  = 0xFF

	optBinary  = 0x00
	optTType   = 0x18
	optEOR     = 0x19
	optTN3270E = 0x28

	ttypeIS   = 0x00
	ttypeSEND = 0x01
)

// parser states
const (
	stData  = iota
	stIAC   // after IAC
	stOpt   // after IAC DO/DONT/WILL/WONT
	stSB    // inside a subnegotiation
	stSBIAC // IAC inside a subnegotiation
)

// telnet frames 3270 records on a connection and answers option
// negotiation on its own.  The parse state survives a read timeout, so a
// record that straddles a deadline is completed by the next read rather than
// lost.
type telnet struct {
	conn  net.Conn
	r     *bufio.Reader
	ttype string
	trace func(format string, a ...any)

	st  int
	cmd byte   // the DO/DONT/WILL/WONT in stOpt
	rec []byte // the record being assembled
	sub []byte // the subnegotiation being assembled

	will [256]bool // options we agreed to perform
	do   [256]bool // options we asked the host to perform
}

func newTelnet(c net.Conn, ttype string, trace func(string, ...any)) *telnet {
	return &telnet{conn: c, r: bufio.NewReader(c), ttype: ttype, trace: trace}
}

// pending: part of a record (or negotiation) has been read, or bytes wait in
// the buffer.  A quiet period only counts when nothing is pending.
func (t *telnet) pending() bool {
	return t.st != stData || len(t.rec) > 0 || t.r.Buffered() > 0
}

// readRecord returns the next complete 3270 record, answering telnet
// commands on the way.  A timeout keeps what has been read so far.
func (t *telnet) readRecord(deadline time.Time) ([]byte, error) {
	if err := t.conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	for {
		b, err := t.r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch t.st {
		case stData:
			if b == tnIAC {
				t.st = stIAC
			} else {
				t.rec = append(t.rec, b)
			}
		case stIAC:
			t.st = stData
			switch b {
			case tnIAC:
				t.rec = append(t.rec, tnIAC)
			case tnEOR:
				rec := t.rec
				t.rec = nil
				return rec, nil
			case tnDO, tnDONT, tnWILL, tnWONT:
				t.cmd, t.st = b, stOpt
			case tnSB:
				t.sub, t.st = t.sub[:0], stSB
			default: // NOP, GA, ...: nothing to do
			}
		case stOpt:
			t.st = stData
			if err := t.negotiate(t.cmd, b); err != nil {
				return nil, err
			}
		case stSB:
			if b == tnIAC {
				t.st = stSBIAC
			} else {
				t.sub = append(t.sub, b)
			}
		case stSBIAC:
			switch b {
			case tnSE:
				t.st = stData
				if err := t.subnegotiation(t.sub); err != nil {
					return nil, err
				}
			case tnIAC:
				t.sub = append(t.sub, tnIAC)
				t.st = stSB
			default: // malformed; keep going inside the subnegotiation
				t.st = stSB
			}
		}
	}
}

func (t *telnet) supportedLocal(opt byte) bool { // we perform it
	return opt == optBinary || opt == optEOR || opt == optTType
}

func (t *telnet) supportedRemote(opt byte) bool { // the host performs it
	return opt == optBinary || opt == optEOR
}

// negotiate answers one option command, replying only when the option's
// state changes (so the two sides cannot loop).
func (t *telnet) negotiate(cmd, opt byte) error {
	t.tracef("telnet <- %s %s", cmdName(cmd), optName(opt))
	switch cmd {
	case tnDO:
		if !t.supportedLocal(opt) {
			return t.send(tnIAC, tnWONT, opt) // TN3270E and anything else: no
		}
		if !t.will[opt] {
			t.will[opt] = true
			return t.send(tnIAC, tnWILL, opt)
		}
	case tnDONT:
		if t.will[opt] {
			t.will[opt] = false
			return t.send(tnIAC, tnWONT, opt)
		}
	case tnWILL:
		if !t.supportedRemote(opt) {
			return t.send(tnIAC, tnDONT, opt)
		}
		if !t.do[opt] {
			t.do[opt] = true
			return t.send(tnIAC, tnDO, opt)
		}
	case tnWONT:
		if t.do[opt] {
			t.do[opt] = false
			return t.send(tnIAC, tnDONT, opt)
		}
	}
	return nil
}

// subnegotiation answers TERMINAL-TYPE SEND with our terminal type.
func (t *telnet) subnegotiation(sub []byte) error {
	if len(sub) >= 2 && sub[0] == optTType && sub[1] == ttypeSEND {
		t.tracef("telnet <- SB TTYPE SEND")
		out := []byte{tnIAC, tnSB, optTType, ttypeIS}
		out = append(out, t.ttype...)
		out = append(out, tnIAC, tnSE)
		return t.send(out...)
	}
	t.tracef("telnet <- SB % X (ignored)", sub)
	return nil
}

func (t *telnet) send(b ...byte) error {
	if len(b) >= 3 && b[0] == tnIAC && b[1] != tnSB {
		t.tracef("telnet -> %s %s", cmdName(b[1]), optName(b[2]))
	} else if len(b) >= 3 && b[1] == tnSB {
		t.tracef("telnet -> SB TTYPE IS %s", t.ttype)
	}
	_, err := t.conn.Write(b)
	return err
}

// writeRecord sends one 3270 record: IAC doubled, IAC EOR appended.
func (t *telnet) writeRecord(rec []byte) error {
	out := make([]byte, 0, len(rec)+8)
	for _, b := range rec {
		out = append(out, b)
		if b == tnIAC {
			out = append(out, tnIAC)
		}
	}
	out = append(out, tnIAC, tnEOR)
	_, err := t.conn.Write(out)
	return err
}

func (t *telnet) tracef(format string, a ...any) {
	if t.trace != nil {
		t.trace(format, a...)
	}
}

func cmdName(c byte) string {
	switch c {
	case tnDO:
		return "DO"
	case tnDONT:
		return "DONT"
	case tnWILL:
		return "WILL"
	case tnWONT:
		return "WONT"
	}
	return fmt.Sprintf("%02X", c)
}

func optName(o byte) string {
	switch o {
	case optBinary:
		return "BINARY"
	case optTType:
		return "TTYPE"
	case optEOR:
		return "EOR"
	case optTN3270E:
		return "TN3270E"
	}
	return fmt.Sprintf("%02X", o)
}
