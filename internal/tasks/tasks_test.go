package tasks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A task that concatenates its inputs with cp/cat -- plain argv, no shell.
func project(t *testing.T) string {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "static", "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "static", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "static", "sub", "b.txt"), []byte("b"), 0o644)
	return root
}

func decl(t *testing.T, raw map[string]any) []*Task {
	ts, err := Declared(map[string]any{"task": raw})
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func webroot() map[string]any {
	return map[string]any{"img": map[string]any{
		"inputs":  []any{"static/**"},
		"outputs": []any{"build/img.txt"},
		"before":  []any{"package", "dist"},
		"run": []any{
			[]any{"cp", "static/a.txt", "{out}"},
			[]any{"sh", "-c", "cat static/sub/b.txt >> \"$1\"", "x", "{out}"},
		},
	}}
}

func runnerFor(t *testing.T, root string, ts []*Task) (*Runner, *[]string) {
	var log []string
	return NewRunner(ts, Options{Root: root, Log: func(s string) { log = append(log, s) }, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}), &log
}

func TestPhaseRunsOnceAndSkipsWhenUpToDate(t *testing.T) {
	root := project(t)
	ts := decl(t, webroot())
	r, log := runnerFor(t, root, ts)
	if err := r.Phase("package"); err != nil {
		t.Fatal(err)
	}
	if err := r.Phase("dist"); err != nil { // same invocation: not again
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "build/img.txt")); string(got) != "ab" {
		t.Errorf("output %q", got)
	}
	if strings.Join(*log, "|") != "[task] img" {
		t.Errorf("log %q", *log)
	}

	// a new invocation, nothing changed: up to date
	r, log = runnerFor(t, root, ts)
	r.Phase("package")
	if strings.Join(*log, "|") != "[task] img up to date" {
		t.Errorf("second run: %q", *log)
	}

	// an input changes: runs again
	later := time.Now().Add(2 * time.Second)
	os.WriteFile(filepath.Join(root, "static", "sub", "b.txt"), []byte("B"), 0o644)
	os.Chtimes(filepath.Join(root, "static", "sub", "b.txt"), later, later)
	r, _ = runnerFor(t, root, ts)
	r.Phase("package")
	if got, _ := os.ReadFile(filepath.Join(root, "build/img.txt")); string(got) != "aB" {
		t.Errorf("after an input change: %q", got)
	}

	// the definition changes: runs again
	w := webroot()
	w["img"].(map[string]any)["run"] = []any{[]any{"cp", "static/sub/b.txt", "{out}"}}
	r, log = runnerFor(t, root, decl(t, w))
	r.Phase("package")
	if strings.Join(*log, "|") != "[task] img" {
		t.Errorf("after a definition change: %q", *log)
	}
}

func TestFailureLeavesNoOutput(t *testing.T) {
	root := project(t)
	w := webroot()
	w["img"].(map[string]any)["run"] = []any{
		[]any{"cp", "static/a.txt", "{out}"},
		[]any{"false"},
	}
	r, _ := runnerFor(t, root, decl(t, w))
	err := r.Phase("package")
	if err == nil || !strings.Contains(err.Error(), "false exited with 1") {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "build/img.txt")); err == nil {
		t.Error("a failed task left its output")
	}
}

func TestRunWithArgsAndEnv(t *testing.T) {
	root := project(t)
	ts := decl(t, map[string]any{"show": map[string]any{
		"env": map[string]any{"GREETING": "hello"},
		"run": []any{[]any{"sh", "-c", "echo \"$GREETING $*\" > out.txt", "x", "{args}"}},
	}})
	r, _ := runnerFor(t, root, ts)
	if err := r.One("show", []string{"--dry-run", "two words"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "out.txt")); string(got) != "hello --dry-run two words\n" {
		t.Errorf("%q", got)
	}
	// the environment wins over the task's default
	t.Setenv("GREETING", "servus")
	r, _ = runnerFor(t, root, ts)
	r.One("show", []string{})
	if got, _ := os.ReadFile(filepath.Join(root, "out.txt")); string(got) != "servus \n" {
		t.Errorf("%q", got)
	}
	if err := r.One("nope", nil); err == nil || !strings.Contains(err.Error(), "declared: show") {
		t.Errorf("%v", err)
	}
}

func TestDeclaredRefuses(t *testing.T) {
	for want, raw := range map[string]map[string]any{
		"no run":              {"x": map[string]any{}},
		"there is no shell":   {"x": map[string]any{"run": []any{"make webroot"}}},
		"not a phase":         {"x": map[string]any{"run": []any{[]any{"true"}}, "before": []any{"compile"}}},
		"no outputs":          {"x": map[string]any{"run": []any{[]any{"true"}}, "inputs": []any{"a"}}},
		"unknown key 'shell'": {"x": map[string]any{"run": []any{[]any{"true"}}, "shell": "sh"}},
	} {
		if _, err := Declared(map[string]any{"task": raw}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestUnknownTool(t *testing.T) {
	root := project(t)
	r, _ := runnerFor(t, root, decl(t, map[string]any{"x": map[string]any{"run": []any{[]any{"{tool:nope}"}}}}))
	if err := r.One("x", nil); err == nil || !strings.Contains(err.Error(), "[tools] does not declare") {
		t.Errorf("%v", err)
	}
}

func TestExpand(t *testing.T) {
	root := project(t)
	got, err := Expand(root, []string{"static"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "static,static/a.txt,static/sub,static/sub/b.txt" {
		t.Errorf("%v", got)
	}
	got, _ = Expand(root, []string{"static/**/*.txt"})
	if strings.Join(got, ",") != "static/a.txt,static/sub/b.txt" {
		t.Errorf("%v", got)
	}
	if _, err := Expand(root, []string{"nothing/**"}); err == nil {
		t.Error("a pattern matching nothing passed")
	}
}
