package ext

import (
	"strings"
	"testing"
)

// A description written as the second argument -- the natural first try --
// gets an error that shows the table form, which is where it belongs.
func TestCommandDescriptionAsSecondArgument(t *testing.T) {
	v := setup(t, map[string]string{
		"mbt/init.lua": `mbt.command("restart", "stop, compress, start", function(ctx) end)`,
	})
	_, err := v.load(t)
	if err == nil || !strings.Contains(err.Error(), `mbt.command{ name = "restart", description = `) {
		t.Errorf("error: %v", err)
	}
}
