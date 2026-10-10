// Package mvsmf talks to MVS through mvsMF's z/OSMF-compatible REST API,
// as mbt v2's scripts/mbt/mvsmf.py does.
//
// Requests go out with Go's standard client (HTTP/1.1, which mvsMF speaks;
// mbt 2 forced HTTP/1.0).  Paths are percent-encoded exactly as Python's
// urllib.parse.quote(path, safe='/?=&()') does, so '#', '$' and '@' in a
// dataset name travel the same way.
package mvsmf

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Error is a communication failure (exit 4).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Client is one mvsMF endpoint.
type Client struct {
	host  string
	port  int
	auth  string
	token string // the LtpaToken2 of a session (Login), sent instead of Basic
	// Sleep is the poll delay; tests replace it so a stub run does not wait.
	Sleep func(time.Duration)
}

// New returns a client for host:port with HTTP Basic credentials.
func New(host string, port int, user, pass string) *Client {
	return &Client{host: host, port: port,
		auth:  base64.StdEncoding.EncodeToString([]byte(user + ":" + pass)),
		Sleep: time.Sleep}
}

const safeChars = "/?=&()"

// quote is urllib.parse.quote(s, safe='/?=&()'): letters, digits, '_.-~'
// and the safe set stay; every other byte is %XX.
func quote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("_.-~", c) >= 0 || strings.IndexByte(safeChars, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

type request struct {
	method, path, contentType, accept string
	body                              []byte
	extra                             map[string]string
	timeout                           time.Duration
	noRelogin                         bool // the logoff, and the one repeat after a re-logon
}

func (c *Client) do(r request) ([]byte, error) {
	if r.timeout == 0 {
		r.timeout = 30 * time.Second
	}
	if r.accept == "" {
		r.accept = "application/json"
	}
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	url := "http://" + addr + "/zosmf" + quote(r.path)
	var body io.Reader
	if r.body != nil {
		body = bytes.NewReader(r.body)
	}
	req, err := http.NewRequest(r.method, url, body)
	if err != nil {
		return nil, errf("bad request %s %s: %v", r.method, r.path, err)
	}
	if c.token != "" {
		req.Header.Set("Cookie", "LtpaToken2="+c.token)
	} else {
		req.Header.Set("Authorization", "Basic "+c.auth)
	}
	req.Header.Set("Accept", r.accept)
	req.Header.Set("User-Agent", "mbt/3")
	if r.body != nil {
		req.Header.Set("Content-Type", r.contentType)
	}
	for k, v := range r.extra {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: r.timeout}).Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, errf("No response to %s %s within %ds (read timeout)", r.method, r.path, int(r.timeout.Seconds()))
		}
		var oe *net.OpError
		if errors.As(err, &oe) && oe.Op == "dial" {
			return nil, errf("Connection failed to %s: %v", url, oe.Err)
		}
		return nil, errf("Connection lost during %s %s: %v", r.method, r.path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errf("Connection lost during %s %s: %v", r.method, r.path, err)
	}
	// a server that restarted (mbt run restart stops and starts HTTPD)
	// has forgotten the session: log on again once and repeat. Safe for a
	// PUT as well -- mvsMF checks the logon before it acts on anything.
	if resp.StatusCode == 401 && c.token != "" && !r.noRelogin {
		if err := c.Login(); err == nil {
			r.noRelogin = true
			return c.do(r)
		}
	}
	if resp.StatusCode >= 400 {
		msg := fmt.Sprintf("HTTP %d %s for %s %s", resp.StatusCode, http.StatusText(resp.StatusCode), r.method, r.path)
		if len(data) > 0 {
			msg += ": " + string(data)
		}
		return nil, &Error{msg}
	}
	return data, nil
}

func (c *Client) jsonDo(method, path string, body any) (map[string]any, []any, error) {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	raw, err := c.do(request{method: method, path: path, body: data, contentType: "application/json"})
	if err != nil || len(raw) == 0 {
		return nil, nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, nil, errf("bad JSON from %s %s: %v", method, path, err)
	}
	switch x := v.(type) {
	case map[string]any:
		return x, nil, nil
	case []any:
		return nil, x, nil
	}
	return nil, nil, nil
}

// JobResult is a finished (or timed-out) job.
type JobResult struct {
	JobID, JobName string
	RC             int    // 0, 4, ...; 9999 ABEND/timeout; 9998 JCL error; -1 unknown
	Status         string // CC, ABEND, JCL ERROR, TIMEOUT, UNKNOWN, ACTIVE
	Spool          string
	SpoolErrors    []string
}

// SubmitOptions mirror v2's submit_jcl arguments.
type SubmitOptions struct {
	Timeout  time.Duration
	NoSpool  bool
	JESOnly  bool
	DontWait bool
}

