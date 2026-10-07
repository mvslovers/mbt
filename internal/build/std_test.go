package build

import (
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

// An mbt.toml project compiles as C99 (gnu99) unless its cflags say
// otherwise; the project's -std comes later on the line and wins. A v2
// project.toml compiles as it did (cc370's own default, gnu89).
func TestStdDefault(t *testing.T) {
	line := func(file string, cflags ...string) string {
		b := &Builder{P: &project.Project{File: file, Root: t.TempDir(), CFlags: cflags}, O: Options{IncludeDir: ".mbt/include"}}
		return strings.Join(b.cflags(), " ")
	}
	if got := line(project.FileV3); !strings.HasPrefix(got, "-O1 -std=gnu99 -Wall -Wextra -Werror") {
		t.Errorf("v3: %s", got)
	}
	if got := line(project.FileV3, "-std=gnu89"); !strings.Contains(got, "-std=gnu99 -Wall -Wextra -Werror -std=gnu89") {
		t.Errorf("v3 override: %s", got)
	}
	if got := line(project.FileV2); strings.Contains(got, "-std=") {
		t.Errorf("v2: %s", got)
	}
}
