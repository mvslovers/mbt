package main

import (
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

// t.testlib in a TSO test names the library mbt test --mvs deploys to,
// [deploy] test_target included.
func TestTestLibrary(t *testing.T) {
	p := &project.Project{Name: "sums", Version: "1.0.0-dev", Raw: map[string]any{}}
	if got := testLibrary(p, "IBMUSER"); got != "IBMUSER.SUMS.V1R0M0D.TESTLIB" {
		t.Errorf("default: %s", got)
	}
	p.Raw["test_deploy"] = map[string]any{"target": "SUMS.TEST.LOADLIB"}
	if got := testLibrary(p, "IBMUSER"); got != "SUMS.TEST.LOADLIB" {
		t.Errorf("test_target: %s", got)
	}
}
