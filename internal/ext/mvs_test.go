package ext

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fakeConn is one connection Open handed out.
type fakeConn struct {
	a     Access
	tok   string
	calls *[]string
}

func (c *fakeConn) RequestHeaders(method, path, ct string, body []byte) (int, []byte, http.Header, error) {
	*c.calls = append(*c.calls, c.a.Mode+":"+c.a.User+" "+method+" "+path)
	h := http.Header{}
	h.Add("Set-Cookie", "LtpaToken2=a")
	h.Add("Set-Cookie", "Other=b")
	h.Set("X-Mode", c.a.Mode)
	if c.a.Password != nil && *c.a.Password == "WRONG" {
		return 401, nil, h, errors.New("HTTP 401")
	}
	if method == "PUT" {
		return 201, []byte("made"), h, nil
	}
	return 200, []byte("ok"), h, nil
}
func (c *fakeConn) Token() string { return c.tok }
func (c *fakeConn) LogoutStatus() (int, error) {
	if c.tok == "" {
		return 0, errors.New("no session")
	}
	*c.calls = append(*c.calls, "logout "+c.tok)
	c.tok = ""
	return 204, nil
}

// mbt#239: requests with Basic, none or a token, headers as a third value,
// private sessions as further users; nothing of it in a dry run.
func TestMVSAccess(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.command("auth", function(ctx)
  local code, body, h = ctx.mvs.request { path = "/info", auth = "basic" }
  assert(code == 200 and h["x-mode"] == "basic", "basic")
  assert(h["set-cookie"] == "LtpaToken2=a\nOther=b", h["set-cookie"])
  code = ctx.mvs.request { path = "/info", auth = "basic", user = "test2", password = "WRONG" }
  assert(code == 401, "wrong password " .. code)
  code = ctx.mvs.request { path = "/info", auth = "none" }
  code = ctx.mvs.request { path = "/info", auth = "token", token = "T9" }
  code = ctx.mvs.request { method = "PUT", path = "/restjobs/jobs", body = "x", auth = "basic" }
  assert(code == 201, "PUT " .. code)
  local s1 = ctx.mvs.session()
  local s2 = ctx.mvs.session { user = "test2" }
  assert(s1:token() == "S1" and s2:token() == "S2")
  assert(s1:logout() == 204)
  assert(s2:request { path = "/restfiles/ds?dslevel=X" } == 200)
  local ok, err = pcall(s1.request, s1, { path = "/info" })
  assert(not ok and tostring(err):find("logged off"), tostring(err))
end)
mbt.command("bad", function(ctx)
  local cases = {
    { { path = "/i", user = "t" }, "only with auth" },
    { { path = "/i", password = "p" }, "only with auth" },
    { { path = "/i", auth = "token" }, "needs token" },
    { { path = "/i", token = "x" }, "needs token" },
    { { path = "/i", auth = "magic" }, "use session" },
  }
  for _, c in ipairs(cases) do
    local ok, err = pcall(ctx.mvs.request, c[1])
    assert(not ok and tostring(err):find(c[2], 1, true), c[2] .. ": " .. tostring(err))
  end
  local s = ctx.mvs.session()
  local ok, err = pcall(s.request, s, { path = "/i", auth = "basic" })
  assert(not ok and tostring(err):find("takes no auth"), tostring(err))
  ok, err = pcall(ctx.mvs.session, { usr = "x" })
  assert(not ok and tostring(err):find("unknown key usr"), tostring(err))
end)`})
	var calls []string
	n := 0
	tweak := func(o *Options) {
		o.Open = func(a Access) (Conn, error) {
			c := &fakeConn{a: a, calls: &calls}
			if a.Mode == "session" {
				n++
				c.tok = "S" + string(rune('0'+n))
			}
			return c, nil
		}
	}
	e := v.mustLoad(t, tweak)
	if err := e.Run("auth", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Run("bad", nil); err != nil {
		t.Fatal(err)
	}
	// the sessions still open are logged off at the end: S2 and bad's S3
	e.CloseSessions()
	got := strings.Join(calls, "|")
	for _, want := range []string{"basic: GET /info", "basic:test2 GET /info", "none: GET /info", "token: GET /info",
		"basic: PUT /restjobs/jobs", "logout S1", "session:test2 GET /restfiles/ds?dslevel=X", "logout S2", "logout S3"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Count(got, "logout S1") != 1 {
		t.Errorf("S1 logged off twice: %s", got)
	}

	// dry run: no connection is opened, nothing but a GET would go out
	calls, n = nil, 0
	v2 := setup(t, map[string]string{"mbt/init.lua": `
mbt.command("auth", function(ctx)
  local code, body, h = ctx.mvs.request { method = "DELETE", path = "/services/authenticate", auth = "basic" }
  assert(code == 0 and body == "" and next(h) == nil)
  local s = ctx.mvs.session { user = "test2" }
  assert(s:token() == "" and s:request { path = "/info" } == 0 and s:logout() == 0)
end)`})
	e = v2.mustLoad(t, tweak, func(o *Options) { o.DryRun = true })
	if err := e.Run("auth", nil); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 || n != 0 {
		t.Errorf("dry run opened %d session(s): %v", n, calls)
	}
	if !strings.Contains(v2.logged(), "(dry run) no session for test2") {
		t.Errorf("log:\n%s", v2.logged())
	}
}
