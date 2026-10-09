package main

import (
	"flag"
	"reflect"
	"testing"
)

// Options may stand anywhere among the positional arguments, as the usage
// text writes them: "mbt release 1.4.0 --next 1.5.0-dev".
func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		next string
		v    bool
		pos  []string
	}{
		{[]string{"1.4.0", "--next", "1.5.0-dev"}, "1.5.0-dev", false, []string{"1.4.0"}},
		{[]string{"--next", "1.5.0-dev", "1.4.0"}, "1.5.0-dev", false, []string{"1.4.0"}},
		{[]string{"-v", "hello", "--next=x"}, "x", true, []string{"hello"}},
		{[]string{"hello", "-v", "world"}, "", true, []string{"hello", "world"}},
		{[]string{"a", "--", "-v", "b"}, "", false, []string{"a", "-v", "b"}},
	} {
		fl := flag.NewFlagSet("t", flag.ContinueOnError)
		next := fl.String("next", "", "")
		v := fl.Bool("v", false, "")
		if err := parseArgs(fl, c.args); err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if *next != c.next || *v != c.v || !reflect.DeepEqual(fl.Args(), c.pos) {
			t.Errorf("%v: next=%q v=%v args=%q", c.args, *next, *v, fl.Args())
		}
	}
	fl := flag.NewFlagSet("t", flag.ContinueOnError)
	fl.SetOutput(new(nopWriter))
	if err := parseArgs(fl, []string{"x", "--nope"}); err == nil {
		t.Error("unknown flag accepted")
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