var jesDDs = map[string]bool{"JESJCLIN": true, "JESMSGLG": true, "JESJCL": true, "JESYSMSG": true}

// Submit sends inline JCL and polls until the job is on the output queue,
// backing off 1, 2, 3, 5, 5, ... seconds.
func (c *Client) Submit(jcl string, o SubmitOptions) (*JobResult, error) {
	if o.Timeout == 0 {
		o.Timeout = 120 * time.Second
	}
	raw, err := c.do(request{method: "PUT", path: "/restjobs/jobs", body: []byte(jcl), contentType: "text/plain"})
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	json.Unmarshal(raw, &resp)
	name, id := strOr(resp["jobname"], "UNKNOWN"), strOr(resp["jobid"], "UNKNOWN")
	if o.DontWait {
		return &JobResult{JobID: id, JobName: name, RC: -1, Status: "ACTIVE"}, nil
	}
	backoff := []int{1, 2, 3, 5}
	elapsed, i := 0, 0
	for time.Duration(elapsed)*time.Second < o.Timeout {
		d := backoff[min(i, len(backoff)-1)]
		c.Sleep(time.Duration(d) * time.Second)
		elapsed += d
		i++
		st, _, err := c.jsonDo("GET", fmt.Sprintf("/restjobs/jobs/%s/%s", name, id), nil)
		if err != nil {
			continue
		}
		if st["status"] != "OUTPUT" {
			continue
		}
		rc, status := -1, "UNKNOWN"
		if rs, ok := st["retcode"].(string); ok {
			rc, status = ParseRetcode(rs)
		}
		var spool string
		var spoolErrs []string
		if !o.NoSpool {
			spool, spoolErrs = c.CollectSpool(name, id, o.JESOnly)
		}
		if rc == -1 && spool != "" {
			rc, status = ParseSpoolRC(spool)
		}
		return &JobResult{JobID: id, JobName: name, RC: rc, Status: status, Spool: spool, SpoolErrors: spoolErrs}, nil
	}
	spool, spoolErrs := c.CollectSpool(name, id, false)
	return &JobResult{JobID: id, JobName: name, RC: 9999, Status: "TIMEOUT", Spool: spool, SpoolErrors: spoolErrs}, nil
}

func strOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func oneLine(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}

// CollectSpool fetches the job's spool files; a failed fetch is reported in
// the error list rather than becoming an empty spool (#87).
func (c *Client) CollectSpool(name, id string, jesOnly bool) (string, []string) {
	m, l, err := c.jsonDo("GET", fmt.Sprintf("/restjobs/jobs/%s/%s/files", name, id), nil)
	if err != nil {
		return "", []string{oneLine(err)}
	}
	files := l
	if files == nil {
		if items, ok := m["items"].([]any); ok {
			files = items
		}
	}
	var parts, errs []string
	for _, f := range files {
		fm, _ := f.(map[string]any)
		fid := fmt.Sprint(fm["id"])
		if fm["id"] == nil || fid == "" {
			continue
		}
		if s, ok := fm["id"].(float64); ok {
			fid = strconv.FormatFloat(s, 'f', -1, 64)
		}
		dd := strOr(fm["ddname"], "")
		if jesOnly && !jesDDs[dd] {
			continue
		}
		raw, err := c.do(request{method: "GET", path: fmt.Sprintf("/restjobs/jobs/%s/%s/files/%s/records", name, id, fid), accept: "text/plain"})
		if err != nil {
			parts = append(parts, fmt.Sprintf("--- %s --- [FAILED TO RETRIEVE: %v]", dd, err))
			errs = append(errs, fmt.Sprintf("%s: %s", dd, oneLine(err)))
			continue
		}
		text := strings.ReplaceAll(strings.ToValidUTF8(string(raw), "�"), "\r", "")
		parts = append(parts, fmt.Sprintf("--- %s ---\n%s", dd, text))
	}
	return strings.Join(parts, "\n"), errs
}

// ParseRetcode reads z/OSMF's retcode: "CC 0004" -> 4, CC; "ABEND S0C4" ->
// 9999, ABEND; "JCL ERROR" -> 9998.
func ParseRetcode(s string) (int, string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "CC ") {
		if rc, err := strconv.Atoi(strings.TrimSpace(s[3:])); err == nil {
			return rc, "CC"
		}
		return 9999, "CC"
	}
	if strings.Contains(s, "ABEND") {
		return 9999, "ABEND"
	}
	if strings.Contains(s, "JCL ERROR") {
		return 9998, "JCL ERROR"
	}
	return 9999, "UNKNOWN"
}

var condCodeRE = regexp.MustCompile(`COND CODE\s+(\d+)`)

