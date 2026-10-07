// Package tn3270 is a minimal TN3270 client: enough of a 3278 model 2
// terminal to log on to TSO, type commands, press attention keys and wait
// for text on the screen.  It is what interactive TSO tests drive.
//
// What a TSO session needs from a client, each found by failing first
// against MVS 3.8j:
//
//   - terminal type IBM-3278-2-E with an answer to Read Partition Query
//     (as plain IBM-3278-2, TSO falls into error recovery, IKT00405I);
//   - honouring WCC reset-MDT, or the previous command is sent again with
//     the next one;
//   - after an attention key, waiting for the keyboard to be restored and
//     then for the host to fall quiet before typing -- TSO answers in
//     several writes, and a later one resets what was typed in between;
//   - answering the host's Read Modified and Read Buffer;
//   - logging off on every path: dropping the connection does not free
//     the userid (IKJ56425I ... IN USE).
//
// A Session is not safe for concurrent use.
package tn3270

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// Options configure a session.  Zero values take the defaults.
type Options struct {
	// TerminalType is sent in the telnet TERMINAL-TYPE negotiation.  Only
	// model 2 (24x80) types are accepted.  Default IBM-3278-2-E.
	TerminalType string
	// Trace, if set, receives the telnet negotiation and every 3270 record
	// in hex.  A record that carries input typed with TypeSecret is traced
	// as its AID and length only.
	Trace io.Writer
	// DialTimeout bounds the connection and the wait for the first screen.
	// Default 30 s.
	DialTimeout time.Duration
	// KeyTimeout bounds Key's wait for the keyboard to be restored.
	// Default 60 s.
	KeyTimeout time.Duration
	// Settle is how long the host must be quiet before the screen counts as
	// complete.  Default 300 ms.
	Settle time.Duration
}

// DefaultTerminalType is the terminal type a TSO session needs.
const DefaultTerminalType = "IBM-3278-2-E"

// ErrTimeout is wrapped by every error that is a timeout waiting for the
// host.
var ErrTimeout = errors.New("tn3270: timed out")

// Session is one TN3270 connection and its screen.
type Session struct {
	opt    Options
	conn   net.Conn
	tn     *telnet
	scr    *screen
	locked bool
	// seen holds the screen text after each host record since the last
	// attention key, so that Expect also finds text a later write erased.
	seen []string
	err  error // the connection has failed (EOF, reset): sticky

	loggedOn  bool // LogonTSO reached READY
	logoffTry bool // LogoffTSO has been tried since
}

// Dial connects to addr (host:port), negotiates TN3270 and waits for the
// first screen.
func Dial(ctx context.Context, addr string, opt Options) (*Session, error) {
	if opt.TerminalType == "" {
		opt.TerminalType = DefaultTerminalType
	}
	if !model2(opt.TerminalType) {
		return nil, fmt.Errorf("tn3270: terminal type %s: only model 2 (24x80) is supported", opt.TerminalType)
	}
	if opt.DialTimeout <= 0 {
		opt.DialTimeout = 30 * time.Second
	}
	if opt.KeyTimeout <= 0 {
		opt.KeyTimeout = 60 * time.Second
	}
	if opt.Settle <= 0 {
		opt.Settle = 300 * time.Millisecond
	}
	deadline := time.Now().Add(opt.DialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	d := net.Dialer{Deadline: deadline}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tn3270: %w", err)
	}
	s := &Session{opt: opt, conn: c, scr: newScreen()}
	s.tn = newTelnet(c, opt.TerminalType, s.tracef)
	for {
		if err := ctx.Err(); err != nil {
			c.Close()
			return nil, err
		}
		got, err := s.readOne(deadline)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("tn3270: %s: waiting for the first screen: %w", addr, err)
		}
		if got {
			break
		}
	}
	if err := s.settle(deadline); err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

func model2(tt string) bool {
	return strings.HasPrefix(tt, "IBM-327") && (strings.HasSuffix(tt, "-2") || strings.HasSuffix(tt, "-2-E"))
}

