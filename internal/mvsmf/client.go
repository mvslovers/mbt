// Package mvsmf talks to MVS through mvsMF's z/OSMF-compatible REST API,
// as mbt v2's scripts/mbt/mvsmf.py does.
//
// Requests go out as HTTP/1.0, as v2 forces them, written by hand on the
// connection: Go's client always speaks HTTP/1.1, and what mvsMF does with
// that has not been measured.  Paths are percent-encoded exactly as Python's
// urllib.parse.quote(path, safe='/?=&()') does, so '#', '$' and '@' in a
// dataset name travel the same way.
package mvsmf

import (
	"bufio"
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
	host string
	port int
	auth string
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
}

func (c *Client) do(r request) ([]byte, error) {
	if r.timeout == 0 {
		r.timeout = 30 * time.Second
	}
	if r.accept == "" {
		r.accept = "application/json"
	}
	path := "/zosmf" + quote(r.path)
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := net.DialTimeout("tcp", addr, r.timeout)
	if err != nil {
		return nil, errf("Connection failed to http://%s%s: %v", addr, path, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(r.timeout))
	var req bytes.Buffer
	fmt.Fprintf(&req, "%s %s HTTP/1.0\r\n", r.method, path)
	fmt.Fprintf(&req, "Host: %s\r\n", addr)
	fmt.Fprintf(&req, "Authorization: Basic %s\r\n", c.auth)
	fmt.Fprintf(&req, "Accept: %s\r\n", r.accept)
	fmt.Fprintf(&req, "User-Agent: mbt/3\r\n")
	if r.body != nil {
		fmt.Fprintf(&req, "Content-Type: %s\r\n", r.contentType)
		fmt.Fprintf(&req, "Content-Length: %d\r\n", len(r.body))
	}
	for k, v := range r.extra {
		fmt.Fprintf(&req, "%s: %s\r\n", k, v)
	}
	req.WriteString("Connection: close\r\n\r\n")
	req.Write(r.body)
	if _, err := conn.Write(req.Bytes()); err != nil {
		return nil, errf("Connection lost during %s %s: %v", r.method, r.path, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, errf("No response to %s %s within %ds (read timeout)", r.method, r.path, int(r.timeout.Seconds()))
		}
		return nil, errf("Connection lost during %s %s: %v", r.method, r.path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errf("Connection lost during %s %s: %v", r.method, r.path, err)
	}
	if resp.StatusCode >= 400 {
		msg := fmt.Sprintf("HTTP %d %s for %s %s", resp.StatusCode, http.StatusText(resp.StatusCode), r.method, r.path)
		if len(body) > 0 {
			msg += ": " + string(body)
		}
		return nil, &Error{msg}
	}
	return body, nil
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
		return false
	}
	items, _ := m["items"].([]any)
	for _, it := range items {
		im, _ := it.(map[string]any)
		if strings.TrimSpace(strOr(im["dsname"], "")) == dsn {
			return true
		}
	}
	return false
}

// CreateDataset allocates a dataset; space is [unit, primary, secondary(,dirblk)].
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