// ParseSpoolRC reads the RC from the spool when the API gives none, which is
// every job on MVS/CE's mvsMF.
func ParseSpoolRC(spool string) (int, string) {
	if m := condCodeRE.FindStringSubmatch(spool); m != nil {
		rc, _ := strconv.Atoi(m[1])
		return rc, "CC"
	}
	if strings.Contains(spool, "IEF452I") || strings.Contains(spool, "JCL ERROR") {
		return 9998, "JCL ERROR"
	}
	if strings.Contains(spool, "IEF472I") || strings.Contains(spool, "ABEND") {
		return 9999, "ABEND"
	}
	return -1, "UNKNOWN"
}

// DatasetExists searches by a prefix (at most two qualifiers, for MVS/CE)
// and looks for the exact name.
func (c *Client) DatasetExists(dsn string) bool {
	_, ok := c.DatasetVolume(dsn)
	return ok
}

// DatasetVolume says whether dsn is cataloged and on which volume ("" when
// the listing does not say).
func (c *Client) DatasetVolume(dsn string) (string, bool) {
	a, ok := c.DatasetAttrs(dsn)
	return a["vol"], ok
}

// DatasetAttrs is the data set list's entry for dsn, as strings: vol, dev,
// dsorg, recfm, blksz, spacu (TRACKS, CYLINDERS), sizex (allocated, in
// spacu), used (percent of it), extx (extents).
func (c *Client) DatasetAttrs(dsn string) (map[string]string, bool) {
	parts := strings.Split(dsn, ".")
	n := len(parts) - 1
	if n > 2 {
		n = 2
	}
	if n < 1 {
		n = 1
	}
	m, _, err := c.jsonDo("GET", "/restfiles/ds?dslevel="+strings.Join(parts[:n], "."), nil)
	if err != nil {
		return nil, false
	}
	items, _ := m["items"].([]any)
	for _, it := range items {
		im, _ := it.(map[string]any)
		if strings.TrimSpace(strOr(im["dsname"], "")) == dsn {
			a := map[string]string{}
			for k, v := range im {
				a[k] = strings.TrimSpace(strOr(v, ""))
			}
			return a, true
		}
	}
	return nil, false
}

