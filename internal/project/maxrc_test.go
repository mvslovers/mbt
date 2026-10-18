package project

import (
	"errors"
	"strings"
	"testing"
)

// [build] max_rc: the highest as370 rc that passes, 4 when absent; anything
// but an integer 0..16 is a configuration error (mbt#286).
func TestMaxRC(t *testing.T) {
	cases := []struct {
		val  string
		want int
		bad  bool
	}{
		{"", 4, false},
		{"[build]\nmax_rc = 0\n", 0, false},
		{"[build]\nmax_rc = 16\n", 16, false},
		{"[build]\nmax_rc = 17\n", 0, true},
		{"[build]\nmax_rc = -1\n", 0, true},
		{"[build]\nmax_rc = \"4\"\n", 0, true},
		{"[build]\nmax_rc = 4.0\n", 0, true},
	}
	for _, c := range cases {
		p, err := load(t, "[project]\nname = \"x\"\n"+c.val)
		if c.bad {
			var ce *ConfigError
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), "max_rc") {
				t.Errorf("%q: want a ConfigError, got %v", c.val, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.val, err)
		}
		if p.MaxRC != c.want {
			t.Errorf("%q: MaxRC = %d, want %d", c.val, p.MaxRC, c.want)
		}
	}
}

// [build] max_rc is a schema 3 key and reaches the model; absent, it is 4;
// out of range, the load fails.
func TestV3MaxRC(t *testing.T) {
	for _, c := range []struct {
		edit string
		want int
	}{{"", 4}, {"max_rc = 0\n", 0}, {"max_rc = 8\n", 8}} {
		p, err := LoadV3(v3Tree(t, strings.Replace(v3Base, "[build]\n", "[build]\n"+c.edit, 1)), FileV3)
		if err != nil || p.MaxRC != c.want {
			t.Errorf("%q: %v, MaxRC %v", c.edit, err, p)
		}
	}
	_, err := LoadV3(v3Tree(t, strings.Replace(v3Base, "[build]\n", "[build]\nmax_rc = 20\n", 1)), FileV3)
	var ce *ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "[build] max_rc = 20") {
		t.Errorf("max_rc = 20: %v", err)
	}
}
