package dist

import (
	"strings"
	"testing"
)

// moveCfg is a product that moves from DD LINKLIB (UFSD.LINKLIB) to a DD of
// its own, and says which libraries its predecessor installed into.
func moveCfg() map[string]any {
	c := elemCfg()
	smp := c["distribution"].(map[string]any)["smp"].(map[string]any)
	smp["target"], smp["lklib"], smp["distlib"] = "ABC.LINKLIB", "ABC.ABCLOAD", "ABC.AABCLOD"
	smp["target_dd"], smp["procedure"] = "ABCLINK", "mvsce"
	smp["delete"] = []any{"TABC090"}
	smp["predecessor"] = map[string]any{"LINKLIB": "ABC.LINKLIB", "AOLDLOD": "ABC.AOLDLOD"}
	return c
}

func predSMP(c map[string]any) map[string]any {
	return c["distribution"].(map[string]any)["smp"].(map[string]any)
}

func TestPredecessorCardsLeadTheStep(t *testing.T) {
	d, err := Parse(moveCfg(), "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	apply := renderApplyDDs(d)
	// LINKLIB is a DD of SMPAPP: an override, so it comes before every addition
	if got, want := strings.Join(ddsOf(apply), " "), "LINKLIB ABCLINK ABCLOAD AABCLOD AOLDLOD"; got != want {
		t.Errorf("APPLY DDs %s, want %s", got, want)
	}
	for _, want := range []string{"//HMASMP.LINKLIB  DD  DISP=SHR,DSN=ABC.LINKLIB", "//HMASMP.AOLDLOD  DD  DISP=SHR,DSN=ABC.AOLDLOD"} {
		if !strings.Contains(apply, want) {
			t.Errorf("APPLY lacks %q:\n%s", want, apply)
		}
	}
}

func TestPredecessorWithoutAProcedureAgrees(t *testing.T) {
	c := moveCfg()
	delete(predSMP(c), "procedure")
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ddsOf(renderApplyDDs(d)), " "), "LINKLIB ABCLINK ABCLOAD AABCLOD AOLDLOD"; got != want {
		t.Errorf("APPLY DDs %s, want %s", got, want)
	}
	// ASAMPLIB is a DD of one procedure only: no block is right for both
	predSMP(c)["predecessor"] = map[string]any{"ASAMPLIB": "ABC.ASAMPLIB"}
	if _, err := Parse(c, "V1R0M0"); err == nil || !strings.Contains(err.Error(), "ASAMPLIB") || !strings.Contains(err.Error(), "Set `procedure`") {
		t.Errorf("%v", err)
	}
}

func TestPredecessorSameDDSameDSNIsOneCard(t *testing.T) {
	c := moveCfg()
	delete(predSMP(c), "target_dd")
	predSMP(c)["predecessor"] = map[string]any{"linklib": "abc.linklib", "AABCLOD": "ABC.AABCLOD"}
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ddsOf(renderApplyDDs(d)), " "), "LINKLIB ABCLOAD AABCLOD"; got != want {
		t.Errorf("APPLY DDs %s, want %s", got, want)
	}
}

func TestPredecessorSameDSNUnderAnotherDDIsEmitted(t *testing.T) {
	// two DDs, one data set: measured, supported (MVS-1423). The DELETE runs
	// before the COPY, so the card for the old DD is required; this pins the cards.
	c := moveCfg()
	predSMP(c)["predecessor"] = map[string]any{"LINKLIB": "ABC.LINKLIB"}
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ddsOf(renderApplyDDs(d)), " "), "LINKLIB ABCLINK ABCLOAD AABCLOD"; got != want {
		t.Errorf("APPLY DDs %s, want %s", got, want)
	}
}

func TestPredecessorRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"same DD, other DSN": {func(s map[string]any) {
			s["predecessor"] = map[string]any{"ABCLINK": "ABC.V1.LINKLIB"}
		}, "'ABC.V1.LINKLIB' and 'ABC.LINKLIB' both yield the ddname 'ABCLINK'"},
		"same DD as a distlib, other DSN": {func(s map[string]any) {
			s["predecessor"] = map[string]any{"AABCLOD": "ABC.OLD.AABCLOD"}
		}, "both yield the ddname 'AABCLOD'"},
		"no delete": {func(s map[string]any) { s["delete"] = []any{} }, "predecessor needs a non-empty delete"},
		"bad DD":    {func(s map[string]any) { s["predecessor"] = map[string]any{"1BAD": "ABC.OLD"} }, "not a valid ddname"},
		"long DD":   {func(s map[string]any) { s["predecessor"] = map[string]any{"TOOLONGDD": "ABC.OLD"} }, "not a valid ddname"},
		"bad DSN":   {func(s map[string]any) { s["predecessor"] = map[string]any{"OLDLIB": "ABC..OLD"} }, "invalid qualifier"},
		"no string": {func(s map[string]any) { s["predecessor"] = map[string]any{"OLDLIB": int64(4)} }, "must be a data set name"},
		"not a table": {func(s map[string]any) {
			s["predecessor"] = []any{"ABC.OLD"}
		}, "must be a table"},
		"vrm":      {func(s map[string]any) { s["predecessor"] = map[string]any{"OLDLIB": "ABC.@VRM@.OLD"} }, "@VRM@"},
		"SYSPRINT": {func(s map[string]any) { s["predecessor"] = map[string]any{"SYSPRINT": "ABC.OLD"} }, "reads by name"},
		"SMPCDS":   {func(s map[string]any) { s["predecessor"] = map[string]any{"SMPCDS": "ABC.OLD"} }, "reads by name"},
		"STEPLIB":  {func(s map[string]any) { s["predecessor"] = map[string]any{"STEPLIB": "ABC.OLD"} }, "reads by name"},
		"ASMPRINT": {func(s map[string]any) { s["predecessor"] = map[string]any{"ASMPRINT": "ABC.OLD"} }, "reads by name"},
	} {
		c := moveCfg()
		tc.mutate(predSMP(c))
		_, err := Parse(c, "V1R0M0")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

func TestPredecessorEmptyTableIsNoPredecessor(t *testing.T) {
	c := moveCfg()
	predSMP(c)["predecessor"] = map[string]any{}
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	delete(predSMP(c), "predecessor")
	e, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if renderApplyDDs(d) != renderApplyDDs(e) {
		t.Errorf("an empty table changes the block:\n%s\n%s", renderApplyDDs(d), renderApplyDDs(e))
	}
}

func TestPredecessorDDIsNotATXLIBName(t *testing.T) {
	c := moveCfg()
	lib := map[string]any{"dir": "samplib", "target": "ABC.SAMPLIB", "element": "SRC", "target_dd": "ABCSAMP", "distlib_dd": "AABCSMP"}
	c["distribution"].(map[string]any)["library"] = []any{lib}
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	// a member named like the predecessor's DD would be a second DD of the name
	d.Libraries[0].Elements = sortedElements([]string{"AOLDLOD", "OK"})
	assignDDs(d)
	if got := d.Libraries[0].Elements; got[0].DD != "TX000001" || got[1].DD != "OK" {
		t.Errorf("TXLIB ddnames %+v", got)
	}
	seen := map[string]int{}
	for _, n := range ddsOf(renderApplyDDs(d)) {
		seen[n]++
	}
	for n, k := range seen {
		if k != 1 {
			t.Errorf("DD %s is on %d cards", n, k)
		}
	}
}

func TestNoPredecessorLeavesTheBlockAlone(t *testing.T) {
	c := moveCfg()
	delete(predSMP(c), "predecessor")
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ddsOf(renderApplyDDs(d)), " "), "ABCLINK ABCLOAD AABCLOD"; got != want {
		t.Errorf("APPLY DDs %s, want %s", got, want)
	}
}
