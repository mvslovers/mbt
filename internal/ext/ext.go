// Package ext runs mbt's extensions: Lua 5.5 (github.com/arnodel/golua),
// inside mbt, in a sandbox (internals/mbt-3-extensions.md).
//
// Lua is read from the project's mbt/init.lua (modules in mbt/lua/) and then
// from the user's ~/.mbt/init.lua (modules in ~/.mbt/lua/). It registers
//
//	mbt.hook(point, fn)                     before_/after_ build, test, package,
//	                                        dist, deploy, release; on_failure
//	mbt.task { name, before, inputs, outputs, run, description }
//	mbt.command(name, fn) or mbt.command { name, run, description }
//
// and every function gets a ctx (ctx.go). Lua sees no os, io, debug,
// dofile or loadfile; require reads mbt/lua/ (then ~/.mbt/lua/) only; a
// call runs under a CPU and a memory limit. Hooks act -- run programs,
// write files, abort -- but never change the build itself.
package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/base"
	"github.com/arnodel/golua/lib/coroutine"
	"github.com/arnodel/golua/lib/mathlib"
	"github.com/arnodel/golua/lib/packagelib"
	"github.com/arnodel/golua/lib/stringlib"
	"github.com/arnodel/golua/lib/tablelib"
	"github.com/arnodel/golua/lib/utf8lib"
	rt "github.com/arnodel/golua/runtime"

	"github.com/mvslovers/mbt/internal/tools"
)

// API is the version of the Lua interface; raised only on incompatible change.
const API = 1

// Points are the commands a hook can run before and after.
var Points = []string{"build", "test", "package", "dist", "deploy", "release"}

// Error is an extension that is wrong (Config: exit 2) or that failed (exit 1).
type Error struct {
	Msg    string
	Config bool
}

func (e *Error) Error() string { return e.Msg }

func cfgErr(format string, a ...any) error {
	return &Error{Msg: fmt.Sprintf(format, a...), Config: true}
}

// Project is what ctx.project shows: read-only facts.
type Project struct {
	Name, Version  string
	Modules, Tests []string
}

// Options configure an engine; Root is required.
type Options struct {
	Root    string // the project directory
	Home    string // ~/.mbt (MBT_HOME); "" = no user level
	Project Project
	Tools   []tools.Tool
	ToolOpt tools.Options
	Version string // mbt's version, as mbt.version
	Verbose bool   // show each command and its output
	DryRun  bool   // ctx.dry_run: nothing is executed or written
	Log     func(string)
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	// CPU and Memory limit one call into Lua (0 = the defaults).
	CPU, Memory uint64
	// Target describes the selected MVS system for ctx.target -- never a
	// password; MVS opens (once) the mvsMF session for ctx.mvs. Both are
	// asked only when Lua uses them; nil: there is none.
	Target func() (map[string]any, error)
	MVS    func() (MVS, error)
}

// MVS is the mvsMF session as Lua reaches it.
type MVS interface {
	Request(method, path, contentType string, body []byte) (int, []byte, error)
	Token() string
}

// Engine is the loaded Lua of one mbt invocation.
type Engine struct {
	o        Options
	r        *rt.Runtime
	hooks    map[string][]hook
	tasks    []*Task
	commands map[string]*command
	sources  []string // files loaded, for the task signature
	done     map[string]bool
	loaded   map[string]rt.Value // require's modules
	stream   bool                // mbt run: programs write to the terminal
}

type hook struct {
	fn   rt.Value
	from string
}

type command struct {
	name, description, from string
	fn                      rt.Value
}

// HookNames lists every valid hook name.
func HookNames() []string {
	var n []string
	for _, p := range Points {
		n = append(n, "before_"+p, "after_"+p)
	}
	return append(n, "on_failure")
}

