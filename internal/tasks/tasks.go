// Package tasks runs the project steps mbt does not do itself, declared in
// mbt.toml -- data, no shell (design §9; Lua comes later, for logic):
//
//	[task.webroot]
//	inputs  = ["static/**"]
//	outputs = ["build/webroot/httpd-webroot.img"]
//	before  = ["package", "dist"]
//	run = [
//	  ["{tool:ufsd-utils}", "create", "{out}", "--size", "1M", "--blksize", "4096"],
//	  ["{tool:ufsd-utils}", "cp", "-r", "static/", "{out}:/"],
//	]
//
// run is a list of argv lists, executed in the project directory without a
// shell. Placeholders: {out} (the first output), {root}, {tool:NAME} (a
// [tools] entry, fetched and pinned on first use), and an element "{args}"
// that stands for the arguments given to `mbt run NAME -- ...`. env sets
// variables the environment does not already set (a default, like make's ?=).
//
// before names the phases the task runs ahead of: build, test, package, dist,
// deploy. A task without before runs only through `mbt run NAME`.
//
// With inputs and outputs, a task whose outputs are newer than every input
// (files and directories, ** recursive), whose definition and tools are
// unchanged, is up to date and skipped. Outputs are removed before a run and
// after a failed one, so a failed task leaves nothing behind.
package tasks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/tools"
)

// Phases a task can run before.
var Phases = []string{"build", "test", "package", "dist", "deploy"}

// Task is one [task.NAME].
type Task struct {
	Name        string
	Description string
	Inputs      []string
	Outputs     []string
	Before      []string
	Run         [][]string
	Env         map[string]string
}

// Error is a task that is wrong (Config) or failed.
type Error struct {
	Msg    string
	Config bool
}

func (e *Error) Error() string { return e.Msg }

func cfgErr(format string, a ...any) error {
	return &Error{Msg: fmt.Sprintf(format, a...), Config: true}
}

