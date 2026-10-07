package ext

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	rt "github.com/arnodel/golua/runtime"

	"github.com/mvslovers/mbt/internal/tools"
)

// callInfo is what one call into Lua knows about itself.
type callInfo struct {
	args    []string
	result  map[string]any
	kind    string
	outputs []string
	inputs  []string
}

// ctx builds the table a hook, task or command gets:
//
//	ctx.project            name, version, modules, tests (a copy: read-only)
//	ctx.args               mbt run NAME -- ARGS
//	ctx.dry_run, ctx.kind  (kind: host, mvs or tso in test hooks)
//	ctx.result             in after_ hooks: what the command produced
//	ctx.out, ctx.inputs    in a task: its outputs, the files its inputs matched
//	ctx.log(s), ctx.warn(s)
//	ctx.exec{argv..., env = {}, check = true} -> output, exit code
//	ctx.tool(name)         a [tools] entry: fetched, pinned, its path
//	ctx.fs.read/write/exists/mkdir/remove/list   inside the project only
func (e *Engine) ctx(ci callInfo) rt.Value {
	c := rt.NewTable()
	set := func(k string, v rt.Value) { c.Set(rt.StringValue(k), v) }

	p := e.o.Project
	set("project", toLua(map[string]any{"name": p.Name, "version": p.Version, "modules": p.Modules, "tests": p.Tests}))
	set("args", toLua(ci.args))
	set("dry_run", rt.BoolValue(e.o.DryRun))
	if ci.kind != "" {
		set("kind", rt.StringValue(ci.kind))
	}
	if ci.result != nil {
		set("result", toLua(ci.result))
	}
	if ci.outputs != nil {
		set("out", toLua(ci.outputs))
		set("inputs", toLua(ci.inputs))
	}

	e.fn(c, "log", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		s, err := k.StringArg(0)
		if err != nil {
			return nil, err
		}
		e.o.Log("[mbt] " + s)
		return k.Next(), nil
	})
	e.fn(c, "warn", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		s, err := k.StringArg(0)
		if err != nil {
			return nil, err
		}
		e.o.Log("[mbt] WARNING: " + s)
		return k.Next(), nil
	})
	e.fn(c, "exec", 1, e.exec)
	e.fn(c, "tool", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		name, err := k.StringArg(0)
		if err != nil {
			return nil, err
		}
		p, err := e.tool(name)
		if err != nil {
			return nil, err
		}
		return k.PushingNext1(t.Runtime, rt.StringValue(p)), nil
	})

	fs := rt.NewTable()
	e.fn(fs, "read", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		p, err := e.path(k, false)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("ctx.fs.read: %v", err)
		}
		return k.PushingNext1(t.Runtime, rt.StringValue(string(data))), nil
	})
	e.fn(fs, "write", 2, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		p, err := e.path(k, true)
		if err != nil {
			return nil, err
		}
		data, err := k.StringArg(1)
		if err != nil {
			return nil, err
		}
		if e.o.DryRun {
			e.o.Log("[mbt] (dry run) would write " + e.label(p))
			return k.Next(), nil
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			return nil, fmt.Errorf("ctx.fs.write: %v", err)
		}
		return k.Next(), nil
	})
	e.fn(fs, "exists", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		p, err := e.path(k, false)
		if err != nil {
			return nil, err
		}
		_, serr := os.Stat(p)
		return k.PushingNext1(t.Runtime, rt.BoolValue(serr == nil)), nil
	})
	e.fn(fs, "mkdir", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		p, err := e.path(k, true)
		if err != nil {
			return nil, err
		}
		if !e.o.DryRun {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return nil, fmt.Errorf("ctx.fs.mkdir: %v", err)
			}
		}
		return k.Next(), nil
	})
	e.fn(fs, "remove", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		p, err := e.path(k, true)
		if err != nil {
			return nil, err
		}
		if p == e.root() {
			return nil, errors.New("ctx.fs.remove: not the project itself")
		}
		if e.o.DryRun {
			e.o.Log("[mbt] (dry run) would remove " + e.label(p))
			return k.Next(), nil
		}
		if err := os.RemoveAll(p); err != nil {
			return nil, fmt.Errorf("ctx.fs.remove: %v", err)
		}
		return k.Next(), nil
	})
	e.fn(fs, "list", 1, func(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
		pat, err := k.StringArg(0)
		if err != nil {
			return nil, err
		}
		files, err := Expand(e.o.Root, []string{pat})
		if err != nil {
			files = nil // nothing matches: an empty list, not an error
		}
		return k.PushingNext1(t.Runtime, toLua(files)), nil
	})
	set("fs", rt.TableValue(fs))
	return rt.TableValue(c)
}

