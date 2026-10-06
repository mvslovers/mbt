package ext

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type env struct {
	root, home string
	log        []string
	out        bytes.Buffer
}

func setup(t *testing.T, files map[string]string) *env {
	t.Helper()
	e := &env{root: t.TempDir(), home: t.TempDir()}
	for name, body := range files {
		p := filepath.Join(e.root, name)
		if strings.HasPrefix(name, "~/") {
			p = filepath.Join(e.home, strings.TrimPrefix(name, "~/"))
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return e
}

func (v *env) load(t *testing.T, tweak ...func(*Options)) (*Engine, error) {
	o := Options{Root: v.root, Home: v.home, Version: "3.0.0-test",
		Project: Project{Name: "sbx", Version: "1.0.0", Modules: []string{"HELLO"}},
		Log:     func(s string) { v.log = append(v.log, s) }, Stdout: &v.out, Stderr: &v.out}
	for _, f := range tweak {
		f(&o)
	}
	return Load(o)
}

func (v *env) mustLoad(t *testing.T, tweak ...func(*Options)) *Engine {
	t.Helper()
	e, err := v.load(t, tweak...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (v *env) logged() string { return strings.Join(v.log, "\n") }

func TestNoLuaCostsNothing(t *testing.T) {
	v := setup(t, nil)
	e := v.mustLoad(t)
	if e.Active() || e.Before("build") != nil || e.After("deploy", nil) != nil {
		t.Error("an engine without Lua did something")
	}
}

func TestHooksInOrderProjectThenUser(t *testing.T) {
	v := setup(t, map[string]string{
		"mbt/init.lua":   `mbt.hook("after_deploy", function(ctx) ctx.log("project " .. ctx.project.name .. " " .. ctx.result.library) end)`,
		"~/init.lua":     `mbt.hook("after_deploy", function(ctx) ctx.log("user, api " .. mbt.api .. ", mbt " .. mbt.version) end)`,
		"mbt/lua/x.lua":  `return 1`,
		"~/lua/mine.lua": `return 2`,
	})
	e := v.mustLoad(t)
	if err := e.After("deploy", map[string]any{"library": "SBX.DEV.LINKLIB"}); err != nil {
		t.Fatal(err)
	}
	if v.logged() != "[mbt] project sbx SBX.DEV.LINKLIB\n[mbt] user, api 1, mbt 3.0.0-test" {
		t.Errorf("log:\n%s", v.logged())
	}
}

func TestRequire(t *testing.T) {
	v := setup(t, map[string]string{
		"mbt/init.lua":            `local a = require("webroot.img") local b = require("webroot.img") local m = require("mine") assert(a == b and a.stc == "HTTPD" and m == 42)`,
		"mbt/lua/webroot/img.lua": `return { stc = "HTTPD" }`,
		"~/lua/mine.lua":          `return 42`,
	})
	if _, err := v.load(t); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{`require("../../etc/passwd")`, `require("nope")`} {
		v := setup(t, map[string]string{"mbt/init.lua": src})
		if _, err := v.load(t); err == nil {
			t.Errorf("%s loaded", src)
		}
	}
}

func TestSandbox(t *testing.T) {
	for _, src := range []string{`os.execute("true")`, `io.open("/etc/passwd")`, `dofile("/etc/passwd")`,
		`loadfile("/etc/passwd")`, `debug.getinfo(1)`, `package.loadlib("x", "y")`} {
		v := setup(t, map[string]string{"mbt/init.lua": src})
		_, err := v.load(t)
		var xe *Error
		if err == nil || !errors.As(err, &xe) || !xe.Config {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func TestErrorsNameTheirPlace(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": "\n\nmbt.hook(\"before_build\", function(ctx)\n  local x = nil\n  return x.field\nend)"})
	e := v.mustLoad(t)
	err := e.Before("build")
	if err == nil || !strings.Contains(err.Error(), "hook before_build (mbt/init.lua:3)") || !strings.Contains(err.Error(), "mbt/init.lua:5") {
		t.Errorf("%v", err)
	}
	v = setup(t, map[string]string{"mbt/init.lua": `mbt.hook("befor_build", function() end)`})
	if _, err := v.load(t); err == nil || !strings.Contains(err.Error(), `no hook point "befor_build"`) {
		t.Errorf("unknown point: %v", err)
	}
	v = setup(t, map[string]string{"mbt/init.lua": `mbt.hook("before_build", function(`})
	_, err = v.load(t)
	var xe *Error
	if !errors.As(err, &xe) || !xe.Config {
		t.Errorf("syntax error: %v", err)
	}
}

func TestExecHasNoShell(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.hook("before_build", function(ctx)
  local out = ctx.exec { "printf", "%s|%s", "$HOME; rm -rf /", "x" }
  assert(out == "$HOME; rm -rf /|x", out)
  local env = ctx.exec { "sh", "-c", "printf %s \"$GREETING\"", env = { GREETING = "servus" } }
  assert(env == "servus", env)
  local _, code = ctx.exec { "sh", "-c", "exit 3", check = false }
  assert(code == 3)
  ctx.exec { "false" }
end)`})
	e := v.mustLoad(t)
	err := e.Before("build")
	if err == nil || !strings.Contains(err.Error(), "false exited with 1") {
		t.Errorf("%v\n%s", err, v.logged())
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.hook("after_deploy", function(ctx)
  assert(ctx.dry_run)
  ctx.exec { "touch", "made-by-exec" }
  ctx.fs.write("made-by-fs", "x")
end)`})
	e := v.mustLoad(t, func(o *Options) { o.DryRun = true })
	if err := e.After("deploy", nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"made-by-exec", "made-by-fs"} {
		if _, err := os.Stat(filepath.Join(v.root, f)); err == nil {
			t.Errorf("dry run wrote %s", f)
		}
	}
	if !strings.Contains(v.logged(), "(dry run) would run: touch made-by-exec") {
		t.Errorf("log:\n%s", v.logged())
	}
}

func TestFsStaysInTheProject(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644)
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.command("ok", function(ctx)
  ctx.fs.mkdir("build/x")
  ctx.fs.write("build/x/a.txt", "hi")
  assert(ctx.fs.read("build/x/a.txt") == "hi" and ctx.fs.exists("build/x/a.txt"))
  assert(#ctx.fs.list("build/**/*.txt") == 1)
end)
mbt.command("abs", function(ctx) ctx.fs.read("/etc/passwd") end)
mbt.command("up", function(ctx) ctx.fs.read("../x") end)
mbt.command("link", function(ctx) ctx.fs.read("out/secret") end)
mbt.command("linkw", function(ctx) ctx.fs.write("out/new", "x") end)`})
	os.Symlink(outside, filepath.Join(v.root, "out"))
	e := v.mustLoad(t)
	if err := e.Run("ok", nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"abs", "up", "link", "linkw"} {
		if err := e.Run(c, nil); err == nil {
			t.Errorf("%s: left the project", c)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Error("wrote through the symlink")
	}
}

func TestLimits(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.command("loop", function() while true do end end)
mbt.command("mem", function() local s = "x" while true do s = s .. s end end)`})
	e := v.mustLoad(t, func(o *Options) { o.CPU = 20_000_000; o.Memory = 32 << 20 })
	t0 := time.Now()
	if err := e.Run("loop", nil); err == nil || !strings.Contains(err.Error(), "CPU limit") {
		t.Errorf("loop: %v", err)
	}
	if time.Since(t0) > 10*time.Second {
		t.Error("the CPU limit took too long")
	}
	if err := e.Run("mem", nil); err == nil || !strings.Contains(err.Error(), "memory limit") {
		t.Errorf("mem: %v", err)
	}
}

const webroot = `
mbt.task {
  name = "img", description = "concatenate", before = { "package", "dist" },
  inputs = { "static/**" }, outputs = { "build/img.txt" },
  run = function(ctx)
    ctx.exec { "cp", "static/a.txt", ctx.out[1] }
    local b = ctx.fs.read("static/sub/b.txt")
    ctx.fs.write(ctx.out[1], ctx.fs.read(ctx.out[1]) .. b)
  end,
}`

func taskProject(t *testing.T) *env {
	return setup(t, map[string]string{"mbt/init.lua": webroot, "static/a.txt": "a", "static/sub/b.txt": "b"})
}

func TestTaskRunsOnceAndOnlyWhenNeeded(t *testing.T) {
	v := taskProject(t)
	e := v.mustLoad(t)
	if err := e.Before("package"); err != nil {
		t.Fatal(err)
	}
	if err := e.Before("dist"); err != nil { // same invocation: not again
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(v.root, "build/img.txt")); string(got) != "ab" {
		t.Errorf("output %q", got)
	}
	if v.logged() != "[mbt] [task] img" {
		t.Errorf("log:\n%s", v.logged())
	}

	run := func() string {
		v.log = nil
		e := v.mustLoad(t)
		if err := e.Before("package"); err != nil {
			t.Fatal(err)
		}
		return v.logged()
	}
	if l := run(); l != "[mbt] [task] img up to date" {
		t.Errorf("unchanged: %s", l)
	}
	later := time.Now().Add(2 * time.Second)
	os.WriteFile(filepath.Join(v.root, "static/sub/b.txt"), []byte("B"), 0o644)
	os.Chtimes(filepath.Join(v.root, "static/sub/b.txt"), later, later)
	if l := run(); l != "[mbt] [task] img" {
		t.Errorf("input changed: %s", l)
	}
	os.WriteFile(filepath.Join(v.root, "mbt/init.lua"), []byte(webroot+"\n-- a comment\n"), 0o644)
	if l := run(); l != "[mbt] [task] img" {
		t.Errorf("Lua changed: %s", l)
	}
}

func TestFailedTaskLeavesNoOutput(t *testing.T) {
	v := setup(t, map[string]string{"static/a.txt": "a", "mbt/init.lua": `
mbt.task { name = "img", before = { "package" }, inputs = { "static" }, outputs = { "build/img.txt" },
  run = function(ctx) ctx.fs.write(ctx.out[1], "half") error("broken") end }`})
	e := v.mustLoad(t)
	err := e.Before("package")
	if err == nil || !strings.Contains(err.Error(), "task img (mbt/init.lua:2)") || !strings.Contains(err.Error(), "broken") {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(v.root, "build/img.txt")); err == nil {
		t.Error("a failed task left its output")
	}
}

func TestCommandsAndRun(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.command { name = "deploy-desktop", description = "upload static/", run = function(ctx)
  ctx.exec { "sh", "-c", "printf '%s ' \"$@\" > args.txt", "x", table.unpack(ctx.args) }
end }
mbt.command("hello", function(ctx) ctx.log("hello") end)
` + webroot, "static/a.txt": "a", "static/sub/b.txt": "b"})
	e := v.mustLoad(t)
	if err := e.Run("deploy-desktop", []string{"--dry-run", "two words"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(v.root, "args.txt")); string(got) != "--dry-run two words " {
		t.Errorf("args %q", got)
	}
	if err := e.Run("img", nil); err != nil { // a task by name
		t.Fatal(err)
	}
	cmds := e.Commands()
	if len(cmds) != 3 || cmds[0][0] != "deploy-desktop" || cmds[0][1] != "upload static/" {
		t.Errorf("%v", cmds)
	}
	if err := e.Run("nope", nil); err == nil || !strings.Contains(err.Error(), "defined: deploy-desktop, hello, img") {
		t.Errorf("%v", err)
	}
}

func TestAfterHookErrorAndOnFailure(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.hook("after_deploy", function(ctx) error("S HTTPD failed") end)
mbt.hook("on_failure", function(ctx) ctx.log("failed: " .. ctx.result.command .. ": " .. ctx.result.error) end)`})
	e := v.mustLoad(t)
	err := e.After("deploy", nil)
	if err == nil || !strings.Contains(err.Error(), "S HTTPD failed") {
		t.Fatalf("%v", err)
	}
	e.Failure("deploy", err)
	if !strings.Contains(v.logged(), "failed: deploy: hook after_deploy") {
		t.Errorf("log:\n%s", v.logged())
	}
}

