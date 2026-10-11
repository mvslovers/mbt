package mvsmf

import (
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// authStub answers like httpd after #288: every logon is a session of its
// own, a logoff ends only that one; Basic requests are answered with a
// cookie (mvsmf#324); no credentials is a 401.
func authStub(t *testing.T) (string, int, *sync.Map) {
	var mu sync.Mutex
	n := 0
	sessions := &sync.Map{}
	basicOK := func(r *http.Request) bool {
		a := r.Header.Get("Authorization")
		dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "Basic "))
		return strings.HasPrefix(a, "Basic ") && (string(dec) == "IBMUSER:sys1" || string(dec) == "TEST2:pw2")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zosmf/services/authenticate" {
			switch r.Method {
			case "POST":
				if !basicOK(r) {
					w.WriteHeader(401)
					return
				}
				mu.Lock()
				n++
				tok := "tok" + strconv.Itoa(n)
				mu.Unlock()
				sessions.Store(tok, true)
				http.SetCookie(w, &http.Cookie{Name: "LtpaToken2", Value: tok})
			case "DELETE":
				ck, err := r.Cookie("LtpaToken2")
				if _, ok := sessions.Load(ck.Value); err != nil || !ok {
					w.WriteHeader(401)
					return
				}
				sessions.Delete(ck.Value)
				w.WriteHeader(204)
			}
			return
		}
		if ck, err := r.Cookie("LtpaToken2"); err == nil {
			if _, ok := sessions.Load(ck.Value); !ok {
				w.WriteHeader(401)
				return
			}
		} else if basicOK(r) {
			http.SetCookie(w, &http.Cookie{Name: "LtpaToken2", Value: "shared"})
		} else {
			w.WriteHeader(401)
			return
		}
		if r.Method == "PUT" {
			w.WriteHeader(201)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	return host, p, sessions
}

// mbt#239: status and headers as the server sent them; private sessions
// that do not log on again; Basic, anonymous and token-only clients.
func TestAuthRequests(t *testing.T) {
	host, port, _ := authStub(t)

	// the real status, not 200 for every success
	c := New(host, port, "IBMUSER", "sys1")
	code, _, h, err := c.RequestHeaders("PUT", "/restjobs/jobs", "text/plain", []byte("x"))
	if err != nil || code != 201 {
		t.Errorf("PUT: %d %v", code, err)
	}
	// a Basic request is answered with a cookie
	if !strings.Contains(h.Get("Set-Cookie"), "LtpaToken2=shared") {
		t.Errorf("Basic: headers %v", h)
	}
	if code, _, _, _ := New(host, port, "IBMUSER", "WRONG").RequestHeaders("GET", "/info", "", nil); code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if code, _, _, _ := NewAnonymous(host, port).RequestHeaders("GET", "/info", "", nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}

	// two sessions of one user: a logoff ends only its own
	s1, s2 := New(host, port, "IBMUSER", "sys1"), New(host, port, "IBMUSER", "sys1")
	s1.NoRelogin, s2.NoRelogin = true, true
	if s1.Login() != nil || s2.Login() != nil || s1.Token() == s2.Token() {
		t.Fatalf("logons: %q %q", s1.Token(), s2.Token())
	}
	tok1 := s1.Token()
	if code, err := s1.LogoutStatus(); code != 204 || err != nil {
		t.Errorf("logoff: %d %v", code, err)
	}
	if code, _, _, _ := s2.RequestHeaders("GET", "/info", "", nil); code != 200 {
		t.Errorf("second session after the first's logoff: %d", code)
	}
	// the logged-off token is dead, and NoRelogin keeps the 401 visible
	if code, _, _, _ := NewWithToken(host, port, tok1).RequestHeaders("GET", "/info", "", nil); code != 401 {
		t.Errorf("dead token: %d", code)
	}
	if code, err := NewWithToken(host, port, tok1).LogoutStatus(); code != 401 || err != nil {
		t.Errorf("logoff of a dead token: %d %v", code, err)
	}
	if _, err := s1.LogoutStatus(); err == nil {
		t.Error("a second logoff of one client must say there is no session")
	}
}

// mbt#246: an error status comes back with its body, as the server sent it.
func TestErrorStatusKeepsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"returnCode":8,"reasonCode":1,"message":"Login failed."}`))
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	code, body, h, err := NewAnonymous(host, p).RequestHeaders("POST", "/services/authenticate", "", nil)
	if code != 401 || err == nil || string(body) != `{"returnCode":8,"reasonCode":1,"message":"Login failed."}` || h.Get("Content-Type") != "application/json" {
		t.Errorf("%d %q %v %v", code, body, h, err)
	}
}
