package main

import (
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

// cobc370 is looked up for COBOL anywhere the build compiles: a module, a
// test, the [lib] archive or [internal] -- a library project has no units.
func TestCobolSourceEverywhere(t *testing.T) {
	cases := map[string]*project.Project{
		"src/u.cbl": {Modules: []*project.Unit{{Sources: []string{"src/a.c", "src/u.cbl"}}}},
		"lib/l.cob": {Lib: &project.Lib{Sources: []string{"lib/x.c", "lib/l.cob"}}},
		"int/i.cbl": {Internal: []string{"int/i.cbl"}},
		"":          {Modules: []*project.Unit{{Sources: []string{"src/a.c", "asm/b.asm"}}}, Lib: &project.Lib{Sources: []string{"x.c"}}},
	}
	for want, p := range cases {
		if got := cobolSource(p); got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	}
}
