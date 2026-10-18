package target

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/tn3270"
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

// WaitTSO logs on to TSO as the [tn3270] user of t, up to READY, and off
// again (tn3270.WaitLogon), trying until deadline when it is set: mvsMF
// answering does not mean TSO takes logons yet (mbt#284).  The error is a
// configuration error -- no [tn3270], no password, a bad size --; how the
// logon went is in the probe.  trace, if set, gets the 3270 stream.
func WaitTSO(t *Target, deadline time.Time, trace io.Writer, sleep func(time.Duration)) (Probe, error) {
	if t.TN3270 == nil {
		return Probe{}, errf("target %s has no [target.%s.tn3270] -- the TSO probe needs a TN3270 user", t.Name, t.Name)
	}
	pw, err := t.TN3270.Password.Resolve()
	if err != nil {
		return Probe{}, err
	}
	opt := tn3270.Options{Size: tn3270.Size{Cols: tn3270.Cols, Rows: tn3270.Rows}, Trace: trace}
	if t.TN3270.Size != "" {
		if opt.Size, err = tn3270.ParseSize(t.TN3270.Size); err != nil {
			return Probe{}, errf("[target.%s.tn3270] size: %v", t.Name, err)
		}
	}
	p := Probe{Access: "tso", Addr: t.TN3270.Addr()}
	t0 := time.Now()
	took, tries, err := tn3270.WaitLogon(context.Background(), p.Addr, opt, t.TN3270.User, pw, deadline, sleep)
	after := ""
	if tries > 1 {
		after = fmt.Sprintf(" after %d tries in %s", tries, time.Since(t0).Round(time.Second))
	}
	if err != nil {
		p.Detail = fmt.Sprintf("no logon as %s%s: %v", strings.ToUpper(t.TN3270.User), after, err)
		return p, nil
	}
	p.OK, p.Detail = true, fmt.Sprintf("logged on as %s in %d ms%s, logged off", strings.ToUpper(t.TN3270.User), took.Milliseconds(), after)
	return p, nil
}

// HerculesInfo logs on to the Hercules web console (HTTP Basic) and reports
// what it names about itself, as the mvsMF line does: the Hercules version
// and build date (/cgi-bin/api/v1/version) and how many 3270 devices have a
// client (/cgi-bin/api/v1/devices) -- the ones mbt test --tso competes for.
func HerculesInfo(e *Endpoint, timeout time.Duration) Probe {
	p := Probe{Access: "hercules", Addr: e.URL}
	pw := ""
	if e.User != "" {
		var err error
		if pw, err = e.Password.Resolve(); err != nil {
			p.Detail = err.Error()
			return p
		}
	}
	get := func(path string, v any) (int, error) {
		req, err := http.NewRequest("GET", strings.TrimRight(e.URL, "/")+path, nil)
		if err != nil {
			return 0, err
		}
		if e.User != "" {
			req.SetBasicAuth(e.User, pw)
		}
		resp, err := (&http.Client{Timeout: timeout}).Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			io.Copy(io.Discard, resp.Body)
			return resp.StatusCode, nil
		}
		return 200, json.NewDecoder(resp.Body).Decode(v)
	}
	var ver struct {
		Version   string `json:"hercules_version"`
		BuildDate string `json:"build_date"`
	}
	code, err := get("/cgi-bin/api/v1/version", &ver)
	switch {
	case err != nil && code == 0:
		p.Detail = "no answer: " + short(err)
		return p
	case code == 401 && e.User == "":
		p.Detail = "answering, but it asks for a logon and no user configured (HTTP 401)"
		return p
	case code == 401:
		p.Detail = fmt.Sprintf("answering, but the logon as %s was refused (HTTP 401)", e.User)
		return p
	case code != 200:
		p.Detail = fmt.Sprintf("answering, HTTP %d on /cgi-bin/api/v1/version", code)
		return p
	}
	p.OK = true
	if e.User != "" {
		p.Detail = fmt.Sprintf("logged on as %s (HTTP 200)", e.User)
	} else {
		p.Detail = "answering without a logon (HTTP 200)"
	}
	if ver.Version != "" {
		p.Detail += " -- Hercules " + ver.Version
		if d := strings.Join(strings.Fields(ver.BuildDate), " "); d != "" {
			p.Detail += ", built " + d
		}
	}
	var dev struct {
		Devices []struct {
			Type   string `json:"devtype"`
			Status string `json:"status"`
		} `json:"devices"`
	}
	if code, err := get("/cgi-bin/api/v1/devices", &dev); code == 200 && err == nil {
		all, open := 0, 0
		for _, d := range dev.Devices {
			if d.Type == "3270" {
				all++
				if strings.TrimSpace(d.Status) == "open" {
					open++
				}
			}
		}
		if all > 0 {
			p.Detail += fmt.Sprintf("; 3270 terminals: %d of %d connected", open, all)
		}
	}
	return p
}
