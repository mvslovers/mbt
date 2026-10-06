package tn3270

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UseridInUseError: TSO rejected the logon because the userid already has
// a session (IKJ56425I).  A session that ended without LOGOFF stays in use;
// the operator frees it with C U=<userid>.
type UseridInUseError struct {
	User   string
	Screen string
}

func (e *UseridInUseError) Error() string {
	return fmt.Sprintf("tn3270: TSO userid %s is in use (IKJ56425I); a session that ended without LOGOFF holds it -- "+
		"cancel it from the console with C U=%s and wait until D TS,L no longer lists it", e.User, e.User)
}

// LogonError: the logon did not reach READY.
type LogonError struct {
	Reason string
	Screen string
}

func (e *LogonError) Error() string {
	return fmt.Sprintf("tn3270: TSO logon failed: %s; screen:\n%s", e.Reason, e.Screen)
}

// ErrLogoffUnconfirmed is wrapped when LOGOFF was sent but "LOGGED OFF"
// never appeared: the userid may still be in use.
var ErrLogoffUnconfirmed = errors.New("tn3270: LOGOFF not confirmed, the userid may stay in use")

// defaultLogonTimeout bounds LogonTSO when ctx has no deadline.
const defaultLogonTimeout = 2 * time.Minute

// LogonTSO logs on to TSO from whatever the first screen is, up to READY:
//
//	a banner (Hercules, a system logo)  -> ENTER
//	"Logon ===>" (VTAM) or IKJ56700A     -> the userid, ENTER
//	"... PASSWORD ..."                   -> the password (secret), ENTER
//	"***" (a full screen of broadcasts)  -> ENTER
//	READY                                -> done
//
// IKJ56425I (the userid is in use) returns *UseridInUseError; other
// rejections return *LogonError.  ctx bounds the whole logon (default two
// minutes) and is checked between steps.
func (s *Session) LogonTSO(ctx context.Context, user, password string) error {
	deadline := time.Now().Add(defaultLogonTimeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	user = strings.ToUpper(user)
	userSent, passSent := false, false
	banners := 0
	lastActed := "\x00" // the screen the last action was taken on
	step := "waiting for the first screen"
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return s.leaveLogon(&LogonError{Reason: "no READY before the deadline; last step: " + step, Screen: s.Text()})
		}
		txt := s.Text()
		if err := s.logonRejected(user); err != nil {
			return s.leaveLogon(err)
		}
		if hasLine(txt, "READY") {
			s.loggedOn, s.logoffTry = true, false
			return nil
		}
		var act func() error
		switch {
		case txt == lastActed:
			// nothing new since the last action: wait for the host
		case userSent && !passSent && strings.Contains(txt, "PASSWORD"):
			passSent, step = true, "ENTER after the password"
			act = func() error { return s.typeAndEnter(password, true) }
		case !userSent && (strings.Contains(txt, "Logon ===>") || strings.Contains(txt, "IKJ56700A")):
			userSent, step = true, "ENTER after the userid"
			act = func() error { return s.typeAndEnter(user, false) }
		case hasLine(txt, "***"):
			step = "ENTER at ***"
			act = func() error { return s.Key(Enter) }
		case !userSent && banners < 3:
			banners++
			step = "ENTER on the first screen, no logon screen came back"
			act = func() error { return s.Key(Enter) }
		}
		if act != nil {
			lastActed = txt
			if err := act(); err != nil && !errors.Is(err, ErrTimeout) {
				if rerr := s.logonRejected(user); rerr != nil {
					return s.leaveLogon(rerr)
				}
				return &LogonError{Reason: err.Error(), Screen: s.Text()}
			}
			continue
		}
		// wait for the host: one record or two seconds, then let it settle
		wait := time.Now().Add(2 * time.Second)
		if wait.After(deadline) {
			wait = deadline
		}
		if got, err := s.readOne(wait); err != nil && !errors.Is(err, ErrTimeout) {
			if rerr := s.logonRejected(user); rerr != nil {
				return s.leaveLogon(rerr)
			}
			return &LogonError{Reason: err.Error(), Screen: s.Text()}
		} else if got {
			if err := s.settle(deadline); err != nil {
				return &LogonError{Reason: err.Error(), Screen: s.Text()}
			}
		}
	}
}

// leaveLogon answers TSO's "IKJ56400A ENTER LOGON OR LOGOFF-", which
// follows a rejection, with LOGOFF -- so the terminal is not left inside a
// logon that never completes -- and returns err.
func (s *Session) leaveLogon(err error) error {
	if s.err != nil || !strings.Contains(s.Text(), "IKJ56400A") {
		return err
	}
	if s.locked {
		_ = s.WaitUnlock(5 * time.Second)
	}
	if s.Type("LOGOFF") == nil && s.SendKey(Enter) == nil {
		// the host ends the session or shows a logon screen again; give it
		// a moment either way
		_ = s.WaitUnlock(10 * time.Second)
	}
	return err
}

func (s *Session) typeAndEnter(text string, secret bool) error {
	if err := s.typeText(text, secret); err != nil {
		return err
	}
	return s.Key(Enter)
}

// logonRejected looks for a TSO rejection on the screen, current or seen
// since the last key.
func (s *Session) logonRejected(user string) error {
	screens := append([]string{s.Text()}, s.seen...)
	for _, scr := range screens {
		for _, line := range strings.Split(scr, "\n") {
			l := strings.TrimSpace(line)
			switch {
			case strings.Contains(l, "IKJ56425I"):
				return &UseridInUseError{User: user, Screen: scr}
			case strings.Contains(l, "LOGON REJECTED"), strings.Contains(l, "NOT AUTHORIZED"):
				return &LogonError{Reason: l, Screen: scr}
			}
		}
	}
	return nil
}

// hasLine: some row of txt, trimmed, is exactly want.
func hasLine(txt, want string) bool {
	for _, line := range strings.Split(txt, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// LogoffTSO logs off from TSO's READY prompt: it waits for (or, with PA1,
// interrupts) a running command, presses CLEAR, types LOGOFF and waits for
// "LOGGED OFF".  It returns nil only when that confirmation was seen;
// otherwise the error wraps ErrLogoffUnconfirmed and carries the screen.
// It does not get out of a full-screen application such as ISPF.
func (s *Session) LogoffTSO() error {
	s.logoffTry = true
	if s.err != nil {
		return fmt.Errorf("%w: %v", ErrLogoffUnconfirmed, s.err)
	}
	if s.locked {
		if err := s.WaitUnlock(10 * time.Second); err != nil {
			_ = s.SendKey(PA1) // attention: interrupt the running command
			_ = s.WaitUnlock(10 * time.Second)
		}
	}
	if err := s.Key(Clear); err != nil && !errors.Is(err, ErrTimeout) {
		return fmt.Errorf("%w: %v", ErrLogoffUnconfirmed, err)
	}
	if err := s.Type("LOGOFF"); err != nil {
		return fmt.Errorf("%w: %v", ErrLogoffUnconfirmed, err)
	}
	if err := s.SendKey(Enter); err != nil {
		return fmt.Errorf("%w: %v", ErrLogoffUnconfirmed, err)
	}
	if err := s.Expect("LOGGED OFF", 30*time.Second); err != nil {
		return fmt.Errorf("%w: %v", ErrLogoffUnconfirmed, err)
	}
	s.loggedOn = false
	return nil
}
