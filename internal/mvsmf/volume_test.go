package mvsmf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The dataset list names the volume (mvsMF answers "vol", as z/OSMF does):
// a deploy reads it to RECEIVE a replaced library back onto it.
func TestDatasetVolume(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/restfiles/ds") || r.URL.Query().Get("dslevel") != "FTPD.DEV" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"dsname": "FTPD.DEV.LINKLIB", "vol": "PUB000", "dsorg": "PO"}}})
	}))
	defer srv.Close()
	port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	c := New("127.0.0.1", port, "u", "p")
	if vol, ok := c.DatasetVolume("FTPD.DEV.LINKLIB"); !ok || vol != "PUB000" {
		t.Errorf("got %q %v", vol, ok)
	}
	if _, ok := c.DatasetVolume("FTPD.DEV.OTHER"); ok {
		t.Error("a data set that is not listed exists")
	}
	if !c.DatasetExists("FTPD.DEV.LINKLIB") {
		t.Error("DatasetExists")
	}
}
