package ext

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	rt "github.com/arnodel/golua/runtime"
)

// Access says how a request from Lua reaches mvsMF (mbt#239). The
// passwords stay in mbt: Lua names a user by its alias in the target and
// gets the operations, never the credentials.
type Access struct {
	// Mode: "basic" (HTTP Basic with the stored password), "none" (no
	// credentials), "token" (a given session token), "session" (a private
	// session of its own, logged on).
	Mode     string
	User     string  // the alias of a further user; "" = the target's main user
	Password *string // basic: a literal of the caller's own instead of the stored one
	Token    string  // token: the session token to present
}

// Conn is one mvsMF connection opened for Lua.
type Conn interface {
	RequestHeaders(method, path, contentType string, body []byte) (int, []byte, http.Header, error)
	Token() string
	LogoutStatus() (int, error)
}

// headered is an MVS session that can also return response headers.
type headered interface {
	RequestHeaders(method, path, contentType string, body []byte) (int, []byte, http.Header, error)
}

// CloseSessions logs off every private session Lua opened and left open;
// it runs at the end of the command, also after an error.
func (e *Engine) CloseSessions() {
	for _, c := range e.opened {
		if c.Token() != "" {
			c.LogoutStatus()
		}
	}
	e.opened = nil
}

func headersToLua(h http.Header) rt.Value {
	t := rt.NewTable()
	for k, v := range h {
		sep := ", "
		if strings.EqualFold(k, "Set-Cookie") {
			sep = "\n"
		}
		t.Set(rt.StringValue(strings.ToLower(k)), rt.StringValue(strings.Join(v, sep)))
	}
	return rt.TableValue(t)
}

// mvsReq is a parsed ctx.mvs.request / s:request table.
type mvsReq struct {
	method, path, ctype string
	body                []byte
	access              Access
}

func parseReq(tb *rt.Table, where string, private bool) (*mvsReq, error) {
	r := &mvsReq{method: tstr(tb, "method"), path: tstr(tb, "path"), ctype: tstr(tb, "content_type")}
	if r.method == "" {
		r.method = "GET"
	}
	if !strings.HasPrefix(r.path, "/") {
		return nil, fmt.Errorf("%s: path %q must start with / (below /zosmf)", where, r.path)
	}
	if b, ok := tb.Get(rt.StringValue("body")).TryString(); ok {
		r.body = []byte(b)
		if r.ctype == "" {
			r.ctype = "application/json"
		}
	}
	auth, user, token := tstr(tb, "auth"), tstr(tb, "user"), tstr(tb, "token")
	pv := tb.Get(rt.StringValue("password"))
	hasToken := !tb.Get(rt.StringValue("token")).IsNil()
	if private && (auth != "" || user != "" || hasToken || !pv.IsNil()) {
		return nil, fmt.Errorf("%s: a session's request takes no auth, user, password or token -- the session has them", where)
	}
	switch auth {
	case "", "session", "basic", "none", "token":
	default:
		return nil, fmt.Errorf("%s: auth %q: use session, basic, none or token", where, auth)
	}
	if auth == "" {
		auth = "session"
	}
	if user != "" && auth != "basic" {
		return nil, fmt.Errorf("%s: user only with auth = \"basic\"; a session as another user is ctx.mvs.session { user = %q }", where, user)
	}
	if !pv.IsNil() {
		p, ok := pv.TryString()
		if !ok || auth != "basic" {
			return nil, fmt.Errorf("%s: password (a string) only with auth = \"basic\"", where)
		}
		r.access.Password = &p
	}
	// an empty token is not refused here: under --dry-run s:token() and
	// ctx.mvs.token() are "", and the script must run through all the same
	if hasToken != (auth == "token") {
		return nil, fmt.Errorf("%s: auth = \"token\" needs token, and token needs auth = \"token\"", where)
	}
	r.access.Mode, r.access.User, r.access.Token = auth, user, token
	return r, nil
}

