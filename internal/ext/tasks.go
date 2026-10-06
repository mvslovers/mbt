package ext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	rt "github.com/arnodel/golua/runtime"
)

// Task is a step with declared inputs and outputs:
//
//	mbt.task {
//	  name = "webroot", description = "...",
//	  before  = { "package", "dist" },   -- phases it runs ahead of; none: mbt run only
//	  inputs  = { "static/**" },          -- files, directories, globs (*, ?, **)
//	  outputs = { "build/webroot/httpd-webroot.img" },
//	  run = function(ctx) ... end,
//	}
//
// It is skipped while its outputs are newer than every input and nothing
// that made them changed (the task, the Lua files loaded, [tools]). Its
// outputs are removed before it runs and after it fails.
type Task struct {
	Name, Description string
	Before            []string
	Inputs, Outputs   []string
	fn                rt.Value
	from              string
}

func (e *Engine) luaTask(t *rt.Thread, c *rt.GoCont) (rt.Cont, error) {
	tb, err := c.TableArg(0)
	if err != nil {
		return nil, fmt.Errorf("mbt.task wants a table: mbt.task { name = \"...\", run = function(ctx) ... end }")
	}
	task := &Task{Name: tstr(tb, "name"), Description: tstr(tb, "description"), fn: tb.Get(rt.StringValue("run")), from: where(t, c)}
	if task.Name == "" {
		return nil, fmt.Errorf("mbt.task: needs a name")
	}
	if _, ok := task.fn.TryCallable(); !ok {
		return nil, fmt.Errorf("mbt.task %q: needs run = function(ctx) ... end", task.Name)
	}
	for _, k := range []string{"before", "inputs", "outputs"} {
		l, err := tstrs(tb, k)
		if err != nil {
			return nil, fmt.Errorf("mbt.task %q: %v", task.Name, err)
		}
		switch k {
		case "before":
			task.Before = l
		case "inputs":
			task.Inputs = l
		case "outputs":
			task.Outputs = l
		}
	}
	for _, b := range task.Before {
		ok := false
		for _, p := range Points {
			ok = ok || p == b
		}
		if !ok {
			return nil, fmt.Errorf("mbt.task %q: before = %q is not a command (%s)", task.Name, b, strings.Join(Points, ", "))
		}
	}
	if len(task.Inputs) > 0 && len(task.Outputs) == 0 {
		return nil, fmt.Errorf("mbt.task %q: inputs but no outputs -- nothing to compare them with", task.Name)
	}
	for _, o := range append(append([]string{}, task.Outputs...), task.Inputs...) {
		if filepath.IsAbs(o) || strings.HasPrefix(filepath.Clean(o), "..") {
			return nil, fmt.Errorf("mbt.task %q: %q is outside the project", task.Name, o)
		}
	}
	for _, x := range e.tasks {
		if x.Name == task.Name {
			return nil, fmt.Errorf("mbt.task %q: defined twice", task.Name)
		}
	}
	if _, dup := e.commands[task.Name]; dup {
		return nil, fmt.Errorf("mbt.task %q: a command has that name", task.Name)
	}
	e.tasks = append(e.tasks, task)
	sort.Slice(e.tasks, func(i, j int) bool { return e.tasks[i].Name < e.tasks[j].Name })
	return c.Next(), nil
}

