package mvsmf

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

// A server that restarted (HTTPD stopped and started by mbt run restart)
// no longer knows the session's token: the client logs on again once and
// repeats the request, and a logoff of a session the server has already
// forgotten is no failure.
func TestReloginAfterServerRestart(t *testing.T) {
	var token atomic.Value
	token.Store("t1")
	var logons, puts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zosmf/services/authenticate" && r.Method == "POST" {
			n := atomic.AddInt32(&logons, 1)
			tok := "t" + strconv.Itoa(int(n))
			token.Store(tok)
			http.SetCookie(w, &http.Cookie{Name: "LtpaToken2", Value: tok})
			return
		}
		ck, err := r.Cookie("LtpaToken2")
		if err != nil || ck.Value != token.Load().(string) {
			w.WriteHeader(401)
			return
		}
		if r.Method == "PUT" {
			atomic.AddInt32(&puts, 1)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := New(host, p, "u", "p")
	if err := c.Login(); err != nil {
		t.Fatal(err)
	}
	token.Store("restarted") // the server forgot every session
	code, _, err := c.Request("PUT", "/restjobs/jobs", "text/plain", []byte("//X JOB\n"))
	if err != nil || code != 200 {
		t.Fatalf("request after restart: %d %v", code, err)
	}
	if logons != 2 || puts != 1 {
		t.Errorf("logons %d (want 2), puts %d (want 1: sent once after the 401)", logons, puts)
	}
	token.Store("gone")
	if err := c.Logout(); err != nil {
		t.Errorf("logoff of a forgotten session: %v", err)
	}
}
