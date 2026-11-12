package dist

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// With a module shipped as source the RECV is also bypassed behind a GENER1
// or an ASSEM that failed. A COND test against a bypassed step is not made,
// so the APPLYCHK, the APPLY and the CLEANUP name those two steps as well:
// otherwise an ASSEM ending 8 would leave a SYSMOD received by an earlier
// run to be applied again (measured on MVS/CE, mbt#380).
func TestSystemModuleGatesEveryStepOnAssembly(t *testing.T) {
	inst, _, err := buildSystem(t, asmSource+asmCard80+"\n         END   IGX00099\n", map[string]any{"accept_fmid": false})
	if err != nil {
		t.Fatal(err)
	}
	checkJobText(t, "install job", inst, []string{
		"//RECV    EXEC SMPREC,COND=((0,NE,RECV1),(0,NE,GENER1),(0,NE,ASSEM))\n",
		"//APPLYCHK EXEC SMPAPP,COND=((0,NE,RECV1),(0,NE,GENER1),(0,NE,ASSEM),\n" +
			"//             (0,NE,RECV.HMASMP))\n",
		"//APPLY   EXEC SMPAPP,COND=((0,NE,RECV1),(0,NE,GENER1),(0,NE,ASSEM),\n" +
			"//             (0,NE,RECV.HMASMP),(0,NE,APPLYCHK.HMASMP))\n",
		"//CLEANUP EXEC PGM=IDCAMS,COND=((4,LT,APPLY.HMASMP),\n" +
			"//             (0,NE,APPLYCHK.HMASMP),(0,NE,RECV.HMASMP),(0,NE,ASSEM),\n" +
			"//             (0,NE,GENER1),(0,NE,RECV1))\n",
	}, nil)
}

// condCard keeps every card within 71 columns and breaks after a comma, with
// as many tests on a card as fit; JCL takes eight tests at most, and a
// package of six libraries with a module shipped as source needs six on the
// CLEANUP.
func TestCondCardWrapsAfterAComma(t *testing.T) {
	var tests []string
	for i := 1; i <= 8; i++ {
		tests = append(tests, fmt.Sprintf("0,NE,STEP%d.HMASMP", i))
	}
	card := condCard("//CLEANUP EXEC PGM=IDCAMS,COND=", tests)
	lines := strings.Split(card, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected several cards:\n%s", card)
	}
	for i, l := range lines {
		if len(l) > maxCardCol {
			t.Errorf("card %d reaches column %d:\n%s", i+1, len(l), l)
		}
		if i > 0 && !strings.HasPrefix(l, "//             (") {
			t.Errorf("continuation %d does not start in column 16:\n%s", i+1, l)
		}
		if i < len(lines)-1 && !strings.HasSuffix(l, ",") {
			t.Errorf("card %d does not end with a comma:\n%s", i+1, l)
		}
	}
	joined := strings.ReplaceAll(card, "\n//             ", "")
	if want := "//CLEANUP EXEC PGM=IDCAMS,COND=" + condList(tests...); joined != want {
		t.Errorf("tests out of order or lost:\n%s\nwant\n%s", joined, want)
	}
	if got := condCard("//RECV    EXEC SMPREC,COND=", []string{"0,NE,RECV1"}); got != "//RECV    EXEC SMPREC,COND=(0,NE,RECV1)" {
		t.Errorf("single test: %s", got)
	}
}

// Without a module for the product's target library mbt package builds no
// load XMIT, so the reason has to be reported before the XMIT is looked for.
func TestAllSystemModulesIsReportedBeforeTheMissingXMIT(t *testing.T) {
	if _, err := exec.LookPath("xmit370"); err != nil {
		t.Skip("xmit370 not on PATH")
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dist"), 0o755)
	os.MkdirAll(filepath.Join(root, "asm"), 0o755)
	os.WriteFile(filepath.Join(root, "asm", "igx00099.asm"), []byte(asmSource+"         END   IGX00099\n"), 0o644)
	cfg := elemCfg()
	cfg["distribution"].(map[string]any)["smp"].(map[string]any)["target"] = "ABC.ABCLINK"
	err := Build(cfg, "abc", "1.0.0", []Module{systemModule()}, Options{
		Root: root, DistDir: "dist", BuildDir: "build",
		Log: func(string) {}, Mtime: time.Unix(0, 0)})
	if err == nil || !strings.Contains(err.Error(), "every shipped module goes into a system library") {
		t.Fatalf("want the system-library message, got %v", err)
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("the missing XMIT hides the reason: %v", err)
	}
}
