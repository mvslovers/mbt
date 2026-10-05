package tpl

import "testing"

func TestSafeSubstitute(t *testing.T) {
	vars := map[string]string{"A": "x", "B_C": "y"}
	for in, want := range map[string]string{
		"$A ${A}":          "x x",
		"$B_C.":            "y.",
		"$$A":              "$A",
		"$UNKNOWN ${NOPE}": "$UNKNOWN ${NOPE}",
		"cost $ 5":         "cost $ 5",
		"$Ab":              "$Ab", // the longest name is taken, and it is unknown
	} {
		if got := SafeSubstitute(in, vars); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
