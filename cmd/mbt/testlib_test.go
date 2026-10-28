package main

import (
	"testing"
	"time"

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

// A prerelease without an explicit [smp] fmid is a configuration error for
// mbt package and mbt dist (exit 2), not a warning.
func TestRunDistRefusesDistError(t *testing.T) {
	p := &project.Project{
		Name: "sums", Version: "1.4.0-dev", Root: t.TempDir(),
		Raw:       map[string]any{"distribution": map[string]any{"readme": "README.md"}},
		DistError: "version 1.4.0-dev is a prerelease",
	}
	if got := runDist(p, time.Time{}); got != exitConfig {
		t.Errorf("runDist = %d, want %d", got, exitConfig)
	}
}
