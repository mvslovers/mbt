package ext

import "testing"

// ctx.project.deploy_target names the library mbt deploy writes to, so a
// command can act on it (restart: compress the library between stop and
// start) without reading mbt.toml itself.
func TestProjectDeployTarget(t *testing.T) {
	v := setup(t, map[string]string{
		"mbt/init.lua": `mbt.command("where", function(ctx) ctx.log("lib " .. tostring(ctx.project.deploy_target)) end)`,
	})
	e := v.mustLoad(t)
	if err := e.Run("where", nil); err != nil {
		t.Fatal(err)
	}
	if v.logged() != "[mbt] lib SBX.DEV.LINKLIB" {
		t.Errorf("log: %q", v.logged())
	}
}