// Close ends the session.  If LogonTSO succeeded and LogoffTSO has not been
// tried, it logs off first -- dropping the connection would leave the
// userid in use.
func (s *Session) Close() error {
	var err error
	if s.loggedOn && !s.logoffTry && s.err == nil {
		err = s.LogoffTSO()
	}
	if cerr := s.conn.Close(); err == nil && cerr != nil && !errors.Is(cerr, net.ErrClosed) {
		err = cerr
	}
	return err
}

// Screen returns the 24 rows, trailing blanks trimmed.  Non-display fields
// and input typed with TypeSecret show as blanks.
func (s *Session) Screen() []string { return s.scr.lines() }

// Text is the screen as one string, rows separated by newlines.
func (s *Session) Text() string { return strings.Join(s.scr.lines(), "\n") }

// Cursor returns the cursor position, 0-based.
func (s *Session) Cursor() (row, col int) { return s.scr.cursor / Cols, s.scr.cursor % Cols }

// SetCursor moves the cursor, 0-based.
func (s *Session) SetCursor(row, col int) error {
	if row < 0 || row >= Rows || col < 0 || col >= Cols {
		return fmt.Errorf("tn3270: cursor %d,%d is off the screen", row, col)
	}
	s.scr.cursor = row*Cols + col
	return nil
}

// Tab moves the cursor to the next unprotected field.
func (s *Session) Tab() error { return s.scr.tab() }

// Locked reports whether the keyboard is locked (an attention key was sent
// and the host has not restored the keyboard yet).
func (s *Session) Locked() bool { return s.locked }

// Type puts text at the cursor, as an operator would.  It fails when the
// keyboard is locked or the cursor is in a protected field.
func (s *Session) Type(text string) error { return s.typeText(text, false) }

// TypeSecret types like Type, but the input never appears in Screen, Text,
// error messages or the trace.
func (s *Session) TypeSecret(text string) error { return s.typeText(text, true) }

func (s *Session) typeText(text string, secret bool) error {
	if s.locked {
		return fmt.Errorf("tn3270: keyboard locked; screen:\n%s", s.Text())
	}
	if err := s.scr.typeText(text, secret); err != nil {
		return fmt.Errorf("%w; screen:\n%s", err, s.Text())
	}
	return nil
}

// SendKey sends an attention key with the Read Modified data it carries and
// returns without waiting for the host.
func (s *Session) SendKey(k Key) error {
	if !k.valid() {
		return fmt.Errorf("tn3270: %v is not an attention key", k)
	}
	if s.err != nil {
		return s.err
	}
	rec, secret := s.scr.readModified(byte(k), k.short())
	s.seen = s.seen[:0]
	if err := s.sendRecord(rec, secret); err != nil {
		return err
	}
	s.locked = true
	if k == Clear { // CLEAR clears the screen locally as well
		s.scr.erase()
	}
	return nil
}

// Key sends an attention key, waits for the host to restore the keyboard
// (KeyTimeout) and then for the host to fall quiet (Settle).
func (s *Session) Key(k Key) error {
	if err := s.SendKey(k); err != nil {
		return err
	}
	return s.WaitUnlock(s.opt.KeyTimeout)
}

// WaitUnlock reads host records until the keyboard is restored, then until
// the host is quiet for Settle.  Input typed before that would be lost (or,
// with TSO, reset by the next write).
func (s *Session) WaitUnlock(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for s.locked {
		if _, err := s.readOne(deadline); err != nil {
			if errors.Is(err, ErrTimeout) {
				return fmt.Errorf("%w: keyboard still locked after %s; screen:\n%s", ErrTimeout, timeout, s.Text())
			}
			return fmt.Errorf("%w; screen:\n%s", err, s.Text())
		}
	}
	return s.settle(deadline.Add(s.opt.Settle))
}

// Expect waits until text is on the screen, or was on it at any point since
// the last attention key.  The error carries the screen.
func (s *Session) Expect(text string, timeout time.Duration) error {
	_, err := s.ExpectAny([]string{text}, timeout)
	return err
}