func (e *Engine) mvsTable() *rt.Table {
	mvs := rt.NewTable()
	session := func() (MVS, error) {
		if e.o.MVS == nil {
			return nil, errors.New("ctx.mvs: no MVS target in this command")
		}
		return e.o.MVS()
	}
	open := func(a Access) (Conn, error) {
		if e.o.Open == nil {
			return nil, errors.New("ctx.mvs: no MVS target in this command")
		}
		return e.o.Open(a)
	}
	dryAnswer := func(t *rt.Thread, k *rt.GoCont, what string) (rt.Cont, error) {
		e.o.Log("[mbt] (dry run) would send " + what)
		return k.PushingNext(t.Runtime, rt.IntValue(0), rt.StringValue(""), rt.TableValue(rt.NewTable())), nil
	}
	answer := func(t *rt.Thread, k *rt.GoCont, r *mvsReq, code int, data []byte, h http.Header, err error) (rt.Cont, error) {
		if err != nil && code == 0 {
			return nil, fmt.Errorf("ctx.mvs.request %s %s: %v", r.method, r.path, err)
		}
		return k.PushingNext(t.Runtime, rt.IntValue(int64(code)), rt.StringValue(string(data)), headersToLua(h)), nil
	}

	e.fn(mvs, "request", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		tb, err := k.TableArg(0)
		if err != nil {
			return nil, errors.New(`ctx.mvs.request wants a table: { method = "GET", path = "/restfiles/ds?dslevel=X" }`)
		}
		r, err := parseReq(tb, "ctx.mvs.request", false)
		if err != nil {
			return nil, err
		}
		if e.o.DryRun && (r.method != "GET" || r.access.Mode == "token" && r.access.Token == "") {
			return dryAnswer(t, k, r.method+" "+r.path)
		}
		if r.access.Mode == "token" && r.access.Token == "" {
			return nil, errors.New("ctx.mvs.request: auth = \"token\" with an empty token")
		}
		if r.access.Mode == "session" {
			m, err := session()
			if err != nil {
				return nil, err
			}
			if hm, ok := m.(headered); ok {
				code, data, h, err := hm.RequestHeaders(r.method, r.path, r.ctype, r.body)
				return answer(t, k, r, code, data, h, err)
			}
			code, data, err := m.Request(r.method, r.path, r.ctype, r.body)
			return answer(t, k, r, code, data, nil, err)
		}
		c, err := open(r.access)
		if err != nil {
			return nil, fmt.Errorf("ctx.mvs.request: %v", err)
		}
		code, data, h, err := c.RequestHeaders(r.method, r.path, r.ctype, r.body)
		return answer(t, k, r, code, data, h, err)
	})
	e.fn(mvs, "token", 0, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		// the token is for an external program, which a dry run does not
		// start: no logon for it either (mbt#210)
		if e.o.DryRun {
			e.o.Log("[mbt] (dry run) no session token")
			return k.PushingNext1(t.Runtime, rt.StringValue("")), nil
		}
		m, err := session()
		if err != nil {
			return nil, err
		}
		return k.PushingNext1(t.Runtime, rt.StringValue(m.Token())), nil
	})
	e.fn(mvs, "session", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		user := ""
		if k.NArgs() > 0 && !k.Arg(0).IsNil() {
			tb, err := k.TableArg(0)
			if err != nil {
				return nil, errors.New(`ctx.mvs.session wants a table or nothing: { user = "test2" }`)
			}
			var bad []string
			for key, _, _ := tb.Next(rt.NilValue); !key.IsNil(); key, _, _ = tb.Next(key) {
				if ks, ok := key.TryString(); !ok || ks != "user" {
					bad = append(bad, fmt.Sprintf("%v", key.Interface()))
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				return nil, fmt.Errorf("ctx.mvs.session: unknown key %s (only user)", bad[0])
			}
			user = tstr(tb, "user")
		}
		label := "the main user"
		if user != "" {
			label = user
		}
		var c Conn
		if e.o.DryRun {
			e.o.Log("[mbt] (dry run) no session for " + label)
		} else {
			var err error
			if c, err = open(Access{Mode: "session", User: user}); err != nil {
				return nil, fmt.Errorf("ctx.mvs.session: %v", err)
			}
			e.opened = append(e.opened, c)
		}
		s := rt.NewTable()
		e.fn(s, "request", 2, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
			tb, err := k.TableArg(1)
			if err != nil {
				return nil, errors.New(`s:request wants a table, called with a colon: s:request { path = "/info" }`)
			}
			r, err := parseReq(tb, "s:request", true)
			if err != nil {
				return nil, err
			}
			if c == nil {
				return dryAnswer(t, k, r.method+" "+r.path+" as "+label)
			}
			if c.Token() == "" {
				return nil, fmt.Errorf("s:request %s %s: the session of %s is logged off", r.method, r.path, label)
			}
			code, data, h, err := c.RequestHeaders(r.method, r.path, r.ctype, r.body)
			return answer(t, k, r, code, data, h, err)
		})
		e.fn(s, "token", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
			tok := ""
			if c != nil {
				tok = c.Token()
			}
			return k.PushingNext1(t.Runtime, rt.StringValue(tok)), nil
		})
		e.fn(s, "logout", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
			if c == nil {
				e.o.Log("[mbt] (dry run) would log off " + label)
				return k.PushingNext1(t.Runtime, rt.IntValue(0)), nil
			}
			code, err := c.LogoutStatus()
			if err != nil && code == 0 {
				return nil, fmt.Errorf("s:logout: %v", err)
			}
			return k.PushingNext1(t.Runtime, rt.IntValue(int64(code))), nil
		})
		return k.PushingNext1(t.Runtime, rt.TableValue(s)), nil
	})
	return mvs
}
