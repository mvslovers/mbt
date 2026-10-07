package deps

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// HTTPStatus says why GitHub answered resp with something other than 200,
// in words: a 403/429 with no requests left is the rate limit (60 an hour
// without a token, shared by everything on the machine), and says how to
// lift it; anything else is its HTTP status.
func HTTPStatus(resp *http.Response) string {
	if (resp.StatusCode == 403 || resp.StatusCode == 429) && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		reset := ""
		if s, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			reset = ", until " + time.Unix(s, 0).Format("15:04")
		}
		return fmt.Sprintf("GitHub API rate limit reached (HTTP %d%s) -- set GITHUB_TOKEN, e.g. GITHUB_TOKEN=$(gh auth token)", resp.StatusCode, reset)
	}
	return fmt.Sprintf("GitHub API HTTP %d", resp.StatusCode)
}
