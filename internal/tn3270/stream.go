package tn3270

import (
	"errors"
	"fmt"
)

// 3270 commands.  Each has a local (channel-attached) and an SNA code; a
// TN3270 host may send either.
const (
	cmdW    = 0xF1 // Write
	cmdEW   = 0xF5 // Erase/Write
	cmdEWA  = 0x7E // Erase/Write Alternate
	cmdRB   = 0xF2 // Read Buffer
	cmdRM   = 0xF6 // Read Modified
	cmdRMA  = 0x6E // Read Modified All
	cmdEAU  = 0x6F // Erase All Unprotected
	cmdWSF  = 0xF3 // Write Structured Field
	snaW    = 0x01
	snaEW   = 0x05
	snaEWA  = 0x0D
	snaRB   = 0x02
	snaRM   = 0x06
	snaRMA  = 0x0E
	snaEAU  = 0x0F
	snaWSF  = 0x11
	wccMDT  = 0x01 // reset modified data tags
	wccKbd  = 0x02 // restore the keyboard
	wccBell = 0x04 // sound the alarm
)

// Orders inside a write.
const (
	orderPT  = 0x05 // Program Tab
	orderGE  = 0x08 // Graphic Escape
	orderSBA = 0x11 // Set Buffer Address
	orderEUA = 0x12 // Erase Unprotected to Address
	orderIC  = 0x13 // Insert Cursor
	orderSF  = 0x1D // Start Field
	orderSA  = 0x28 // Set Attribute
	orderSFE = 0x29 // Start Field Extended
	orderMF  = 0x2C // Modify Field
	orderRA  = 0x3C // Repeat to Address
)

// Structured fields.
const (
	sfReadPartition = 0x01
	sfEraseReset    = 0x03
	sfOutbound3270  = 0x40
	rpQuery         = 0x02
	rpQueryList     = 0x03
)

// errTruncated: a record ended inside an order or a structured field.
// Everything before it has been applied.
var errTruncated = errors.New("tn3270: truncated 3270 record")

// hostResult is what a host record asks of the terminal.
type hostResult struct {
	restore    bool // keyboard restored (WCC 0x02, or EAU)
	readMod    bool // answer with Read Modified data
	readBuffer bool // answer with the whole buffer
	query      bool // answer with the query reply
}

func (r *hostResult) merge(o hostResult) {
	r.restore = r.restore || o.restore
	r.readMod = r.readMod || o.readMod
	r.readBuffer = r.readBuffer || o.readBuffer
	r.query = r.query || o.query
}

// process applies one host record (one 3270 command with its data).
func (s *screen) process(rec []byte) (hostResult, error) {
	if len(rec) == 0 {
		return hostResult{}, nil
	}
	return s.command(rec[0], rec[1:])
}

func (s *screen) command(cmd byte, data []byte) (hostResult, error) {
	var res hostResult
	switch cmd {
	case cmdRM, snaRM, cmdRMA, snaRMA:
		res.readMod = true
	case cmdRB, snaRB:
		res.readBuffer = true
	case cmdEAU, snaEAU:
		s.eraseUnprotected()
		res.restore = true
	case cmdEW, snaEW, cmdEWA, snaEWA:
		s.erase()
		return s.write(data)
	case cmdW, snaW:
		return s.write(data)
	case cmdWSF, snaWSF:
		return s.structuredFields(data)
	default:
		return res, fmt.Errorf("tn3270: unknown 3270 command %02X", cmd)
	}
	return res, nil
}

// eraseUnprotected: null every unprotected position, reset their MDTs, and
// put the cursor into the first input field.
func (s *screen) eraseUnprotected() {
	if !s.formatted() {
		s.erase()
		return
	}
	for i := 0; i < bufSize; i++ {
		if s.attr[i] >= 0 {
			if s.attr[i]&attrProtected == 0 {
				s.attr[i] &^= attrMDT
			}
			continue
		}
		if !s.protected(i) {
			s.setChar(i, 0)
		}
	}
	if p := s.nextUnprotected(0); p >= 0 {
		s.cursor = p
	} else {
		s.cursor = 0
	}
}

