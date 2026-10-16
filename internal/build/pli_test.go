package build

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/toolchain"
)

// A PL/I source is two steps: pl1c370 to build/pli/<stem>.asm (with a depfile
// of its %INCLUDE members), then as370 on that into the object of the .pli's
// stem. Unlike cobc370, pl1c370 takes the source last and writes -MD/-MF.
func TestPLISteps(t *testing.T) {
	b := New(&project.Project{PLIFlags: []string{"-I", "inc"}, ASFlags: []string{"-I", "maclib"}},
		&toolchain.Toolchain{AS: "as370", PL1C: "/x/pl1c370"}, Options{BuildDir: "build"})
	tr, err := b.translatePLIStep("src/Hello.PLI")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/x/pl1c370", "-I", "inc", "-MD", "-MF", "build/pli/Hello.d", "-o", "build/pli/Hello.asm", "src/Hello.PLI"}
	if !reflect.DeepEqual(tr.argv, want) || tr.out != "build/pli/Hello.asm" || tr.depfile != "build/pli/Hello.d" || tr.kind != "pl1c" {
		t.Errorf("translate %+v", tr)
	}
	as := b.compileStep("src/Hello.PLI", "build/Hello.o")
	if as.kind != "as" || !reflect.DeepEqual(as.inputs, []string{"build/pli/Hello.asm"}) ||
		!reflect.DeepEqual(as.argv, []string{"as370", "-I", "maclib", "-o", "build/Hello.o", "build/pli/Hello.asm"}) || as.depfile != "" {
		t.Errorf("assemble %+v", as)
	}
	// .pl1 is the same language, translated the same way.
	if tr2, _ := b.translatePLIStep("x.pl1"); tr2.out != "build/pli/x.asm" || tr2.kind != "pl1c" {
		t.Errorf("pl1 ext: %+v", tr2)
	}
	b.T.PL1C = ""
	if _, err := b.translatePLIStep("src/x.pli"); err == nil || !strings.Contains(err.Error(), "no pl1c370") {
		t.Errorf("no pl1c370: %v", err)
	}
}
