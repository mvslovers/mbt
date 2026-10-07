package deps

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A GitHub that has run out of anonymous requests: 403 with
// X-RateLimit-Remaining 0. The error says so and names GITHUB_TOKEN, instead
// of "Cannot find release" for a release that exists.
func TestRateLimitIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1791374400")
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("MBT_GITHUB_TOKEN", "")
	o := Options{API: srv.URL, Cache: t.TempDir(), Warn: func(string) {}}
	_, err := fetch(o, "o", "r", "1.0.0", "r-1.0.0-lib.tar.gz", false)
	if err == nil || !strings.Contains(err.Error(), "rate limit") || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("fetch: %v", err)
	}
	_, err = resolve(o, "o", "r", ">=1.0.0")
	if err == nil || !strings.Contains(err.Error(), "rate limit") || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("resolve: %v", err)
	}
	os.RemoveAll(filepath.Join(o.Cache))
}
