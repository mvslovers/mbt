package target

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Probe is one access of a target and how it answered.
type Probe struct {
	Access string // mvsmf, hercules, tn3270, ssh
	Addr   string
	OK     bool
	Detail string
}

func httpAnswers(u string, timeout time.Duration) (int, time.Duration, error) {
	t0 := time.Now()
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "mbt/3")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return 0, 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, time.Since(t0), nil
}

func short(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i < 60 {
		return s[i+2:]
	}
	return s
}

// PingMVSMF asks mvsMF without credentials: any HTTP answer -- the 401 of
// /zosmf/info included -- means it is there.
func PingMVSMF(t *Target, timeout time.Duration) Probe {
	p := Probe{Access: "mvsmf", Addr: t.MVSMF.URL}
	code, d, err := httpAnswers(strings.TrimRight(t.MVSMF.URL, "/")+"/zosmf/info", timeout)
	if err != nil {
		p.Detail = "no answer: " + short(err)
		return p
	}
	p.OK, p.Detail = true, fmt.Sprintf("answering (HTTP %d, %d ms)", code, d.Milliseconds())
	return p
}

// Ping asks every access of t, logging on nowhere.
func Ping(t *Target, timeout time.Duration) []Probe {
	ps := []Probe{PingMVSMF(t, timeout)}
	if t.Hercules != nil {
		p := Probe{Access: "hercules", Addr: t.Hercules.URL}
		code, d, err := httpAnswers(strings.TrimRight(t.Hercules.URL, "/")+"/", timeout)
		if err != nil {
			p.Detail = "no answer: " + short(err)
		} else {
			p.OK, p.Detail = true, fmt.Sprintf("answering (HTTP %d, %d ms)", code, d.Milliseconds())
		}
		ps = append(ps, p)
	}
	port := func(access, host string, n int) Probe {
		addr := net.JoinHostPort(host, strconv.Itoa(n))
		p := Probe{Access: access, Addr: addr}
		t0 := time.Now()
		c, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			p.Detail = "closed: " + short(err)
			return p
		}
		c.Close()
		p.OK, p.Detail = true, fmt.Sprintf("open (%d ms)", time.Since(t0).Milliseconds())
		return p
	}
	if t.TN3270 != nil {
		ps = append(ps, port("tn3270", t.TN3270.Host, t.TN3270.Port))
	}
	if t.SSH != nil {
		ps = append(ps, port("ssh", t.SSH.Host, t.SSH.Port))
	}
	return ps
}

// Wait asks mvsMF until it answers or wait runs out; it returns the last
// probe and how long it took.
func Wait(t *Target, wait time.Duration, sleep func(time.Duration)) (Probe, time.Duration) {
	t0 := time.Now()
	for {
		p := PingMVSMF(t, 5*time.Second)
		if p.OK || time.Since(t0) >= wait {
			return p, time.Since(t0)
		}
		sleep(2 * time.Second)
	}
}

// HerculesInfo logs on to the Hercules web console (HTTP Basic, if the
// target has credentials) and reports the status and the server it names.
func HerculesInfo(e *Endpoint, timeout time.Duration) Probe {
	p := Probe{Access: "hercules", Addr: e.URL}
	req, err := http.NewRequest("GET", strings.TrimRight(e.URL, "/")+"/", nil)
	if err != nil {
		p.Detail = err.Error()
		return p
	}
	if e.User != "" {
		pw, err := e.Password.Resolve()
		if err != nil {
			p.Detail = err.Error()
			return p
		}
		req.SetBasicAuth(e.User, pw)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		p.Detail = "no answer: " + short(err)
		return p
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	switch {
	case resp.StatusCode == 401:
		p.Detail = "answering, but the logon was refused (HTTP 401)"
	case resp.StatusCode >= 400:
		p.Detail = fmt.Sprintf("answering, HTTP %d", resp.StatusCode)
	default:
		p.OK = true
		p.Detail = "logged on"
		if s := resp.Header.Get("Server"); s != "" {
			p.Detail += " -- " + s
		}
	}
	return p
}
