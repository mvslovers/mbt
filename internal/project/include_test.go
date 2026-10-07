package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIncludeDefault(t *testing.T) {
	root := t.TempDir()
	if got := IncludeDirs(root, []string{"x"}); strings.Join(got, ",") != "x" {
		t.Errorf("no include/ on disk: %v", got)
	}
	os.Mkdir(filepath.Join(root, "include"), 0o755)
	for _, c := range []struct{ in, want string }{
		{"", "include"},
		{"x", "include,x"},
		{"x,include", "x,include"},   // named: stays where it is
		{"include/,x", "include/,x"}, // named with a slash
	} {
		var in []string
		if c.in != "" {
			in = strings.Split(c.in, ",")
		}
		if got := strings.Join(IncludeDirs(root, in), ","); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEffectiveCFlags(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"-I include -Wall -Werror", "-I include"},
		{"-Wall -Wextra -Werror -I include", "-I include"},
		{"-I include -I x -I include", "-I include -I x"},
		{"-Wno-unused -Wall", "-Wno-unused -Wall"}, // -Wall re-enables: kept
		{"-O0 -DX=1", "-O0 -DX=1"},
	} {
		if got := strings.Join(EffectiveCFlags(strings.Fields(c.in)), " "); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}