// runTask runs t once per mbt invocation, unless it is up to date.
func (e *Engine) runTask(t *Task) error {
	if e.done[t.Name] {
		return nil
	}
	e.done[t.Name] = true
	sigPath := filepath.Join(e.o.Root, ".mbt", "cmd", "task-"+t.Name)
	sig := e.signature(t)
	inputs, err := Expand(e.o.Root, t.Inputs)
	if err != nil {
		return &Error{Msg: fmt.Sprintf("task %s (%s): %v", t.Name, t.from, err), Config: true}
	}
	if len(t.Outputs) > 0 && !e.stream && upToDate(e.o.Root, t.Outputs, inputs, sigPath, sig) {
		e.o.Log(fmt.Sprintf("[mbt] [task] %s up to date", t.Name))
		return nil
	}
	e.o.Log(fmt.Sprintf("[mbt] [task] %s", t.Name))
	if e.o.DryRun {
		e.o.Log("[mbt] (dry run) outputs left as they are")
	} else {
		for _, f := range t.Outputs {
			p := filepath.Join(e.o.Root, f)
			os.RemoveAll(p)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
		}
	}
	fail := func(msg string) error {
		if !e.o.DryRun {
			for _, f := range t.Outputs {
				os.RemoveAll(filepath.Join(e.o.Root, f))
			}
		}
		os.Remove(sigPath)
		return &Error{Msg: fmt.Sprintf("task %s (%s): %s", t.Name, t.from, msg)}
	}
	ctx := e.ctx(callInfo{outputs: nonNil(t.Outputs), inputs: nonNil(inputs)})
	if err := e.limited(func() error {
		_, err := rt.Call1(e.r.MainThread(), t.fn, ctx)
		return err
	}); err != nil {
		return fail(luaMsg(err))
	}
	if e.o.DryRun {
		return nil
	}
	for _, f := range t.Outputs {
		if _, err := os.Stat(filepath.Join(e.o.Root, f)); err != nil {
			return fail("ran, but did not write its output " + f)
		}
	}
	if len(t.Outputs) > 0 {
		os.MkdirAll(filepath.Dir(sigPath), 0o755)
		os.WriteFile(sigPath, []byte(sig), 0o644)
	}
	return nil
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// signature: what a task's outputs were made from besides its input files.
func (e *Engine) signature(t *Task) string {
	data, _ := json.Marshal(struct {
		Name            string
		Inputs, Outputs []string
		Lua             string
		Tools           any
	}{t.Name, t.Inputs, t.Outputs, e.sourceHash(), e.o.Tools})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func upToDate(root string, outputs, inputs []string, sigPath, sig string) bool {
	if old, err := os.ReadFile(sigPath); err != nil || string(old) != sig {
		return false
	}
	var oldest time.Time
	for _, f := range outputs {
		st, err := os.Stat(filepath.Join(root, f))
		if err != nil {
			return false
		}
		if oldest.IsZero() || st.ModTime().Before(oldest) {
			oldest = st.ModTime()
		}
	}
	for _, f := range inputs {
		st, err := os.Stat(filepath.Join(root, f))
		if err != nil || st.ModTime().After(oldest) {
			return false
		}
	}
	return true
}

// Expand resolves input patterns -- files, directories (everything below),
// globs with * ? and ** -- to the files and directories they name, relative
// to root. Directories count: removing a file changes its directory's time.
func Expand(root string, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, pat := range patterns {
		pat = filepath.ToSlash(pat)
		if !strings.ContainsAny(pat, "*?[") {
			st, err := os.Stat(filepath.Join(root, pat))
			if err != nil {
				return nil, fmt.Errorf("input %s does not exist", pat)
			}
			if !st.IsDir() {
				add(pat)
				continue
			}
			add(strings.TrimSuffix(pat, "/"))
			pat = strings.TrimSuffix(pat, "/") + "/**"
		}
		re, base := globRE(pat)
		n := 0
		filepath.WalkDir(filepath.Join(root, base), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			r, _ := filepath.Rel(root, p)
			r = filepath.ToSlash(r)
			if re.MatchString(r) {
				add(r)
				n++
			}
			return nil
		})
		if n == 0 && strings.ContainsAny(pat, "*?[") && !seen[strings.TrimSuffix(pat, "/**")] {
			return nil, fmt.Errorf("input pattern %s matches nothing", pat)
		}
	}
	sort.Strings(out)
	return out, nil
}

// globRE turns a pattern into a regexp, and returns the directory to walk.
func globRE(pat string) (*regexp.Regexp, string) {
	var b strings.Builder
	b.WriteString("^")
	base := ""
	if i := strings.IndexAny(pat, "*?["); i >= 0 {
		if j := strings.LastIndex(pat[:i], "/"); j >= 0 {
			base = pat[:j]
		}
	}
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch {
		case strings.HasPrefix(pat[i:], "**/"):
			b.WriteString("(.*/)?")
			i += 2
		case strings.HasPrefix(pat[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	if base == "" {
		base = "."
	}
	return regexp.MustCompile(b.String()), base
}
