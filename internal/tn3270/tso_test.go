package tn3270

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

// isISPFPrimary drives the new logon/logoff ISPF handling (mbt#272). The
// state machine itself is exercised live against a stand whose logon proc
// starts ISPF; this covers the pure screen test offline.
func TestIsISPFPrimary(t *testing.T) {
	yes := []string{
		"              ISPF/PDF PRIMARY OPTION MENU\n Option ===>\n",
		"  Menu  Utilities  Compilers  Options\n          ISPF Primary Option Menu\n Option  ===>              \n 0  Settings\n",
		"option   ===> =x\nwelcome to ispf\n", // collapsed spacing, lower case
	}
	no := []string{
		"READY\n",
		"IKJ56700A ENTER USERID -\n",
		"***\n",
		" Option ===> \n",           // an Option line but no ISPF anywhere
		"ISPF\n Command ===> 3.4\n", // ISPF, but a Command line, not the primary Option menu
		"",
	}
	for _, s := range yes {
		if !isISPFPrimary(s) {
			t.Errorf("isISPFPrimary = false, want true:\n%q", s)
		}
	}
	for _, s := range no {
		if isISPFPrimary(s) {
			t.Errorf("isISPFPrimary = true, want false:\n%q", s)
		}
	}
}

// WaitLogon against the recorded TK5 logon: READY, then LOGOFF and the
// wait for VTAM's screen; one try, no sleep (mbt#284).
func TestWaitLogon(t *testing.T) {
	addr, done := serveScript(t, tk5Steps(t, 0))
	slept := 0
	took, tries, err := WaitLogon(context.Background(), addr, testOptions, "herc01", fakePassword,
		time.Now().Add(30*time.Second), func(time.Duration) { slept++ })
	if err != nil {
		t.Fatal(err)
	}
	if tries != 1 || slept != 0 || took <= 0 {
		t.Errorf("took %v, %d tries, slept %d times; want one try, no sleep", took, tries, slept)
	}
	scriptResult(t, done)
}

// A system still coming up refuses the connection; WaitLogon tries again
// and logs on once the host answers, within the deadline.
func TestWaitLogonRefusedFirst(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens: the first try is refused
	var done <-chan error
	slept := 0
	sleep := func(time.Duration) {
		slept++
		if done != nil {
			return
		}
		l, err := net.Listen("tcp", addr) // the host comes up
		if err != nil {
			t.Error(err)
			return
		}
		done = serveScriptOn(l, tk5Steps(t, 0))
	}
	_, tries, err := WaitLogon(context.Background(), addr, testOptions, "herc01", fakePassword,
		time.Now().Add(30*time.Second), sleep)
	if err != nil {
		t.Fatal(err)
	}
	if tries != 2 || slept != 1 {
		t.Errorf("%d tries, slept %d times; want 2 tries, one sleep", tries, slept)
	}
	scriptResult(t, done)
}

// IKJ56425I does not go away by waiting: WaitLogon fails at once, with the
// cancel hint, and does not try again.
func TestWaitLogonUseridInUse(t *testing.T) {
	addr, done := serveScript(t, useridInUseSteps(t))
	slept := 0
	_, tries, err := WaitLogon(context.Background(), addr, testOptions, "TSTUSR0", fakePassword,
		time.Now().Add(30*time.Second), func(time.Duration) { slept++ })
	var inUse *UseridInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("got %v, want *UseridInUseError", err)
	}
	if tries != 1 || slept != 0 {
		t.Errorf("%d tries, slept %d times; want one try, no sleep", tries, slept)
	}
	scriptResult(t, done)
}

// Without a deadline WaitLogon tries once, even after a failure it would
// otherwise try again.
func TestWaitLogonNoDeadline(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	slept := 0
	_, tries, err := WaitLogon(context.Background(), addr, testOptions, "herc01", fakePassword, time.Time{}, func(time.Duration) { slept++ })
	if err == nil || tries != 1 || slept != 0 {
		t.Errorf("err %v, %d tries, slept %d times; want an error, one try, no sleep", err, tries, slept)
	}
}

// What WaitLogon tries again after, and what it does not.
func TestPermanent(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&UseridInUseError{User: "HERC01"}, true},
		{&LogonError{Reason: "IKJ56421I PASSWORD NOT AUTHORIZED FOR USERID HERC01"}, true},
		{&LogonError{Reason: "LOGON REJECTED"}, true},
		{fmt.Errorf("%w: EOF", ErrLogoffUnconfirmed), true},
		{&LogonError{Reason: "no READY before the deadline; last step: waiting for the first screen"}, false},
		{&LogonError{Reason: "tn3270: connection closed"}, false},
		{errors.New("tn3270: dial tcp 127.0.0.1:3270: connect: connection refused"), false},
		{fmt.Errorf("tn3270: lab:3270: waiting for the first screen: %w", ErrTimeout), false},
	} {
		if got := permanent(c.err); got != c.want {
			t.Errorf("permanent(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
