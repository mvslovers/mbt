package build

import (
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

// mbt#200: src/mean.c and src2/mean.c both became build/mean.o, and the
// second was never compiled.
func TestObjectNameClash(t *testing.T) {
	p := &project.Project{
		Modules:  []*project.Unit{{Name: "SUMS", Sources: []string{"src/sums.c", "src2/mean.c"}}},
		Internal: []string{"src/mean.c", "src/sums.c"},
	}
	err := CheckObjectNames(p, "build")
	if err == nil || !strings.Contains(err.Error(), "src2/mean.c") || !strings.Contains(err.Error(), "src/mean.c") || !strings.Contains(err.Error(), "build/mean.o") {
		t.Errorf("%v", err)
	}
	// the same source in several units is one object, not a clash
	p.Modules[0].Sources = []string{"src/sums.c", "src/mean.c"}
	if err := CheckObjectNames(p, "build"); err != nil {
		t.Errorf("shared source: %v", err)
	}
	// a .c and an .asm of one stem clash too
	p.Tests = []*project.Unit{{Name: "T", Sources: []string{"asm/sums.asm"}}}
	if err := CheckObjectNames(p, "build"); err == nil {
		t.Error(".c and .asm of one stem passed")
	}
}
