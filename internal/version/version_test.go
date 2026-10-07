package version

import "testing"

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOrder(t *testing.T) {
	seq := []string{"1.0.0-dev", "1.0.0-rc1", "1.0.0-rc2", "1.0.0", "1.0.1-dev", "1.0.1", "1.10.0"}
	for i := 1; i < len(seq); i++ {
		if Compare(mustParse(t, seq[i-1]), mustParse(t, seq[i])) >= 0 {
			t.Errorf("%s should sort before %s", seq[i-1], seq[i])
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"1.0", "1.0.0-beta", "v1.0.0", "1.0.0-rc"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		v, c string
		want bool
	}{
		{"1.5.0", ">=1.0.0", true},
		{"2.0.0", ">=1.0.0,<2.0.0", false},
		{"1.0.0", "=1.0.0", true},
		{"1.0.0-dev", ">=1.0.0", false},
		{"1.0.0-dev", "=1.0.0", false},
		{"1.4.0-dev", ">=1.4.0-dev", true},
	}
	for _, c := range cases {
		got, err := Satisfies(mustParse(t, c.v), c.c)
		if err != nil || got != c.want {
			t.Errorf("Satisfies(%s, %s) = %v, %v", c.v, c.c, got, err)
		}
	}
	if _, err := Satisfies(mustParse(t, "1.0.0"), "~1.0.0"); err == nil {
		t.Error("an unknown operator was accepted")
	}
}

func TestAllowed(t *testing.T) {
	// a prerelease satisfies >=1.0.0 numerically but is not picked for it
	if ok, _ := Allowed(mustParse(t, "1.5.0-dev"), ">=1.0.0"); ok {
		t.Error("prerelease allowed under a stable constraint")
	}
	if ok, _ := Allowed(mustParse(t, "1.5.0-dev"), ">=1.0.0-dev"); !ok {
		t.Error("prerelease refused under a prerelease constraint")
	}
}

func TestVRM(t *testing.T) {
	for s, want := range map[string]string{"1.0.0": "V1R0M0", "3.3.1": "V3R3M1", "1.0.0-dev": "V1R0M0D", "3.3.1-rc1": "V3R3M1R1"} {
		if got := mustParse(t, s).VRM(); got != want {
			t.Errorf("%s -> %s, want %s", s, got, want)
		}
	}
}

func TestCaret(t *testing.T) {
	for _, c := range []struct {
		v, r string
		ok   bool
	}{
		{"1.0.0", "^1", true}, {"1.9.3", "^1", true}, {"2.0.0", "^1", false}, {"2.0.0-dev", "^1", false},
		{"0.9.0", "^1", false}, {"1.4.0", "^1.4", true}, {"1.3.9", "^1.4", false}, {"0.3.5", "^0.3", true},
		{"0.4.0", "^0.3", false}, {"1.4.2", "^1.4.2", true}, {"1.4.1", "^1.4.2", false},
		{"1.5.0", ">=1.4.0, ^1", true},
	} {
		v, _ := Parse(c.v)
		got, err := Satisfies(v, c.r)
		if err != nil || got != c.ok {
			t.Errorf("%s in %s: %v %v", c.v, c.r, got, err)
		}
	}
	if _, err := Satisfies(Version{Major: 1}, "^x"); err == nil {
		t.Error("^x accepted")
	}
}
