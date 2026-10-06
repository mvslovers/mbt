package tn3270

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// TestTSOIntegration logs on to a real TSO, runs TIME twice and logs off.  It runs
// only when MBT_TN3270_TEST is set, against MBT_TN3270_HOST (host or
// host:port, default port 3270) as MBT_TN3270_USER / MBT_TN3270_PASS.
// MBT_TN3270_TRACE=1 traces the session to stderr (the password is never
// traced).
func TestTSOIntegration(t *testing.T) {
	if os.Getenv("MBT_TN3270_TEST") == "" {
		t.Skip("MBT_TN3270_TEST not set")
	}
	host, user, pass := os.Getenv("MBT_TN3270_HOST"), os.Getenv("MBT_TN3270_USER"), os.Getenv("MBT_TN3270_PASS")
	if host == "" || user == "" || pass == "" {
		t.Fatal("MBT_TN3270_TEST needs MBT_TN3270_HOST, MBT_TN3270_USER and MBT_TN3270_PASS")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "3270")
	}
	opt := Options{}
	if os.Getenv("MBT_TN3270_TRACE") != "" {
		opt.Trace = os.Stderr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	start := time.Now()
	s, err := Dial(ctx, host, opt)
	if err != nil {
		t.Fatal(err)
	}
	// Close logs off if a step below fails after the logon
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	t.Logf("first screen after %s", time.Since(start).Round(time.Millisecond))

	if err := s.LogonTSO(ctx, user, pass); err != nil {
		var inUse *UseridInUseError
		if errors.As(err, &inUse) {
			t.Fatalf("%v", err)
		}
		t.Fatal(err)
	}
	t.Logf("READY after %s", time.Since(start).Round(time.Millisecond))

	timeTwice(t, s, 15*time.Second)
	if err := s.LogoffTSO(); err != nil {
		t.Fatal(err)
	}
	t.Logf("logged off after %s", time.Since(start).Round(time.Millisecond))
}

// TestTSOIntegrationInUse logs on, then tries a second logon with the same
// userid while the first session holds it: the second must end in
// *UseridInUseError, and the first must still log off.
//
// It runs only when MBT_TN3270_TEST_INUSE is set as well.  Its first run,
// before LogonTSO answered the IKJ56400A prompt with LOGOFF, dropped the
// second connection at that prompt; afterwards D TS,L listed one user
// STARTING and new connections got no logon screen after the Hercules
// banner.  Whether the LOGOFF answer avoids that has not been measured.
func TestTSOIntegrationInUse(t *testing.T) {
	if os.Getenv("MBT_TN3270_TEST") == "" || os.Getenv("MBT_TN3270_TEST_INUSE") == "" {
		t.Skip("MBT_TN3270_TEST and MBT_TN3270_TEST_INUSE not both set")
	}
	host, user, pass := os.Getenv("MBT_TN3270_HOST"), os.Getenv("MBT_TN3270_USER"), os.Getenv("MBT_TN3270_PASS")
	if host == "" || user == "" || pass == "" {
		t.Fatal("MBT_TN3270_TEST needs MBT_TN3270_HOST, MBT_TN3270_USER and MBT_TN3270_PASS")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "3270")
	}
	opt := Options{}
	if os.Getenv("MBT_TN3270_TRACE") != "" {
		opt.Trace = os.Stderr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	first, err := Dial(ctx, host, opt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Errorf("first session: %v", err)
		}
	}()
	if err := first.LogonTSO(ctx, user, pass); err != nil {
		t.Fatal(err)
	}

	second, err := Dial(ctx, host, opt)
	if err != nil {
		t.Fatal(err)
	}
	err = second.LogonTSO(ctx, user, pass)
	screen := second.Text()
	if cerr := second.Close(); cerr != nil {
		t.Errorf("second session: %v", cerr)
	}
	var inUse *UseridInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("second logon: got %v; screen:\n%s", err, screen)
	}
	t.Logf("second logon refused as expected; screen:\n%s", strings.TrimRight(inUse.Screen, "\n"))

	if err := first.LogoffTSO(); err != nil {
		t.Fatal(err)
	}
}
