package console

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mvslovers/mbt/internal/mvsmf"
)

type fake struct {
	name  string
	d     mvsmf.Delivery
	lines []string
	sent  []string
}

func (f *fake) Name() string { return f.name }
func (f *fake) Send(cmd string) ([]string, mvsmf.Delivery, error) {
	f.sent = append(f.sent, cmd)
	if f.d == mvsmf.Delivered {
		return f.lines, f.d, nil
	}
	return nil, f.d, errors.New(f.name + " down")
}

func TestChainRules(t *testing.T) {
	// first delivers: the second is never asked
	a, b := &fake{name: "mvsmf", d: mvsmf.Delivered, lines: []string{"IEE114I"}}, &fake{name: "hercules"}
	r, err := (&Chain{Channels: []Channel{a, b}}).Send("D T")
	if err != nil || r.Channel != "mvsmf" || len(b.sent) != 0 {
		t.Errorf("%+v %v %v", r, err, b.sent)
	}
	// first certainly not delivered: the fallback takes it
	a, b = &fake{name: "mvsmf", d: mvsmf.NotDelivered}, &fake{name: "hercules", d: mvsmf.Delivered}
	var log []string
	r, err = (&Chain{Channels: []Channel{a, b}, Log: func(s string) { log = append(log, s) }}).Send("S HTTPD")
	if err != nil || r.Channel != "hercules" || len(log) != 1 {
		t.Errorf("%+v %v %v", r, err, log)
	}
	// sent, no answer: stop -- never twice
	a, b = &fake{name: "mvsmf", d: mvsmf.Unknown}, &fake{name: "hercules", d: mvsmf.Delivered}
	_, err = (&Chain{Channels: []Channel{a, b}}).Send("S HTTPD")
	if err == nil || !strings.Contains(err.Error(), "may have run; not sent again") || len(b.sent) != 0 {
		t.Errorf("%v %v", err, b.sent)
	}
	// nobody takes it
	a, b = &fake{name: "mvsmf", d: mvsmf.NotDelivered}, &fake{name: "hercules", d: mvsmf.NotDelivered}
	if _, err = (&Chain{Channels: []Channel{a, b}}).Send("D T"); err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Errorf("%v", err)
	}
}

func client(t *testing.T, url string) *mvsmf.Client {
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(url, "http://"))
	p, _ := strconv.Atoi(port)
	return mvsmf.New(host, p, "U", "P")
}

func TestMVSMFChannel(t *testing.T) {
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "ok":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			json.NewEncoder(w).Encode(map[string]any{"cmd-response": "IEE136I LOCAL: TIME=12.00.00\r" + b["cmd"] + " ECHO\r"})
		case "500":
			w.WriteHeader(500)
		case "hang":
			time.Sleep(2 * time.Second)
		}
	}))
	defer srv.Close()
	ch := &MVSMF{Client: func() (*mvsmf.Client, error) { return client(t, srv.URL), nil }, Timeout: 500 * time.Millisecond}

	mode = "ok"
	lines, d, err := ch.Send("D T")
	if d != mvsmf.Delivered || err != nil || len(lines) != 2 || lines[0] != "IEE136I LOCAL: TIME=12.00.00" {
		t.Errorf("ok: %q %v %v", lines, d, err)
	}
	mode = "500"
	if _, d, _ = ch.Send("D T"); d != mvsmf.NotDelivered {
		t.Errorf("500: %v", d)
	}
	mode = "hang"
	if _, d, _ = ch.Send("D T"); d != mvsmf.Unknown {
		t.Errorf("timeout after sending: %v", d)
	}
	down := &MVSMF{Client: func() (*mvsmf.Client, error) { return client(t, "http://127.0.0.1:1"), nil }}
	if _, d, _ = down.Send("D T"); d != mvsmf.NotDelivered {
		t.Errorf("no connection: %v", d)
	}
}

// a stand-in for the Hercules web console, after cgibin.c: command runs a
// panel command, MVS answers into the syslog a little later.
func TestHerculesChannel(t *testing.T) {
	var mu sync.Mutex
	log := []string{"HHC01603I ipl 148", "IEE136I LOCAL: TIME=11.59.59"}
	var gotAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotAuth = ok && u == "admin" && p == "pw"
		if r.URL.Path != "/cgi-bin/api/v1/syslog" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if c := r.URL.Query().Get("command"); c != "" {
			log = append(log, c) // the console echoes the command
			cmd := strings.TrimPrefix(c, "/")
			go func() {
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				log = append(log, "IEE136I LOCAL: TIME=12.00.00 DATE=2026.279", "  ("+cmd+" done)")
				mu.Unlock()
			}()
		}
		json.NewEncoder(w).Encode(map[string]any{"command": "", "msgcount": 200, "syslog": log})
	}))
	defer srv.Close()
	h := &Hercules{URL: srv.URL, User: "admin", Password: "pw", Settle: time.Second}
	lines, d, err := h.Send("D T")
	if d != mvsmf.Delivered || err != nil || !gotAuth {
		t.Fatalf("%v %v auth=%v", d, err, gotAuth)
	}
	if strings.Join(lines, "|") != "IEE136I LOCAL: TIME=12.00.00 DATE=2026.279|  (D T done)" {
		t.Errorf("reply: %q", lines)
	}
	down := &Hercules{URL: "http://127.0.0.1:1"}
	if _, d, _ = down.Send("D T"); d != mvsmf.NotDelivered {
		t.Errorf("no connection: %v", d)
	}
}
