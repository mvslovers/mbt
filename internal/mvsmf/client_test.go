package mvsmf

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Paths reach the server encoded as urllib.parse.quote(safe='/?=&()') sent
// them: '#', '$' and '@' escaped, the query and member parentheses as is.
func TestPathEncoding(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.RequestURI+" auth="+r.Header.Get("Authorization"))
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := New(host, p, "U", "P")
	c.DeleteDataset("IBMUSER.IRX#HELO.$X@")
	c.DatasetExists("IBMUSER.A.B")
	c.do(request{method: "GET", path: "/restfiles/ds/A.B(MEM#1)"})
	want := []string{
		"/zosmf/restfiles/ds/IBMUSER.IRX%23HELO.%24X%40 auth=Basic VTpQ",
		"/zosmf/restfiles/ds?dslevel=IBMUSER.A auth=Basic VTpQ",
		"/zosmf/restfiles/ds/A.B(MEM%231) auth=Basic VTpQ",
	}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Errorf("request %d: got %q, want %q", i, got, w)
		}
	}
}

func TestQuoteMatchesUrllib(t *testing.T) {
	// python3 -c "import urllib.parse;print(urllib.parse.quote('/a b/#$@~_.-()?=&+%', safe='/?=&()'))"
	if got := quote("/a b/#$@~_.-()?=&+%"); got != "/a%20b/%23%24%40~_.-()?=&%2B%25" {
		t.Errorf("quote: %s", got)
	}
}

// CheckBasic: the logon of a client without a session is one GET /info
// with Basic credentials; 200 passes, 401 is a refused logon, and no
// session is ever opened (mvsmf#363).
func TestCheckBasic(t *testing.T) {
	var got []string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		w.WriteHeader(status)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := New(host, p, "U", "P")
	c.Sleep = func(time.Duration) {}
	if err := c.CheckBasic(); err != nil {
		t.Fatal(err)
	}
	c.DatasetExists("A.B")
	if err := c.Logout(); err != nil {
		t.Errorf("logout without a session: %v", err)
	}
	for _, g := range got {
		if strings.Contains(g, "/services/authenticate") || !strings.HasSuffix(g, "Basic VTpQ") {
			t.Errorf("request %q", g)
		}
	}
	if len(got) != 2 || got[0] != "GET /zosmf/info Basic VTpQ" {
		t.Errorf("requests %q", got)
	}
	status = 401
	if err := c.CheckBasic(); err == nil || !strings.Contains(err.Error(), "logon refused") {
		t.Errorf("401: %v", err)
	}
}

// collectSpool keeps the step each spool file belongs to, so a reader can
// count per step (#288); a failed fetch is a file marked Failed, and the
// concatenated spool is what CollectSpool returned before.
func TestCollectSpoolFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/zosmf/restjobs/jobs/MBTTEST/JOB00001/files":
			w.Write([]byte(`[{"id":2,"ddname":"JESMSGLG","stepname":"JES2"},{"id":102,"ddname":"SYSPRINT","stepname":"B01"},{"id":103,"ddname":"SYSTSPRT","stepname":"T01"}]`))
		case "/zosmf/restjobs/jobs/MBTTEST/JOB00001/files/2/records":
			w.Write([]byte("IEF142I\r\n"))
		case "/zosmf/restjobs/jobs/MBTTEST/JOB00001/files/102/records":
			w.Write([]byte("PASS: one\n"))
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := New(host, p, "U", "P")
	spool, errs, files := c.collectSpool("MBTTEST", "JOB00001", false)
	want := []SpoolFile{
		{DD: "JESMSGLG", Step: "JES2", Text: "IEF142I\n"},
		{DD: "SYSPRINT", Step: "B01", Text: "PASS: one\n"},
		{DD: "SYSTSPRT", Step: "T01", Failed: true},
	}
	if len(files) != len(want) {
		t.Fatalf("files %+v", files)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("file %d: %+v, want %+v", i, files[i], want[i])
		}
	}
	if len(errs) != 1 || !strings.HasPrefix(errs[0], "SYSTSPRT: HTTP 500") {
		t.Errorf("errors %q", errs)
	}
	s2, _ := c.CollectSpool("MBTTEST", "JOB00001", false)
	if s2 != spool || !strings.HasPrefix(spool, "--- JESMSGLG ---\nIEF142I\n\n--- SYSPRINT ---\nPASS: one\n\n--- SYSTSPRT --- [FAILED TO RETRIEVE: ") {
		t.Errorf("spool %q", spool)
	}
}
