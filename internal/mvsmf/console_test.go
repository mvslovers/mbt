package mvsmf

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A console answer decides whether the next channel may send the command
// again (mvsMF docs/endpoints/console/issue-command.md): 429/8/16 and
// 503/8/17 were refused before SVC 34; 503/8/15 was issued; anything else
// at 5xx -- an empty body included, as seen on mvsdev 2026-10-10 for
// "P HTTPD" -- may have run, and a resend could run an S twice.
func TestConsoleDelivery(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   Delivery
	}{
		{200, `{"cmd-response":"IEE136I LOCAL: TIME=11.12.13"}`, Delivered},
		{400, `{"return-code":1,"reason-code":12,"reason":"bad JSON"}`, NotDelivered},
		{401, ``, NotDelivered},
		{429, `{"return-code":8,"reason-code":16,"reason":"busy"}`, NotDelivered},
		{503, `{"return-code":8,"reason-code":17,"reason":"quiescing, not issued"}`, NotDelivered},
		{503, `{"return-code":8,"reason-code":15,"reason":"issued, capture abandoned"}`, Unknown},
		{500, `{"return-code":8,"reason-code":14,"reason":"no response"}`, Unknown},
		{503, ``, Unknown},
		{502, `<html>bad gateway</html>`, Unknown},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte(c.body))
		}))
		port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
		_, got, _ := New("127.0.0.1", port, "u", "p").Console("MBT", "P HTTPD", 5*time.Second)
		srv.Close()
		if got != c.want {
			t.Errorf("HTTP %d %q: delivery %v, want %v", c.status, c.body, got, c.want)
		}
	}
}
