package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A project whose declared dependencies are not staged (after distclean, or
// a fresh clone) is told to run mbt deps, instead of compiling into a wall
// of "No such file or directory" for the dependency's headers.
func TestUnstagedDependency(t *testing.T) {
	root := t.TempDir()
	raw := map[string]any{"dependencies": map[string]any{"mvslovers/httpd": ">=4.2.0-dev", "mvslovers/ufsd": ">=1.4.0-dev"}}
	err := CheckStaged(root, raw)
	if err == nil || !strings.Contains(err.Error(), "mbt deps") || !strings.Contains(err.Error(), "httpd") || !strings.Contains(err.Error(), "ufsd") {
		t.Errorf("nothing staged: %v", err)
	}
	os.MkdirAll(filepath.Join(root, ".mbt", "deps", "httpd", "include"), 0o755)
	os.MkdirAll(filepath.Join(root, ".mbt", "deps", "ufsd", "include"), 0o755)
	if err := CheckStaged(root, raw); err != nil {
		t.Errorf("all staged: %v", err)
	}
	if err := CheckStaged(root, map[string]any{}); err != nil {
		t.Errorf("no dependencies: %v", err)
	}
}