func (e *Engine) root() string {
	r, err := filepath.EvalSymlinks(e.o.Root)
	if err != nil {
		return e.o.Root
	}
	return r
}

// path resolves argument 0 inside the project: relative, no "..", and --
// through any symlink -- still inside the project. forWrite checks the
// directory the path would be created in.
func (e *Engine) path(k *rt.GoCont, forWrite bool) (string, error) {
	s, err := k.StringArg(0)
	if err != nil {
		return "", err
	}
	if s == "" || filepath.IsAbs(s) {
		return "", fmt.Errorf("ctx.fs: %q -- a path relative to the project, please", s)
	}
	clean := filepath.Clean(s)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ctx.fs: %q leaves the project", s)
	}
	root := e.root()
	full := filepath.Join(root, clean)
	check := full
	if forWrite {
		// the deepest part that exists decides
		for {
			if _, err := os.Lstat(check); err == nil || check == root {
				break
			}
			check = filepath.Dir(check)
		}
	}
	if real, err := filepath.EvalSymlinks(check); err == nil {
		if r, err := filepath.Rel(root, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("ctx.fs: %q leads outside the project (through a symlink)", s)
		}
	}
	return full, nil
}

// exec runs a program: an argv list, no shell. Named fields: env (extra
// variables), check (false: a non-zero exit is returned, not raised).
func (e *Engine) exec(t *rt.Thread, k *rt.GoCont) (rt.Cont, error) {
	tb, err := k.TableArg(0)
	if err != nil {
		return nil, errors.New(`ctx.exec wants an argv list: ctx.exec { "prog", "arg" } -- there is no shell`)
	}
	var argv []string
	for i := int64(1); i <= tb.Len(); i++ {
		s, ok := tb.Get(rt.IntValue(i)).ToString()
		if !ok {
			return nil, fmt.Errorf("ctx.exec: argument %d is not a string", i)
		}
		argv = append(argv, s)
	}
	if len(argv) == 0 {
		return nil, errors.New("ctx.exec: empty argv")
	}
	check := true
	if v := tb.Get(rt.StringValue("check")); !v.IsNil() {
		check = rt.Truth(v)
	}
	env := os.Environ()
	if v := tb.Get(rt.StringValue("env")); !v.IsNil() {
		et, ok := v.TryTable()
		if !ok {
			return nil, errors.New("ctx.exec: env must be a table of strings")
		}
		var keys []string
		vals := map[string]string{}
		for key, val, _ := et.Next(rt.NilValue); !key.IsNil(); key, val, _ = et.Next(key) {
			ks, ok1 := key.TryString()
			vs, ok2 := val.ToString()
			if !ok1 || !ok2 {
				return nil, errors.New("ctx.exec: env must be a table of strings")
			}
			keys = append(keys, ks)
			vals[ks] = vs
		}
		sort.Strings(keys)
		for _, ks := range keys {
			env = append(env, ks+"="+vals[ks])
		}
	}
	line := quote(argv)
	if e.o.DryRun {
		e.o.Log("[mbt] (dry run) would run: " + line)
		return k.PushingNext(t.Runtime, rt.StringValue(""), rt.IntValue(0)), nil
	}
	if e.o.Verbose || e.stream {
		e.o.Log("[mbt]   " + line)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = e.o.Root
	cmd.Env = env
	var out bytes.Buffer
	if e.stream {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = e.o.Stdin, e.o.Stdout, e.o.Stderr
	} else {
		cmd.Stdout, cmd.Stderr = &out, &out
	}
	runErr := cmd.Run()
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(runErr, &ee):
		code = ee.ExitCode()
	case runErr != nil:
		return nil, fmt.Errorf("ctx.exec: cannot run %s: %v", argv[0], runErr)
	}
	if e.o.Verbose && !e.stream {
		e.o.Stdout.Write(out.Bytes())
	}
	if code != 0 && check {
		if !e.o.Verbose && !e.stream {
			e.o.Stderr.Write(out.Bytes())
		}
		return nil, fmt.Errorf("ctx.exec: %s exited with %d", filepath.Base(argv[0]), code)
	}
	return k.PushingNext(t.Runtime, rt.StringValue(out.String()), rt.IntValue(int64(code))), nil
}

func (e *Engine) tool(name string) (string, error) {
	for _, t := range e.o.Tools {
		if t.Name == name {
			return tools.Ensure(e.o.Root, t, e.o.ToolOpt)
		}
	}
	return "", fmt.Errorf("ctx.tool(%q): [tools] in mbt.toml does not declare it", name)
}

func quote(c []string) string {
	q := make([]string, len(c))
	for i, a := range c {
		if a == "" || strings.ContainsAny(a, " \t'\"$\\;&|<>()*?") {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			q[i] = a
		}
	}
	return strings.Join(q, " ")
}