// write applies a WCC and its orders.  The buffer address starts at the
// cursor (Erase/Write has just put that at 0).
func (s *screen) write(data []byte) (hostResult, error) {
	var res hostResult
	if len(data) == 0 {
		return res, nil
	}
	wcc := data[0]
	if wcc&wccMDT != 0 {
		s.resetMDT()
	}
	// the keyboard is restored after the write has been applied, so record
	// it now and report it whatever happens below
	res.restore = wcc&wccKbd != 0
	pos := s.cursor
	need := func(i, n int) bool { return i+n < len(data) }
	for i := 1; i < len(data); i++ {
		switch b := data[i]; b {
		case orderSBA:
			if !need(i, 2) {
				return res, errTruncated
			}
			pos = decodeAddr(data[i+1], data[i+2]) % bufSize
			i += 2
		case orderSF:
			if !need(i, 1) {
				return res, errTruncated
			}
			s.setAttr(pos, data[i+1])
			pos = (pos + 1) % bufSize
			i++
		case orderSFE:
			if !need(i, 1) {
				return res, errTruncated
			}
			n := int(data[i+1])
			if !need(i, 1+2*n) {
				return res, errTruncated
			}
			a := byte(0)
			for k := 0; k < n; k++ {
				if data[i+2+2*k] == 0xC0 { // the 3270 field attribute
					a = data[i+3+2*k]
				}
			}
			s.setAttr(pos, a)
			pos = (pos + 1) % bufSize
			i += 1 + 2*n
		case orderMF:
			if !need(i, 1) {
				return res, errTruncated
			}
			n := int(data[i+1])
			if !need(i, 1+2*n) {
				return res, errTruncated
			}
			if s.attr[pos] >= 0 {
				for k := 0; k < n; k++ {
					if data[i+2+2*k] == 0xC0 {
						s.attr[pos] = int16(data[i+3+2*k])
					}
				}
			}
			pos = (pos + 1) % bufSize
			i += 1 + 2*n
		case orderIC:
			s.cursor = pos
		case orderPT:
			if p := s.nextUnprotected(pos); p >= 0 {
				pos = p
			} else {
				pos = 0
			}
		case orderRA:
			if !need(i, 3) {
				return res, errTruncated
			}
			to := decodeAddr(data[i+1], data[i+2]) % bufSize
			ch := data[i+3]
			i += 3
			if ch == orderGE {
				if !need(i, 1) {
					return res, errTruncated
				}
				ch = data[i+1]
				i++
			}
			for k := span(pos, to); k > 0; k-- {
				s.setChar(pos, ch)
				pos = (pos + 1) % bufSize
			}
		case orderEUA:
			if !need(i, 2) {
				return res, errTruncated
			}
			to := decodeAddr(data[i+1], data[i+2]) % bufSize
			i += 2
			for k := span(pos, to); k > 0; k-- {
				if s.attr[pos] < 0 && !s.protected(pos) {
					s.setChar(pos, 0)
				}
				pos = (pos + 1) % bufSize
			}
		case orderSA:
			if !need(i, 2) {
				return res, errTruncated
			}
			i += 2
		case orderGE:
			if !need(i, 1) {
				return res, errTruncated
			}
			s.setChar(pos, data[i+1])
			pos = (pos + 1) % bufSize
			i++
		default:
			s.setChar(pos, b)
			pos = (pos + 1) % bufSize
		}
	}
	return res, nil
}

// span counts the positions from pos up to, not including, to; equal
// addresses mean the whole buffer (RA and EUA are defined that way).
func span(pos, to int) int {
	n := (to - pos + bufSize) % bufSize
	if n == 0 {
		return bufSize
	}
	return n
}

// structuredFields walks the fields of a WSF: each is a two-byte length
// (0: the rest of the record), an id, and data.
func (s *screen) structuredFields(data []byte) (hostResult, error) {
	var res hostResult
	for i := 0; i < len(data); {
		if i+3 > len(data) {
			return res, errTruncated
		}
		n := int(data[i])<<8 | int(data[i+1])
		if n == 0 {
			n = len(data) - i
		}
		if n < 3 || i+n > len(data) {
			return res, errTruncated
		}
		body := data[i+3 : i+n]
		switch data[i+2] {
		case sfReadPartition: // partition id, type
			if len(body) >= 2 {
				switch body[1] {
				case rpQuery, rpQueryList:
					res.query = true
				case cmdRB:
					res.readBuffer = true
				case cmdRM, cmdRMA:
					res.readMod = true
				}
			}
		case sfEraseReset:
			s.erase()
		case sfOutbound3270: // partition id, a write command, its data
			if len(body) >= 2 {
				r, err := s.command(body[1], body[2:])
				res.merge(r)
				if err != nil {
					return res, err
				}
			}
		}
		i += n
	}
	return res, nil
}

// queryReply is the inbound answer to Read Partition Query: AID 0x88 with
// three query replies -- Summary, Usable Area, Implicit Partition.  That is
// enough for TSO to take the terminal as a 24x80 3278-2 with the extended
// data stream; without it, an IBM-3278-2-E session falls into error
// recovery (IKT00405I).
func queryReply() []byte {
	sf := func(b ...byte) []byte {
		n := len(b) + 2
		return append([]byte{byte(n >> 8), byte(n)}, b...)
	}
	out := []byte{0x88}
	out = append(out, sf(0x81, 0x80, 0x80, 0x81, 0xA6)...) // Summary: itself, Usable Area, Implicit Partition
	out = append(out, sf(0x81, 0x81,                       // Usable Area
		0x01, 0x00, // 12/14-bit addressing; no special features
		0x00, Cols, 0x00, Rows, // width, height
		0x00,                   // units: inches
		0x00, 0x0A, 0x02, 0xE5, // Xr
		0x00, 0x02, 0x00, 0x6F, // Yr
		0x09, 0x0C, // AW, AH: cell size in pixels
		byte(bufSize>>8), byte(bufSize&0xFF))...) // buffer size
	out = append(out, sf(0x81, 0xA6, 0x00, 0x00, // Implicit Partition
		0x0B, 0x01, 0x00, 0x00, Cols, 0x00, Rows, 0x00, Cols, 0x00, Rows)...)
	return out
}
