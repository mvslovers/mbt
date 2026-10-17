package ext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	rt "github.com/arnodel/golua/runtime"
)

// TSO is the terminal session an interactive test drives (mbt test --tso);
// mbt backs it with its TN3270 client.
type TSO interface {
	Logon() error  // user and password come from the target
	Logoff() error // CLEAR, LOGOFF, "LOGGED OFF"
	Type(text string) error
	Key(name string) error // enter, clear, tab, pf1..pf24, pa1..pa3
	// Press sends the same keys without waiting for the keyboard to come
	// back: a running TSO program keeps it locked until READY (mbt#274).
	Press(name string) error
	Expect(text string, timeout time.Duration) error
	Screen() []string
	Cursor() (row, col int)
}

// TSOTests lists test/tso/*.lua, sorted; the name is the file's, upper case.
func TSOTests(root string) ([][2]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "test", "tso", "*.lua"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out [][2]string
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".lua")
		rel, _ := filepath.Rel(root, f)
		out = append(out, [2]string{strings.ToUpper(base), filepath.ToSlash(rel)})
	}
	return out, nil
}

// TSOTest runs one interactive test: the file returns function(t), which
// drives s; info is what t.testlib, t.target and friends show. The test is
// logged off whatever happens -- a TSO userid left logged on blocks every
// later logon (IKJ56425I) until it is cancelled.
func (e *Engine) TSOTest(file string, s TSO, info map[string]any) (err error) {
	if e.r == nil {
		e.runtime()
	}
	src, rerr := os.ReadFile(filepath.Join(e.o.Root, file))
	if rerr != nil {
		return rerr
	}
	chunk, cerr := e.r.CompileAndLoadLuaChunk(file, src, rt.TableValue(e.r.GlobalEnv()))
	if cerr != nil {
		return cfgErr("%s", luaMsg(cerr))
	}
	var fn rt.Value
	if lerr := e.limited(func() error {
		var err error
		fn, err = rt.Call1(e.r.MainThread(), rt.FunctionValue(chunk))
		return err
	}); lerr != nil {
		return cfgErr("%s", luaMsg(lerr))
	}
	if _, ok := fn.TryCallable(); !ok {
		return cfgErr("%s must return a function(t)", file)
	}
	loggedOn := false
	defer func() {
		if loggedOn {
			// reported whether the test passed or not: after a failed test
			// the logoff is the more likely to fail, and a user left logged
			// on fails every later logon (mbt#244 review)
			if lerr := s.Logoff(); lerr != nil {
				le := &LogoffError{User: fmt.Sprint(info["user"]), Err: lerr}
				if err == nil {
					err = le
				} else {
					err = &LogoffError{User: le.User, Err: lerr, Test: err}
				}
			}
		}
	}()
	t := e.tsoTable(s, info, &loggedOn)
	if cerr := e.limited(func() error {
		_, err := rt.Call1(e.r.MainThread(), fn, t)
		return err
	}); cerr != nil {
		return &Error{Msg: luaFull(cerr)}
	}
	return nil
}

// LogoffError is a TSO session that could not be logged off: the user is
// most likely still logged on, and every later logon fails (IKJ56425I)
// until it is cancelled. Test is the test's own error, when it failed too.
type LogoffError struct {
	User string
	Err  error
	Test error
}

func (e *LogoffError) Error() string {
	msg := fmt.Sprintf("logoff failed: %v -- %s is probably still logged on; later logons fail until it is cancelled (operator: C U=%s)", e.Err, e.User, e.User)
	if e.Test != nil {
		return e.Test.Error() + "\n" + msg
	}
	return msg
}

// luaFull is a Lua error without golua's "error: " prefix and without its
// traceback lines -- but with every line of the message (a screen dump).
func luaFull(err error) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimPrefix(err.Error(), "error: "), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "in function") || strings.HasPrefix(t, "in ") && strings.Contains(t, "(file ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n")
}

func (e *Engine) tsoTable(s TSO, info map[string]any, loggedOn *bool) rt.Value {
	t := rt.NewTable()
	for k, v := range info {
		t.Set(rt.StringValue(k), toLua(v))
	}
	self := rt.TableValue(t)
	// methods are called as t:name(...): argument 0 is t itself
	method := func(name string, nargs int, f func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error)) {
		e.fn(t, name, nargs, f)
	}
	ret := func(th *rt.Thread, k *rt.GoCont, err error) (rt.Cont, error) {
		if err != nil {
			return nil, err
		}
		return k.PushingNext1(th.Runtime, self), nil
	}
	method("logon", 1, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		err := s.Logon()
		if err == nil {
			*loggedOn = true
		}
		return ret(th, k, err)
	})
	method("logoff", 1, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		err := s.Logoff()
		*loggedOn = false
		return ret(th, k, err)
	})
	method("type", 2, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		text, err := k.StringArg(1)
		if err != nil {
			return nil, err
		}
		return ret(th, k, s.Type(text))
	})
	// A key method takes an optional table, like expect's: { wait = false }
	// sends the key and returns at once instead of waiting for the keyboard,
	// so a test can press PA1 while a program runs; t:expect synchronises.
	press := func(k *rt.GoCont, i int, name string) error {
		if k.NArgs() > i {
			if o, ok := k.Arg(i).TryTable(); ok {
				if v := o.Get(rt.StringValue("wait")); !v.IsNil() && !rt.Truth(v) {
					return s.Press(name)
				}
			}
		}
		return s.Key(name)
	}
	for _, key := range []string{"enter", "clear", "tab"} {
		key := key
		method(key, 2, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) { return ret(th, k, press(k, 1, key)) })
	}
	for _, kind := range []string{"pf", "pa"} {
		kind := kind
		method(kind, 3, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
			n, err := k.IntArg(1)
			if err != nil {
				return nil, err
			}
			return ret(th, k, press(k, 2, fmt.Sprintf("%s%d", kind, n)))
		})
	}
	method("expect", 3, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		text, err := k.StringArg(1)
		if err != nil {
			return nil, err
		}
		timeout := 10 * time.Second
		if k.NArgs() > 2 {
			if o, ok := k.Arg(2).TryTable(); ok {
				if n, ok := rt.ToInt(o.Get(rt.StringValue("timeout"))); ok && n > 0 {
					timeout = time.Duration(n) * time.Second
				}
			}
		}
		if err := s.Expect(text, timeout); err != nil {
			return nil, fmt.Errorf("expected %q within %s; the screen:\n%s", text, timeout, strings.Join(s.Screen(), "\n"))
		}
		return k.PushingNext1(th.Runtime, self), nil
	})
	method("screen", 1, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		return k.PushingNext1(th.Runtime, toLua(s.Screen())), nil
	})
	method("text", 1, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		return k.PushingNext1(th.Runtime, rt.StringValue(strings.Join(s.Screen(), "\n"))), nil
	})
	method("cursor", 1, func(th *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		r, c := s.Cursor()
		return k.PushingNext(th.Runtime, rt.IntValue(int64(r)), rt.IntValue(int64(c))), nil
	})
	return self
}

// ErrNoTSOTests: a project with none.
var ErrNoTSOTests = errors.New("no interactive tests: test/tso/*.lua")
