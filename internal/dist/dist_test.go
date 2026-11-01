package dist

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			"recv":    renderReceiveSteps(receivePlan(d, g.Files)),
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

// buildJobs builds a package with the given [distribution.smp] changes and
// returns the install job, and the accept job ("" when none was written).
func buildJobs(t *testing.T, smpExtra map[string]any) (inst, acc string) {
	t.Helper()
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, "abc-1.0.0-load.xmit"), []byte("x"), 0o644)
	cfg := elemCfg()
	smp := cfg["distribution"].(map[string]any)["smp"].(map[string]any)
	for k, v := range smpExtra {
		smp[k] = v
	}
	err := Build(cfg, "abc", "1.0.0", []Module{{Name: "ABCMOD"}}, Options{
		Root: root, DistDir: "dist", BuildDir: "build",
		Log: func(string) {}, Mtime: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "build", "dist-stage")
	b, err := os.ReadFile(filepath.Join(stage, "abc-1.0.0-inst.jcl"))
	if err != nil {
		t.Fatal(err)
	}
	inst = string(b)
	if b, err := os.ReadFile(filepath.Join(stage, "abc-1.0.0-accept.jcl")); err == nil {
		acc = string(b)
	}
	return inst, acc
}

func TestAcceptJobWithoutPrereq(t *testing.T) {
	if _, err := exec.LookPath("xmit370"); err != nil {
		t.Skip("xmit370 not on PATH")
	}
	inst, acc := buildJobs(t, nil)
	for _, not := range []string{"//ACCEPT ", "//CLEANUP ", "BYPASS"} {
		if strings.Contains(inst, not) {
			t.Errorf("install job has %q", not)
		}
	}
	for _, want := range []string{"//APPLY   EXEC SMPAPP,COND=(0,NE,APPLYCHK.HMASMP)", "abc-1.0.0-accept.jcl", "DELETE ABC.LKLIB NONVSAM SCRATCH PURGE"} {
		if !strings.Contains(inst, want) {
			t.Errorf("install job lacks %q", want)
		}
	}
	for _, want := range []string{"//ACCEPT  EXEC SMPAPP\n", "\n ACCEPT S(TABC100) DIS(WRITE) .\n", "//CLEANUP EXEC PGM=IDCAMS,COND=(0,NE,ACCEPT.HMASMP)\n"} {
		if !strings.Contains(acc, want) {
			t.Errorf("accept job lacks %q:\n%s", want, acc)
		}
	}
	if strings.Contains(acc, "BYPASS") || strings.Contains(acc, "COND=(0,NE,APPLY") {
		t.Errorf("accept job without prereq bypasses or gates on APPLY:\n%s", acc)
	}
}

// With prereq only the ACCEPT bypasses REQ, one BYPASS(REQ) covers every id,
// CLEANUP accepts the RC 4 of REQ BYPASSED, and APPLY CHECK / APPLY are
// untouched.
func TestAcceptJobBypassesReqOnly(t *testing.T) {
	if _, err := exec.LookPath("xmit370"); err != nil {
		t.Skip("xmit370 not on PATH")
	}
	for _, ids := range [][]any{{"MS00100"}, {"MS00100", "ZP60017", "TNIP800", "ZP60099"}} {
		inst, acc := buildJobs(t, map[string]any{"prereq": ids})
		if strings.Contains(inst, "BYPASS") || strings.Contains(inst, "//ACCEPT ") || strings.Contains(inst, "//CLEANUP ") {
			t.Errorf("%v: install job bypasses, accepts or cleans up", ids)
		}
		if !strings.Contains(inst, "//APPLY   EXEC SMPAPP,COND=(0,NE,APPLYCHK.HMASMP)") {
			t.Errorf("%v: APPLY changed", ids)
		}
		if n := strings.Count(acc, " ACCEPT S(TABC100) BYPASS(REQ) DIS(WRITE) .\n"); n != 1 {
			t.Errorf("%v: %d BYPASS(REQ) ACCEPT commands:\n%s", ids, n, acc)
		}
		for _, want := range []string{"HMA3590     --- <id> REQ BYPASSED", "RC 04", "COND=(4,LT,ACCEPT.HMASMP)"} {
			if !strings.Contains(acc, want) {
				t.Errorf("%v: accept job lacks %q", ids, want)
			}
		}
		if strings.Contains(acc, "APPLYCHK") {
			t.Errorf("%v: accept job names APPLYCHK", ids)
		}
	}
}

// accept_fmid = false: no ACCEPT job, CLEANUP stays behind the APPLY.
func TestNoAcceptJobWithoutAcceptFMID(t *testing.T) {
	if _, err := exec.LookPath("xmit370"); err != nil {
		t.Skip("xmit370 not on PATH")
	}
	inst, acc := buildJobs(t, map[string]any{"accept_fmid": false})
	if acc != "" {
		t.Errorf("accept job written:\n%s", acc)
	}
	if strings.Contains(inst, "//ACCEPT ") || strings.Contains(inst, "WHAT NEXT") ||
		!strings.Contains(inst, "//CLEANUP EXEC PGM=IDCAMS,COND=(0,NE,APPLY.HMASMP)") {
		t.Errorf("install job:\n%s", inst)
	}
}
