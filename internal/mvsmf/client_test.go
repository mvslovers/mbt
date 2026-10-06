package mvsmf

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
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
