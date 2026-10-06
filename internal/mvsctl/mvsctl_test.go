package mvsctl

import (
	"errors"
	"strings"
	"testing"
)

// fake docker: which objects exist, and the calls made.
type fake struct {
	exists  map[string]bool
	running bool
	calls   []string
}

func (f *fake) docker(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case args[0] == "network" && args[1] == "inspect", args[0] == "inspect" && len(args) == 2:
		if !f.exists[args[len(args)-1]] {
			return "", errors.New("no such object")
		}
	case args[0] == "inspect":
		if f.running {
			return "true", nil
		}
		return "false", nil
	}
	return "", nil
}

var cfg = FromEnv(func(string) string { return "" })

func TestUpCreates(t *testing.T) {
	f := &fake{exists: map[string]bool{}}
	if err := Up(cfg, f.docker, false, "", func(string) {}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"network inspect mvs-net",
		"network create mvs-net",
		"inspect mvs",
		"run -d --name mvs --network mvs-net -p 1080:1080 -p 3270:3270 -p 8888:8888 ghcr.io/mvslovers/mvsce-builder",
	}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls:\n%s", strings.Join(f.calls, "\n"))
	}
}

func TestUpStartsOrLeavesRunning(t *testing.T) {
	f := &fake{exists: map[string]bool{"mvs-net": true, "mvs": true}}
	Up(cfg, f.docker, true, "devbox", func(string) {})
	if got := strings.Join(f.calls, "|"); got != "network inspect mvs-net|inspect mvs|inspect -f {{.State.Running}} mvs|start mvs|network connect mvs-net devbox" {
		t.Errorf("stopped: %s", got)
	}
	f = &fake{exists: map[string]bool{"mvs-net": true, "mvs": true}, running: true}
	var log []string
	Up(cfg, f.docker, false, "", func(s string) { log = append(log, s) })
	if strings.Contains(strings.Join(f.calls, "|"), "start") || log[0] != "mvs is already running" {
		t.Errorf("running: %v %v", f.calls, log)
	}
}

func TestDown(t *testing.T) {
	f := &fake{exists: map[string]bool{"mvs": true}}
	Down(cfg, f.docker, func(string) {})
	if got := strings.Join(f.calls, "|"); got != "inspect mvs|stop mvs" {
		t.Errorf("%s", got)
	}
	f = &fake{exists: map[string]bool{}}
	if err := Down(cfg, f.docker, func(string) {}); err != nil || len(f.calls) != 1 {
		t.Errorf("missing container: %v %v", err, f.calls)
	}
}

func TestFromEnv(t *testing.T) {
	c := FromEnv(func(k string) string {
		return map[string]string{"MBT_MVS_CONTAINER": "tk5", "MBT_MVS_PORTS": "3505 3270"}[k]
	})
	if c.Container != "tk5" || strings.Join(c.Ports, ",") != "3505,3270" || c.Network != "mvs-net" {
		t.Errorf("%+v", c)
	}
}
