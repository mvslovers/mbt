package build

import (
	"strings"
	"testing"

	"github.com/mvslovers/mbt/internal/project"
)

// mbt#209: `mbt build NOSUCH` built nothing and reported success.
func TestCheckNames(t *testing.T) {
	p := &project.Project{
		Modules: []*project.Unit{{Name: "HELLO"}},
		Tests:   []*project.Unit{{Name: "TSTHELLO", Test: true}},
	}
	if err := CheckNames(p, nil); err != nil {
		t.Errorf("no names: %v", err)
	}
	if err := CheckNames(p, []string{"hello", "TSTHELLO"}); err != nil {
		t.Errorf("known names: %v", err)
	}
	err := CheckNames(p, []string{"hello", "nosuch", "NOSUCH", "other"})
	be, ok := err.(*Error)
	if !ok || be.Code != 2 || !strings.HasSuffix(be.Msg, ": NOSUCH, OTHER") {
		t.Errorf("unknown names: %#v", err)
	}
	// the Builder refuses before it touches the file system
	b := New(p, nil, Options{Only: []string{"nosuch"}})
	if err := b.Run(); err == nil || !strings.Contains(err.Error(), "NOSUCH") {
		t.Errorf("Run: %v", err)
	}
}