// ExpectAny waits until one of texts is (or was, since the last attention
// key) on the screen, and returns its index.
func (s *Session) ExpectAny(texts []string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		if i := s.find(texts); i >= 0 {
			return i, nil
		}
		if _, err := s.readOne(deadline); err != nil {
			if i := s.find(texts); i >= 0 {
				return i, nil
			}
			return -1, fmt.Errorf("expected %s: %w; screen:\n%s", quoteAll(texts), err, s.Text())
		}
	}
}

// WaitScreen reads host records until cond holds for the screen text (as
// Text returns it), checking the current screen first.
func (s *Session) WaitScreen(cond func(text string) bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for !cond(s.Text()) {
		if _, err := s.readOne(deadline); err != nil {
			if cond(s.Text()) {
				return nil
			}
			return fmt.Errorf("waiting for the screen: %w; screen:\n%s", err, s.Text())
		}
	}
	return nil
}

func (s *Session) find(texts []string) int {
	cur := s.Text()
	for i, t := range texts {
		if strings.Contains(cur, t) {
			return i
		}
		for _, old := range s.seen {
			if strings.Contains(old, t) {
				return i
			}
		}
	}
	return -1
}

func quoteAll(texts []string) string {
	q := make([]string, len(texts))
	for i, t := range texts {
		q[i] = fmt.Sprintf("%q", t)
	}
	return strings.Join(q, " or ")
}

// readOne reads and applies one host record.  false, nil: nothing complete
// arrived before the deadline -- that is not reported as an error here, only
// by the callers that need it.  A timeout returns false and an error
// wrapping ErrTimeout.
func (s *Session) readOne(deadline time.Time) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if !time.Now().Before(deadline) {
		return false, ErrTimeout
	}
	rec, err := s.tn.readRecord(deadline)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return false, ErrTimeout
		}
		s.err = fmt.Errorf("tn3270: connection: %w", err)
		return false, s.err
	}
	s.apply(rec)
	return true, nil
}

// settle reads records until the host has been quiet for Settle, or until
// the deadline.
func (s *Session) settle(deadline time.Time) error {
	for {
		quiet := time.Now().Add(s.opt.Settle)
		if quiet.After(deadline) {
			quiet = deadline
		}
		_, err := s.readOne(quiet)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrTimeout) {
			return err
		}
		if !s.tn.pending() || !time.Now().Before(deadline) {
			return nil
		}
	}
}

// apply interprets a host record and answers what it asks for.
func (s *Session) apply(rec []byte) {
	s.tracef("<- %d bytes: % X", len(rec), rec)
	res, err := s.scr.process(rec)
	if err != nil {
		s.tracef("   %v", err)
	}
	if res.restore {
		s.locked = false
	}
	s.seen = append(s.seen, s.Text())
	var reply []byte
	secret := false
	switch {
	case res.query:
		reply = queryReply()
	case res.readBuffer:
		reply, secret = s.scr.readBuffer(aidNone)
	case res.readMod:
		reply, secret = s.scr.readModified(aidNone, false)
	}
	if reply != nil {
		if err := s.sendRecord(reply, secret); err != nil && s.err == nil {
			s.err = fmt.Errorf("tn3270: connection: %w", err)
		}
	}
}

func (s *Session) sendRecord(rec []byte, secret bool) error {
	if secret {
		s.tracef("-> AID %02X, %d bytes (holds secret input, not shown)", rec[0], len(rec))
	} else {
		s.tracef("-> %d bytes: % X", len(rec), rec)
	}
	if err := s.tn.writeRecord(rec); err != nil {
		s.err = fmt.Errorf("tn3270: connection: %w", err)
		return s.err
	}
	return nil
}

func (s *Session) tracef(format string, a ...any) {
	if s.opt.Trace != nil {
		fmt.Fprintf(s.opt.Trace, format+"\n", a...)
	}
}
