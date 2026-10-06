// Package console issues MVS operator commands through a chain of channels
// (internals/mbt-3-extensions.md §3): mvsMF first, the Hercules web console
// as the fallback -- for when HTTPD or mvsMF are down, or are the very thing
// being deployed.
//
// The rule that keeps a fallback safe: the next channel is tried only when
// the previous one certainly did not deliver (no connection, an HTTP error).
// A command that was sent and then went unanswered may have run; trying it
// again could run it twice (S HTTPD), so the chain stops and says so. An
// answer from MVS rejecting the command is an answer, not a failure.
package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/mvsmf"
)

// Channel is one way to the operator console.
type Channel interface {
	Name() string
	Send(cmd string) ([]string, mvsmf.Delivery, error)
}

// Result of a command: the channel that delivered it and the reply.
type Result struct {
	Channel string
	Lines   []string
}

// Chain tries its channels in order.
type Chain struct {
	Channels []Channel
	Log      func(string)
}

// Send issues cmd on the first channel that takes it.
func (c *Chain) Send(cmd string) (Result, error) {
	if len(c.Channels) == 0 {
		return Result{}, errors.New("console: no channel configured")
	}
	var tried []string
	for _, ch := range c.Channels {
		lines, d, err := ch.Send(cmd)
		switch d {
		case mvsmf.Delivered:
			return Result{Channel: ch.Name(), Lines: lines}, nil
		case mvsmf.Unknown:
			return Result{Channel: ch.Name()}, fmt.Errorf("console: %q went out through %s and got no answer -- it may have run; not sent again: %v", cmd, ch.Name(), err)
		}
		tried = append(tried, fmt.Sprintf("%s: %v", ch.Name(), err))
		if c.Log != nil {
			c.Log(fmt.Sprintf("console: %s did not take %q (%v)", ch.Name(), cmd, err))
		}
	}
	return Result{}, fmt.Errorf("console: %q not delivered (%s)", cmd, strings.Join(tried, "; "))
}

// MVSMF is the channel through mvsMF's console API.
type MVSMF struct {
	Client  func() (*mvsmf.Client, error) // the session, opened on first use
	Console string                        // console name, 2-8 characters
	Timeout time.Duration
}

func (m *MVSMF) Name() string { return "mvsmf" }

func (m *MVSMF) Send(cmd string) ([]string, mvsmf.Delivery, error) {
	c, err := m.Client()
	if err != nil {
		return nil, mvsmf.NotDelivered, err
	}
	name, t := m.Console, m.Timeout
	if name == "" {
		name = "MBT"
	}
	if t == 0 {
		t = 30 * time.Second
	}
	return c.Console(name, cmd, t)
}

// Hercules is the channel through the Hercules web console: the command goes
// to the guest's console with a "/" prefix (/cgi-bin/api/v1/syslog?command=),
// and MVS answers asynchronously into the syslog, which is read back until
// the reply has settled.
type Hercules struct {
	URL, User, Password string
	Timeout             time.Duration
	Settle              time.Duration // how long to read the syslog for the reply
	Sleep               func(time.Duration)
}

func (h *Hercules) Name() string { return "hercules" }

func (h *Hercules) get(path string, q url.Values) ([]byte, int, error) {
	t := h.Timeout
	if t == 0 {
		t = 15 * time.Second
	}
	req, err := http.NewRequest("GET", strings.TrimRight(h.URL, "/")+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	if h.User != "" {
		req.SetBasicAuth(h.User, h.Password)
	}
	resp, err := (&http.Client{Timeout: t}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

type syslog struct {
	Syslog []string `json:"syslog"`
}

func (h *Hercules) Send(cmd string) ([]string, mvsmf.Delivery, error) {
	data, code, err := h.get("/cgi-bin/api/v1/syslog", url.Values{"command": {"/" + cmd}, "msgcount": {"0"}})
	if err != nil {
		var oe *net.OpError
		if errors.As(err, &oe) && oe.Op == "dial" {
			return nil, mvsmf.NotDelivered, fmt.Errorf("no connection to %s: %v", h.URL, oe.Err)
		}
		return nil, mvsmf.Unknown, err
	}
	if code >= 400 {
		return nil, mvsmf.NotDelivered, fmt.Errorf("HTTP %d: %s", code, strings.TrimSpace(string(data)))
	}
	// the command ran; read what MVS answered
	settle, sleep := h.Settle, h.Sleep
	if settle == 0 {
		settle = 3 * time.Second
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	var reply []string
	for waited := time.Duration(0); waited < settle; waited += 500 * time.Millisecond {
		sleep(500 * time.Millisecond)
		data, code, err := h.get("/cgi-bin/api/v1/syslog", url.Values{"msgcount": {"200"}})
		if err != nil || code >= 400 {
			break
		}
		var s syslog
		if json.Unmarshal(data, &s) != nil {
			break
		}
		reply = after(s.Syslog, cmd)
	}
	return reply, mvsmf.Delivered, nil
}

// after returns the syslog lines that follow the last echo of the typed
// command ("/D T") -- not merely the last line mentioning it: a reply may
// repeat the command text. (How Hercules echoes a "/" command was taken from
// its source, not yet seen on a live web console.)
func after(lines []string, cmd string) []string {
	var flat []string
	for _, l := range lines {
		for _, x := range strings.Split(l, "\n") {
			if strings.TrimSpace(x) != "" {
				flat = append(flat, strings.TrimRight(x, " \r"))
			}
		}
	}
	last := -1
	for i, l := range flat {
		if strings.HasSuffix(strings.TrimSpace(l), "/"+cmd) {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	return flat[last+1:]
}
