package dist

import (
	"encoding/json"
	"os"
	"strings"
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

func acceptStepFor(t *testing.T, prereq ...string) string {
	t.Helper()
	cfg := elemCfg()
	if len(prereq) > 0 {
		var p []any
		for _, id := range prereq {
			p = append(p, id)
		}
		cfg["distribution"].(map[string]any)["smp"].(map[string]any)["prereq"] = p
	}
	d, err := Parse(cfg, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	return renderAcceptStep(d)
}

// Without a prerequisite the ACCEPT step is what mbt 2 renders: no BYPASS.
func TestAcceptStepWithoutPrereq(t *testing.T) {
	want := "//*\n" +
		"//* ---- ACCEPT -------------------------------------------------------\n" +
		"//* Accepting the FMID once fills the distribution library, so a\n" +
		"//* later RESTORE has a previous level to go back to. Without it\n" +
		"//* a RESTORE would DELETE the module instead of reverting it.\n" +
		"//* Service (PTFs) is deliberately never accepted.\n" +
		"//*\n" +
		"//ACCEPT  EXEC SMPAPP,COND=(0,NE,APPLY.HMASMP)\n"
	got := acceptStepFor(t)
	if !strings.HasPrefix(got, want) {
		t.Errorf("ACCEPT step changed:\n%s", got)
	}
	if !strings.HasSuffix(got, "//SMPCNTL  DD  *\n ACCEPT S(TABC100) DIS(WRITE) .\n/*") || strings.Contains(got, "BYPASS") {
		t.Errorf("ACCEPT step has a BYPASS or a changed command:\n%s", got)
	}
}

// With prereq the ACCEPT, and only the ACCEPT, bypasses REQ; one BYPASS(REQ)
// covers a list of prerequisites.
func TestAcceptStepBypassesReq(t *testing.T) {
	for _, ids := range [][]string{{"MS00100"}, {"MS00100", "ZP60017"}} {
		got := acceptStepFor(t, ids...)
		if !strings.Contains(got, "//ACCEPT  EXEC SMPAPP,COND=((0,NE,APPLYCHK.HMASMP),(0,NE,APPLY.HMASMP))\n") {
			t.Errorf("%v: the ACCEPT does not stop behind a failed APPLY CHECK:\n%s", ids, got)
		}
		if !strings.Contains(got, "\n ACCEPT S(TABC100) BYPASS(REQ) DIS(WRITE) .\n") {
			t.Errorf("%v: no BYPASS(REQ) on the ACCEPT:\n%s", ids, got)
		}
		if n := strings.Count(got, "BYPASS(REQ)"); n != 2 { // header comment + command
			t.Errorf("%v: %d mentions of BYPASS(REQ)", ids, n)
		}
		if !strings.Contains(got, "names "+strings.Join(ids, ", ")+" as a prerequisite") {
			t.Errorf("%v: header comment does not name the prerequisites:\n%s", ids, got)
		}
		for _, line := range strings.Split(got, "\n") {
			if len(line) > 71 && strings.HasPrefix(line, "//*") {
				t.Errorf("comment card beyond column 71: %q", line)
			}
		}
	}
}

// BYPASS(REQ) makes the ACCEPT end RC 4 by design, so the CLEANUP behind it
// must run at RC 4 and still not after RC 8; without prereq it stays at 0.
func TestCleanupAfterBypassedAccept(t *testing.T) {
	for _, tc := range []struct {
		prereq []any
		want   string
	}{
		{nil, "COND=(0,NE,ACCEPT.HMASMP)"},
		{[]any{"MS00100"}, "//         COND=((0,NE,APPLYCHK.HMASMP),(4,LT,ACCEPT.HMASMP))"},
	} {
		cfg := elemCfg()
		if tc.prereq != nil {
			cfg["distribution"].(map[string]any)["smp"].(map[string]any)["prereq"] = tc.prereq
		}
		d, err := Parse(cfg, "V1R0M0")
		if err != nil {
			t.Fatal(err)
		}
		c, err := renderCleanupStep(d, "ACCEPT.HMASMP")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(c, tc.want) {
			t.Errorf("prereq %v: want %s in\n%s", tc.prereq, tc.want, c)
		}
	}
}
