package dist

import (
	"encoding/json"
	"os"
	"testing"
)

// The golden files were rendered by mbt v2's scripts/mbt/distribution.py.
func TestMatchesV2(t *testing.T) {
	for _, name := range []string{"upgrade", "noaccept"} {
		data, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var g struct {
			Cfg     map[string]any
			Modules []string
			Aliases map[string][]string
			Files   map[string]string
			Out     map[string]string
		}
		if err := json.Unmarshal(data, &g); err != nil {
			t.Fatal(err)
		}
		d, err := Parse(g.Cfg, "V1R2M0")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mcs, err := AssembleMCS(d, g.Modules, "prod", "1.2.0", g.Aliases)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cleanup, _ := renderCleanupStep(d, "ACCEPT.HMASMP")
		jc, _ := jobcard(jobName("prod", "INS"), "PROD INSTALL")
		got := map[string]string{
			"mcs": mcs, "apply": renderApplyDDs(d), "alloc": renderAllocDDs(d),
			"recv": renderReceiveSteps(receivePlan(d, g.Files)), "cond": acceptCond(d),
			"cleanup": cleanup, "jobname": jobName("brexx370", "ALC"), "jobcard": jc,
		}
		for k, want := range g.Out {
			if got[k] != want {
				t.Errorf("%s/%s differs from v2:\n--- got\n%s\n--- want\n%s", name, k, got[k], want)
			}
		}
	}
}

func TestRejects(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"distribution": map[string]any{"smp": map[string]any{
			"fmid": "TABC100", "system": "Z038", "lklib": "ABC.LKLIB", "target": "ABC.LINKLIB", "distlib": "ABC.AABCLOD"}}}
	}
	smp := func(c map[string]any) map[string]any {
		return c["distribution"].(map[string]any)["smp"].(map[string]any)
	}
	for name, mutate := range map[string]func(map[string]any){
		"short fmid":     func(c map[string]any) { smp(c)["fmid"] = "TAB" },
		"unknown key":    func(c map[string]any) { smp(c)["fmdi"] = "x" },
		"self delete":    func(c map[string]any) { smp(c)["delete"] = []any{"TABC100"} },
		"lklib = target": func(c map[string]any) { smp(c)["lklib"] = "ABC.LINKLIB" },
		"reserved dd":    func(c map[string]any) { smp(c)["lklib"] = "ABC.SMPTLIB" },
		"long qualifier": func(c map[string]any) { smp(c)["target"] = "ABC.TOOLONGNAME" },
		"dd collision":   func(c map[string]any) { smp(c)["lklib"] = "XYZ.LINKLIB" },
	} {
		c := base()
		mutate(c)
		if _, err := Parse(c, "V1R0M0"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