// Members lists a PDS's member names.
func (c *Client) Members(dsn string) ([]string, error) {
	m, _, err := c.jsonDo("GET", "/restfiles/ds/"+dsn+"/member", nil)
	if err != nil {
		return nil, err
	}
	var out []string
	items, _ := m["items"].([]any)
	for _, it := range items {
		im, _ := it.(map[string]any)
		if n := strings.TrimSpace(strOr(im["member"], "")); n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

func (c *Client) CreateDataset(dsn, dsorg, recfm string, lrecl, blksize int, space []any, unit, volume string) error {
	body := map[string]any{"dsorg": dsorg, "alcunit": space[0], "primary": space[1], "secondary": space[2],
		"recfm": recfm, "lrecl": lrecl, "blksize": blksize, "unit": unit}
	if len(space) >= 4 {
		body["dirblk"] = space[3]
	}
	if volume != "" {
		body["vol"] = volume
	}
	_, _, err := c.jsonDo("POST", "/restfiles/ds/"+dsn, body)
	return err
}

// DeleteDataset deletes a dataset.
func (c *Client) DeleteDataset(dsn string) error {
	_, err := c.do(request{method: "DELETE", path: "/restfiles/ds/" + dsn, accept: "*/*"})
	return err
}

// UploadBinary writes data to a sequential dataset, binary.
func (c *Client) UploadBinary(dsn string, data []byte) error {
	t := len(data)/(1024*1024)*20 + 120
	if t < 120 {
		t = 120
	}
	_, err := c.do(request{method: "PUT", path: "/restfiles/ds/" + dsn, body: data,
		contentType: "application/octet-stream", accept: "*/*",
		extra: map[string]string{"X-IBM-Data-Type": "binary"}, timeout: time.Duration(t) * time.Second})
	return err
}

// Ping: any HTTP answer counts.
func (c *Client) Ping() bool {
	_, err := c.do(request{method: "GET", path: "/info", accept: "*/*"})
	if err == nil {
		return true
	}
	return strings.Contains(err.Error(), "HTTP ")
}

// Status asks path with the client's credentials and returns the HTTP status
// (0 when the server could not be reached at all).
func (c *Client) Status(path string) (int, error) {
	_, err := c.do(request{method: "GET", path: path, timeout: 5 * time.Second})
	if err == nil {
		return 200, nil
	}
	var code int
	if n, _ := fmt.Sscanf(err.Error(), "HTTP %d", &code); n == 1 {
		return code, nil
	}
	return 0, err
}

// Login opens a session: one logon (POST /zosmf/services/authenticate with
// the credentials), then every request carries the LtpaToken2 cookie
// instead of the password. Logout ends it; httpd also expires an idle
// session by itself (SESSION_TIMEOUT, 30 minutes by default).
func (c *Client) Login() error {
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	req, err := http.NewRequest("POST", "http://"+addr+"/zosmf/services/authenticate", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+c.auth)
	req.Header.Set("X-CSRF-ZOSMF-HEADER", "")
	req.Header.Set("User-Agent", "mbt/3")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return errf("Connection failed to %s: %v", addr, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == 401 {
		return errf("logon to mvsMF at %s refused (HTTP 401): user or password wrong", addr)
	}
	if resp.StatusCode != 200 {
		return errf("logon to mvsMF at %s: HTTP %d", addr, resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "LtpaToken2" && ck.Value != "" {
			c.token = ck.Value
			return nil
		}
	}
	return errf("logon to mvsMF at %s: no LtpaToken2 cookie in the answer", addr)
}

// Token is the session token ("" without a session).
func (c *Client) Token() string { return c.token }

// Logout ends the session (DELETE /zosmf/services/authenticate); without
// one it does nothing.
func (c *Client) Logout() error {
	if c.token == "" {
		return nil
	}
	_, err := c.do(request{method: "DELETE", path: "/services/authenticate", timeout: 10 * time.Second, noRelogin: true})
	c.token = ""
	var me *Error
	if errors.As(err, &me) && strings.HasPrefix(me.Msg, "HTTP 401") {
		return nil // the server has forgotten the session already (a restart)
	}
	return err
}

// Request is any mvsMF call within the session: path below /zosmf, an
// optional body; it returns the HTTP status and the body.
func (c *Client) Request(method, path, contentType string, body []byte) (int, []byte, error) {
	data, err := c.do(request{method: method, path: path, contentType: contentType, body: body})
	if err != nil {
		var me *Error
		if errors.As(err, &me) {
			var code int
			if _, scanErr := fmt.Sscanf(me.Msg, "HTTP %d", &code); scanErr == nil {
				return code, nil, err
			}
		}
		return 0, nil, err
	}
	return 200, data, nil
}

// consoleFailure classifies a console error answer by mvsMF's documented
// codes (docs/endpoints/console/issue-command.md). A 4xx is a refusal
// before the command (validation, logon, 429/8/16: another command in
// progress), and so is 503/8/17 (quiescing, not issued). Any other 5xx may
// come after the command ran -- 503/8/15 says so outright, 500/8/14 cannot
// tell, and an empty or foreign body tells nothing -- so it is Unknown:
// the next channel must not send it again (an S twice starts two servers).
func consoleFailure(status int, body []byte) Delivery {
	if status < 500 {
		return NotDelivered
	}
	var r struct {
		RC     int `json:"return-code"`
		Reason int `json:"reason-code"`
	}
	if json.Unmarshal(body, &r) == nil && status == 503 && r.RC == 8 && r.Reason == 17 {
		return NotDelivered
	}
	return Unknown
}

// Delivery says what became of an operator command on one channel.
type Delivery int

const (
	// Delivered: the channel took the command and answered.
	Delivered Delivery = iota
	// NotDelivered: certainly not issued (no connection, an HTTP error) --
	// another channel may try.
	NotDelivered
	// Unknown: sent, but no answer (a timeout after the request went out) --
	// it may have run; trying again could run it twice.
	Unknown
)

// Console issues an operator command through mvsMF's console API
// (PUT /zosmf/restconsoles/consoles/<name>) and returns its response lines.
func (c *Client) Console(name, cmd string, timeout time.Duration) ([]string, Delivery, error) {
	body, _ := json.Marshal(map[string]string{"cmd": cmd})
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	req, err := http.NewRequest("PUT", "http://"+addr+"/zosmf/restconsoles/consoles/"+name, bytes.NewReader(body))
	if err != nil {
		return nil, NotDelivered, err
	}
	if c.token != "" {
		req.Header.Set("Cookie", "LtpaToken2="+c.token)
	} else {
		req.Header.Set("Authorization", "Basic "+c.auth)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mbt/3")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		var oe *net.OpError
		if errors.As(err, &oe) && oe.Op == "dial" {
			return nil, NotDelivered, errf("mvsMF console: no connection to %s: %v", addr, oe.Err)
		}
		return nil, Unknown, errf("mvsMF console: %q sent, no answer: %v", cmd, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		d := consoleFailure(resp.StatusCode, data)
		msg := fmt.Sprintf("mvsMF console: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
		if d == Unknown {
			msg += " -- it may have been issued"
		}
		return nil, d, errf("%s", msg)
	}
	var r map[string]any
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, Delivered, nil // issued; the answer is not readable
	}
	text, _ := r["cmd-response"].(string)
	var lines []string
	for _, l := range strings.FieldsFunc(text, func(c rune) bool { return c == '\r' || c == '\n' }) {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimRight(l, " "))
		}
	}
	return lines, Delivered, nil
}