func TestBadDeclarations(t *testing.T) {
	for want, src := range map[string]string{
		"needs a name":        `mbt.task { run = function() end }`,
		"needs run":           `mbt.task { name = "x" }`,
		"is not a command":    `mbt.task { name = "x", before = { "compile" }, run = function() end }`,
		"no outputs":          `mbt.task { name = "x", inputs = { "a" }, run = function() end }`,
		"outside the project": `mbt.task { name = "x", outputs = { "../x" }, run = function() end }`,
		"defined twice":       `mbt.command("x", function() end) mbt.command("x", function() end)`,
	} {
		v := setup(t, map[string]string{"mbt/init.lua": src})
		if _, err := v.load(t); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

type fakeMVS struct{ calls []string }

func (f *fakeMVS) Request(method, path, ct string, body []byte) (int, []byte, error) {
	f.calls = append(f.calls, method+" "+path+" "+ct+" "+string(body))
	return 200, []byte(`{"items":[]}`), nil
}
func (f *fakeMVS) Token() string { return "tok" }

func TestTargetAndMVS(t *testing.T) {
	v := setup(t, map[string]string{"mbt/init.lua": `
mbt.hook("after_deploy", function(ctx)
  local tg = ctx.target()
  assert(tg.name == "lab" and tg.mvsmf.user == "IBMUSER" and tg.mvsmf.password == nil, "target")
  local code, body = ctx.mvs.request { path = "/restfiles/ds?dslevel=HTTPD" }
  assert(code == 200 and body == '{"items":[]}', body)
  ctx.mvs.request { method = "PUT", path = "/restconsoles/consoles/MBT", body = '{"cmd":"D T"}' }
  assert(ctx.mvs.token() == "tok")
end)
mbt.command("bad", function(ctx) ctx.mvs.request { path = "zosmf/x" } end)`})
	f := &fakeMVS{}
	logins := 0
	e := v.mustLoad(t, func(o *Options) {
		o.Target = func() (map[string]any, error) {
			return map[string]any{"name": "lab", "mvsmf": map[string]any{"url": "http://lab:1080", "user": "IBMUSER"}}, nil
		}
		o.MVS = func() (MVS, error) { logins++; return f, nil }
	})
	if err := e.After("deploy", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "|") != `GET /restfiles/ds?dslevel=HTTPD  |PUT /restconsoles/consoles/MBT application/json {"cmd":"D T"}` {
		t.Errorf("calls: %q", f.calls)
	}
	if err := e.Run("bad", nil); err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Errorf("%v", err)
	}
	// dry run: a GET goes out, anything else does not
	f.calls = nil
	e = v.mustLoad(t, func(o *Options) {
		o.DryRun = true
		o.Target = func() (map[string]any, error) { return map[string]any{"name": "lab", "mvsmf": map[string]any{"user": "IBMUSER"}}, nil }
		o.MVS = func() (MVS, error) { return f, nil }
	})
	if err := e.After("deploy", nil); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0], "GET") {
		t.Errorf("dry run calls: %q", f.calls)
	}
	// no target in the command
	v2 := setup(t, map[string]string{"mbt/init.lua": `mbt.command("x", function(ctx) ctx.target() end)`})
	e = v2.mustLoad(t)
	if err := e.Run("x", nil); err == nil || !strings.Contains(err.Error(), "no MVS target") {
		t.Errorf("%v", err)
	}
}
