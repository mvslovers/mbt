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