// Declared reads [task.*] from a project's raw table, sorted by name.
func Declared(raw map[string]any) ([]*Task, error) {
	t, _ := raw["task"].(map[string]any)
	var out []*Task
	for name, v := range t {
		e, ok := v.(map[string]any)
		if !ok {
			return nil, cfgErr("[task.%s] must be a table", name)
		}
		task := &Task{Name: name, Env: map[string]string{}}
		for k, x := range e {
			var err error
			switch k {
			case "description":
				task.Description, _ = x.(string)
			case "inputs":
				task.Inputs, err = strList(x, name, k)
			case "outputs":
				task.Outputs, err = strList(x, name, k)
			case "before":
				task.Before, err = strList(x, name, k)
			case "run":
				task.Run, err = argvList(x, name)
			case "env":
				m, ok := x.(map[string]any)
				if !ok {
					return nil, cfgErr("[task.%s] env must be a table of strings", name)
				}
				for ek, ev := range m {
					s, ok := ev.(string)
					if !ok {
						return nil, cfgErr("[task.%s] env.%s must be a string", name, ek)
					}
					task.Env[ek] = s
				}
			default:
				return nil, cfgErr("[task.%s]: unknown key '%s' (description, inputs, outputs, before, run, env)", name, k)
			}
			if err != nil {
				return nil, err
			}
		}
		if len(task.Run) == 0 {
			return nil, cfgErr("[task.%s] has no run = [[...]]", name)
		}
		for _, b := range task.Before {
			if !contains(Phases, b) {
				return nil, cfgErr("[task.%s] before = '%s': not a phase (%s)", name, b, strings.Join(Phases, ", "))
			}
		}
		if len(task.Inputs) > 0 && len(task.Outputs) == 0 {
			return nil, cfgErr("[task.%s] has inputs but no outputs -- nothing to compare them with", name)
		}
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func strList(x any, task, key string) ([]string, error) {
	l, ok := x.([]any)
	if !ok {
		return nil, cfgErr("[task.%s] %s must be a list of strings", task, key)
	}
	var out []string
	for _, e := range l {
		s, ok := e.(string)
		if !ok {
			return nil, cfgErr("[task.%s] %s must be a list of strings", task, key)
		}
		out = append(out, s)
	}
	return out, nil
}

func argvList(x any, task string) ([][]string, error) {
	l, ok := x.([]any)
	if !ok {
		return nil, cfgErr("[task.%s] run must be a list of commands, each a list of strings: [[\"tool\", \"arg\"]]", task)
	}
	var out [][]string
	for _, c := range l {
		argv, err := strList(c, task, "run")
		if err != nil || len(argv) == 0 {
			return nil, cfgErr("[task.%s] run must be a list of commands, each a non-empty list of strings: [[\"tool\", \"arg\"]] -- there is no shell", task)
		}
		out = append(out, argv)
	}
	return out, nil
}

// Options for running tasks.
type Options struct {
	Root    string
	Tools   []tools.Tool
	ToolOpt tools.Options
	Verbose bool
	Stream  bool // show the commands' output as it comes (mbt run)
	Log     func(string)
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
}

// Runner runs tasks of one project, each at most once per mbt invocation.
type Runner struct {
	Tasks []*Task
	O     Options
	done  map[string]bool
}

// NewRunner makes a runner.
func NewRunner(t []*Task, o Options) *Runner {
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
	return &Runner{Tasks: t, O: o, done: map[string]bool{}}
}

// Phase runs every task that runs before phase.
func (r *Runner) Phase(phase string) error {
	for _, t := range r.Tasks {
		if contains(t.Before, phase) {
			if err := r.run(t, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// One runs the named task with extra arguments for {args}.
func (r *Runner) One(name string, args []string) error {
	for _, t := range r.Tasks {
		if t.Name == name {
			return r.run(t, args)
		}
	}
	var names []string
	for _, t := range r.Tasks {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return cfgErr("no task '%s': mbt.toml declares no [task.*]", name)
	}
	return cfgErr("no task '%s' (declared: %s)", name, strings.Join(names, ", "))
}

var toolRE = regexp.MustCompile(`\{tool:([^}]+)\}`)

func (r *Runner) run(t *Task, args []string) error {
	if r.done[t.Name] && args == nil {
		return nil
	}
	r.done[t.Name] = true
	o := r.O

	// tools first: their binaries are inputs of the task
	toolPaths := map[string]string{}
	for _, argv := range t.Run {
		for _, a := range argv {
			for _, m := range toolRE.FindAllStringSubmatch(a, -1) {
				if _, ok := toolPaths[m[1]]; ok {
					continue
				}
				var tool *tools.Tool
				for i := range o.Tools {
					if o.Tools[i].Name == m[1] {
						tool = &o.Tools[i]
					}
				}
				if tool == nil {
					return cfgErr("[task.%s] names {tool:%s}, which [tools] does not declare", t.Name, m[1])
				}
				p, err := tools.Ensure(o.Root, *tool, o.ToolOpt)
				if err != nil {
					return err
				}
				toolPaths[m[1]] = p
			}
		}
	}

	out := ""
	if len(t.Outputs) > 0 {
		out = t.Outputs[0]
	}
	expand := func(a string) string {
		a = toolRE.ReplaceAllStringFunc(a, func(m string) string { return toolPaths[toolRE.FindStringSubmatch(m)[1]] })
		return strings.NewReplacer("{out}", out, "{root}", o.Root).Replace(a)
	}
	var cmds [][]string
	for _, argv := range t.Run {
		var c []string
		for _, a := range argv {
			if a == "{args}" {
				c = append(c, args...)
				continue
			}
			c = append(c, expand(a))
		}
		cmds = append(cmds, c)
	}

	sigPath := filepath.Join(o.Root, ".mbt", "cmd", "task-"+t.Name)
	sig := signature(t, cmds, toolPaths)
	if args == nil && len(t.Outputs) > 0 {
		if fresh, err := upToDate(o.Root, t, sigPath, sig, toolPaths); err != nil {
			return err
		} else if fresh {
			o.Log(fmt.Sprintf("[task] %s up to date", t.Name))
			return nil
		}
	}

	o.Log(fmt.Sprintf("[task] %s", t.Name))
	for _, f := range t.Outputs {
		p := filepath.Join(o.Root, f)
		os.RemoveAll(p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
	}
	env := os.Environ()
	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, set := os.LookupEnv(k); !set {
			env = append(env, k+"="+t.Env[k])
		}
	}
	fail := func(err error) error {
		for _, f := range t.Outputs {
			os.RemoveAll(filepath.Join(o.Root, f))
		}
		os.Remove(sigPath)
		return err
	}
	for _, c := range cmds {
		if o.Verbose || o.Stream {
			o.Log("  " + quote(c))
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = o.Root
		cmd.Env = env
		var buf bytes.Buffer
		if o.Stream {
			cmd.Stdin, cmd.Stdout, cmd.Stderr = o.Stdin, o.Stdout, o.Stderr
		} else {
			cmd.Stdout, cmd.Stderr = &buf, &buf
		}
		if err := cmd.Run(); err != nil {
			if !o.Stream {
				if !o.Verbose {
					o.Log("  " + quote(c))
				}
				o.Stderr.Write(buf.Bytes())
			}
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return fail(&Error{Msg: fmt.Sprintf("task %s: %s exited with %d", t.Name, filepath.Base(c[0]), ee.ExitCode())})
			}
			return fail(&Error{Msg: fmt.Sprintf("task %s: cannot run %s: %v", t.Name, c[0], err)})
		}
		if o.Verbose && !o.Stream {
			o.Stdout.Write(buf.Bytes())
		}
	}
	for _, f := range t.Outputs {
		if _, err := os.Stat(filepath.Join(o.Root, f)); err != nil {
			return fail(&Error{Msg: fmt.Sprintf("task %s ran, but did not write its output %s", t.Name, f)})
		}
	}
	if len(t.Outputs) > 0 {
		os.MkdirAll(filepath.Dir(sigPath), 0o755)
		os.WriteFile(sigPath, []byte(sig), 0o644)
	}
	return nil
}

func quote(c []string) string {
	q := make([]string, len(c))
	for i, a := range c {
		if a == "" || strings.ContainsAny(a, " \t'\"$\\") {
			q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			q[i] = a
		}
	}
	return strings.Join(q, " ")
}

// signature: what the outputs were made from, besides the input files.
func signature(t *Task, cmds [][]string, toolPaths map[string]string) string {
	data, _ := json.Marshal(struct {
		Inputs, Outputs []string
		Cmds            [][]string
		Env             map[string]string
	}{t.Inputs, t.Outputs, cmds, t.Env})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func upToDate(root string, t *Task, sigPath, sig string, toolPaths map[string]string) (bool, error) {
	if old, err := os.ReadFile(sigPath); err != nil || string(old) != sig {
		return false, nil
	}
	var oldest time.Time
	for _, f := range t.Outputs {
		st, err := os.Stat(filepath.Join(root, f))
		if err != nil {
			return false, nil
		}
		if oldest.IsZero() || st.ModTime().Before(oldest) {
			oldest = st.ModTime()
		}
	}
	ins, err := Expand(root, t.Inputs)
	if err != nil {
		return false, err
	}
	for _, p := range toolPaths {
		ins = append(ins, p)
	}
	for _, f := range ins {
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, f)
		}
		st, err := os.Stat(p)
		if err != nil || st.ModTime().After(oldest) {
			return false, nil
		}
	}
	return true, nil
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
				return nil, cfgErr("task input %s does not exist", pat)
			}
			if !st.IsDir() {
				add(pat)
				continue
			}
			pat = strings.TrimSuffix(pat, "/") + "/**"
			add(strings.TrimSuffix(pat, "/**"))
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
		if n == 0 {
			return nil, cfgErr("task input pattern '%s' matches nothing", pat)
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