// Load reads mbt/init.lua and ~/.mbt/init.lua. Without either it returns an
// engine that does nothing and costs nothing.
func Load(o Options) (*Engine, error) {
	if o.Log == nil {
		o.Log = func(s string) { fmt.Println(s) }
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.CPU == 0 {
		o.CPU = 1_000_000_000 // pure Lua work: some seconds
	}
	if o.Memory == 0 {
		o.Memory = 256 << 20
	}
	e := &Engine{o: o, hooks: map[string][]hook{}, commands: map[string]*command{}, done: map[string]bool{}, loaded: map[string]rt.Value{}}
	files := []string{filepath.Join(o.Root, "mbt", "init.lua")}
	if o.Home != "" {
		files = append(files, filepath.Join(o.Home, "init.lua"))
	}
	var present []string
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			present = append(present, f)
		}
	}
	if len(present) == 0 {
		return e, nil
	}
	e.runtime()
	for _, f := range present {
		if err := e.loadFile(f, e.label(f)); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Active says whether any Lua was loaded.
func (e *Engine) Active() bool { return e.r != nil }

// label names a file the way messages show it.
func (e *Engine) label(path string) string {
	if r, err := filepath.Rel(e.o.Root, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	if e.o.Home != "" {
		if r, err := filepath.Rel(e.o.Home, path); err == nil && !strings.HasPrefix(r, "..") {
			return "~/.mbt/" + filepath.ToSlash(r)
		}
	}
	return path
}

func (e *Engine) runtime() {
	e.r = rt.New(e.o.Stdout)
	// the package library first: the others register in it; then it goes
	// again with base's file access -- require is mbt's own
	lib.LoadLibs(e.r, packagelib.LibLoader, base.LibLoader, stringlib.LibLoader,
		tablelib.LibLoader, mathlib.LibLoader, utf8lib.LibLoader, coroutine.LibLoader)
	g := e.r.GlobalEnv()
	for _, k := range []string{"dofile", "loadfile", "package"} {
		g.Set(rt.StringValue(k), rt.NilValue)
	}
	e.fn(g, "require", 1, e.require)

	m := rt.NewTable()
	e.fn(m, "hook", 2, e.luaHook)
	e.fn(m, "task", 1, e.luaTask)
	e.fn(m, "command", 2, e.luaCommand)
	m.Set(rt.StringValue("api"), rt.IntValue(API))
	m.Set(rt.StringValue("version"), rt.StringValue(e.o.Version))
	g.Set(rt.StringValue("mbt"), rt.TableValue(m))
}

// fn registers a Go function; mbt's own functions run under the limits
// (they reach the outside world on purpose, through mbt's rules).
func (e *Engine) fn(t *rt.Table, name string, nargs int, f rt.GoFunctionFunc) {
	g := e.r.SetEnvGoFunc(t, name, f, nargs, false)
	rt.SolemnlyDeclareCompliance(rt.ComplyCpuSafe|rt.ComplyMemSafe|rt.ComplyTimeSafe|rt.ComplyIoSafe, g)
}

func (e *Engine) loadFile(path, label string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	e.sources = append(e.sources, path)
	chunk, err := e.r.CompileAndLoadLuaChunk(label, src, rt.TableValue(e.r.GlobalEnv()))
	if err != nil {
		return cfgErr("%s", luaMsg(err))
	}
	if err := e.limited(func() error {
		_, err := rt.Call1(e.r.MainThread(), rt.FunctionValue(chunk))
		return err
	}); err != nil {
		return cfgErr("%s", luaMsg(err))
	}
	return nil
}

// limited runs f under the CPU and memory limits; no time limit, because a
// program run through ctx.exec may rightly take long.
func (e *Engine) limited(f func() error) error {
	_, err := e.r.MainThread().CallContext(rt.RuntimeContextDef{
		HardLimits:    rt.RuntimeResources{Cpu: e.o.CPU, Memory: e.o.Memory},
		RequiredFlags: rt.ComplyIoSafe,
	}, f)
	return err
}

var modRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// require loads <name> from mbt/lua/, then ~/.mbt/lua/; a module is loaded
// once.
func (e *Engine) require(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.Check1Arg(); err != nil {
		return nil, err
	}
	name, err := c.StringArg(0)
	if err != nil {
		return nil, err
	}
	if !modRE.MatchString(name) {
		return nil, fmt.Errorf("require %q: not a module name (lower case, dots: a.b.c)", name)
	}
	if v, ok := e.loaded[name]; ok {
		return c.PushingNext1(t.Runtime, v), nil
	}
	rel := strings.ReplaceAll(name, ".", "/") + ".lua"
	dirs := []string{filepath.Join(e.o.Root, "mbt", "lua")}
	if e.o.Home != "" {
		dirs = append(dirs, filepath.Join(e.o.Home, "lua"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, rel)
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		e.sources = append(e.sources, p)
		chunk, err := e.r.CompileAndLoadLuaChunk(e.label(p), src, rt.TableValue(e.r.GlobalEnv()))
		if err != nil {
			return nil, err
		}
		v, err := rt.Call1(t, rt.FunctionValue(chunk))
		if err != nil {
			return nil, err
		}
		if v.IsNil() {
			v = rt.BoolValue(true)
		}
		e.loaded[name] = v
		return c.PushingNext1(t.Runtime, v), nil
	}
	return nil, fmt.Errorf("require %q: no mbt/lua/%s (nor ~/.mbt/lua/%s)", name, rel, rel)
}

// where names the file and line of the Lua that is calling into Go.
func where(t *rt.Thread, c *rt.GoCont) string {
	tb := t.Runtime.Traceback("", c)
	for _, l := range strings.Split(tb, "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, "(file "); i >= 0 && !strings.Contains(l, "(file [Go])") {
			return strings.TrimSuffix(l[i+6:], ")")
		}
	}
	return "?"
}

func (e *Engine) luaHook(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	if err := c.CheckNArgs(2); err != nil {
		return nil, err
	}
	point, err := c.StringArg(0)
	if err != nil {
		return nil, err
	}
	f, err := c.CallableArg(1)
	if err != nil {
		return nil, err
	}
	valid := false
	for _, n := range HookNames() {
		valid = valid || n == point
	}
	if !valid {
		return nil, fmt.Errorf("mbt.hook: no hook point %q (%s)", point, strings.Join(HookNames(), ", "))
	}
	e.hooks[point] = append(e.hooks[point], hook{fn: rt.FunctionValue(f), from: where(t, c)})
	return c.Next(), nil
}

func (e *Engine) luaCommand(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	cmd := &command{from: where(t, c)}
	if c.NArgs() >= 1 {
		if tb, ok := c.Arg(0).TryTable(); ok {
			cmd.name = tstr(tb, "name")
			cmd.description = tstr(tb, "description")
			cmd.fn = tb.Get(rt.StringValue("run"))
		} else {
			cmd.name, _ = c.Arg(0).TryString()
			if c.NArgs() >= 2 {
				cmd.fn = c.Arg(1)
			}
		}
	}
	if cmd.name == "" {
		return nil, errors.New(`mbt.command: needs a name -- mbt.command("name", function(ctx) ... end)`)
	}
	if _, ok := cmd.fn.TryCallable(); !ok {
		return nil, fmt.Errorf("mbt.command %q: needs a function to run", cmd.name)
	}
	if _, dup := e.commands[cmd.name]; dup {
		return nil, fmt.Errorf("mbt.command %q: defined twice", cmd.name)
	}
	e.commands[cmd.name] = cmd
	return c.Next(), nil
}

func tstr(t *rt.Table, k string) string {
	s, _ := t.Get(rt.StringValue(k)).TryString()
	return s
}

func tstrs(t *rt.Table, k string) ([]string, error) {
	v := t.Get(rt.StringValue(k))
	if v.IsNil() {
		return nil, nil
	}
	l, ok := v.TryTable()
	if !ok {
		return nil, fmt.Errorf("%s must be a list of strings", k)
	}
	var out []string
	for i := int64(1); i <= l.Len(); i++ {
		s, ok := l.Get(rt.IntValue(i)).TryString()
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not a string", k, i)
		}
		out = append(out, s)
	}
	return out, nil
}

// Commands lists the commands and tasks `mbt run` can run, sorted.
func (e *Engine) Commands() [][2]string {
	var out [][2]string
	for _, c := range e.commands {
		out = append(out, [2]string{c.name, c.description})
	}
	for _, t := range e.tasks {
		d := t.Description
		if d == "" {
			d = "task"
		}
		out = append(out, [2]string{t.Name, d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// Before runs, ahead of a command: the tasks declared for it, then the
// before_ hooks. An error stops the command before it does anything.
func (e *Engine) Before(point string) error {
	if !e.Active() {
		return nil
	}
	for _, t := range e.tasks {
		for _, b := range t.Before {
			if b == point {
				if err := e.runTask(t); err != nil {
					return err
				}
			}
		}
	}
	return e.callHooks("before_"+point, nil)
}

// After runs the after_ hooks with what the command produced. An error
// undoes nothing, but the command fails.
func (e *Engine) After(point string, result map[string]any) error {
	if !e.Active() {
		return nil
	}
	return e.callHooks("after_"+point, result)
}

// Failure runs the on_failure hooks; their own errors are only reported.
func (e *Engine) Failure(command string, failure error) {
	if !e.Active() || len(e.hooks["on_failure"]) == 0 {
		return
	}
	msg := ""
	if failure != nil {
		msg = failure.Error()
	}
	if err := e.callHooks("on_failure", map[string]any{"command": command, "error": msg}); err != nil {
		e.o.Log(fmt.Sprintf("[mbt] WARNING: %v", err))
	}
}

func (e *Engine) callHooks(name string, result map[string]any) error {
	for _, h := range e.hooks[name] {
		ctx := e.ctx(callInfo{result: result, kind: kindOf(result)})
		if err := e.limited(func() error {
			_, err := rt.Call1(e.r.MainThread(), h.fn, ctx)
			return err
		}); err != nil {
			return &Error{Msg: fmt.Sprintf("hook %s (%s): %s", name, h.from, luaMsg(err))}
		}
	}
	return nil
}

func kindOf(result map[string]any) string {
	if k, ok := result["kind"].(string); ok {
		return k
	}
	return ""
}

// Run runs a command or a task by name: mbt run NAME [-- ARGS].
func (e *Engine) Run(name string, args []string) error {
	if c, ok := e.commands[name]; ok {
		e.stream = true
		defer func() { e.stream = false }()
		ctx := e.ctx(callInfo{args: args})
		if err := e.limited(func() error {
			_, err := rt.Call1(e.r.MainThread(), c.fn, ctx)
			return err
		}); err != nil {
			return &Error{Msg: fmt.Sprintf("command %s (%s): %s", name, c.from, luaMsg(err))}
		}
		return nil
	}
	for _, t := range e.tasks {
		if t.Name == name {
			e.stream = true
			defer func() { e.stream = false }()
			return e.runTask(t)
		}
	}
	var names []string
	for _, c := range e.Commands() {
		names = append(names, c[0])
	}
	if len(names) == 0 {
		return cfgErr("no command or task %q: there is no mbt/init.lua (or it defines none)", name)
	}
	return cfgErr("no command or task %q (defined: %s)", name, strings.Join(names, ", "))
}

// sourceHash covers every Lua file loaded: a change re-runs the tasks.
func (e *Engine) sourceHash() string {
	h := sha256.New()
	for _, f := range e.sources {
		data, _ := os.ReadFile(f)
		h.Write([]byte(f))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// luaMsg is the first line of a Lua error, without golua's "error: " prefix.
func luaMsg(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "error: ")
}
