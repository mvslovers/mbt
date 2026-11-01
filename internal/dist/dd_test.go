package dist

import (
	"strings"
	"testing"
)

// brexxCfg is a product whose libraries would all be addressed by DD names of
// the SMP procedures (LINKLIB, SAMPLIB, MACLIB) without a *_dd key.
func brexxCfg() map[string]any {
	c := elemCfg(
		map[string]any{"dir": "samplib", "target": "BRX.SAMPLIB", "element": "SRC", "target_dd": "BRXSAMP", "distlib_dd": "ABRXSAMP"},
		map[string]any{"dir": "maclib", "target": "BRX.MACLIB", "element": "MAC", "target_dd": "BRXMAC", "distlib_dd": "ABRXMAC"},
	)
	smp := c["distribution"].(map[string]any)["smp"].(map[string]any)
	smp["target"], smp["lklib"], smp["distlib"] = "BRX.LINKLIB", "BRX.BRXLOAD", "BRX.ABRXLOD"
	smp["target_dd"], smp["procedure"] = "BRXLINK", "mvsce"
	return c
}

func TestOwnDDNamesInTheJobs(t *testing.T) {
	d, err := Parse(brexxCfg(), "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("warnings for a package with its own DD names: %v", d.Warnings)
	}
	d.Libraries[0].Elements = sortedElements([]string{"ONE"})
	d.Libraries[1].Elements = sortedElements([]string{"TWO"})
	assignDDs(d)
	mcs, err := AssembleMCS(d, []string{"MODA"}, "brx", "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"++SRC(ONE) DISTLIB(ABRXSAMP) SYSLIB(BRXSAMP) TXLIB(ONE) .",
		"++MAC(TWO) DISTLIB(ABRXMAC) SYSLIB(BRXMAC) TXLIB(TWO) .",
		"//COPYLOAD EXEC PGM=IEBCOPY\n//ABRXLOD  DD  DISP=SHR,DSN=BRX.ABRXLOD\n//BRXLINK  DD  DISP=SHR,DSN=BRX.LINKLIB\n",
		"  COPY INDD=ABRXLOD,OUTDD=BRXLINK\n",
		"++MOD(MODA) LKLIB(BRXLOAD) DISTLIB(ABRXLOD) .",
	} {
		if !strings.Contains(mcs, want) {
			t.Errorf("SYSMOD lacks %q:\n%s", want, mcs)
		}
	}
	// none of the procedures' libraries is named, so every DD is an addition
	apply := strings.Join(ddsOf(renderApplyDDs(d)), " ")
	if want := "BRXLINK BRXLOAD ABRXLOD BRXSAMP ABRXSAMP BRXMAC ABRXMAC ONE TWO"; apply != want {
		t.Errorf("APPLY DDs %s, want %s", apply, want)
	}
	alloc := renderAllocDDs(d)
	for _, want := range []string{"//BRXLINK  DD  DSN=BRX.LINKLIB,", "//ABRXLOD  DD  DSN=BRX.ABRXLOD,",
		"//BRXSAMP  DD  DSN=BRX.SAMPLIB,", "//ABRXSAMP DD  DSN=BRX.ASAMPLIB,", "//BRXMAC   DD  DSN=BRX.MACLIB,"} {
		if !strings.Contains(alloc, want) {
			t.Errorf("allocation lacks %q:\n%s", want, alloc)
		}
	}
	if strings.Contains(alloc, "\n//SAMPLIB ") || strings.HasPrefix(alloc, "//SAMPLIB ") {
		t.Errorf("allocation still uses the last qualifier:\n%s", alloc)
	}
	plan := receivePlan(d, map[string]string{})
	var got []string
	for _, r := range plan {
		got = append(got, r.placeholder)
	}
	if want := "CHANGE.ME.BRXLOAD CHANGE.ME.TSAMPLIB CHANGE.ME.TMACLIB"; strings.Join(got, " ") != want {
		t.Errorf("RECEIVE placeholders %v, want %s", got, want)
	}
}

func TestDDNameKeyOverrideOrder(t *testing.T) {
	// a DD of the procedure that is not given a name of its own is still an
	// override and still comes first (#315)
	c := brexxCfg()
	delete(c["distribution"].(map[string]any)["smp"].(map[string]any), "target_dd")
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ddsOf(renderApplyDDs(d)), " "); !strings.HasPrefix(got, "LINKLIB BRXLOAD") {
		t.Errorf("APPLY DDs %s", got)
	}
}

func TestDDNameWarnsForAProcedureDD(t *testing.T) {
	c := brexxCfg()
	delete(c["distribution"].(map[string]any)["smp"].(map[string]any), "target_dd")
	lib := c["distribution"].(map[string]any)["library"].([]any)[0].(map[string]any)
	delete(lib, "target_dd")
	delete(c["distribution"].(map[string]any)["library"].([]any)[1].(map[string]any), "distlib_dd")
	d, err := Parse(c, "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Warnings) != 3 {
		t.Fatalf("warnings: %v", d.Warnings)
	}
	for i, want := range []string{"[distribution.smp] target 'BRX.LINKLIB' is addressed by the DD name LINKLIB",
		"[[distribution.library]] samplib target 'BRX.SAMPLIB' is addressed by the DD name SAMPLIB",
		"[[distribution.library]] maclib distlib 'BRX.AMACLIB' is addressed by the DD name AMACLIB"} {
		if !strings.HasPrefix(d.Warnings[i], want) || !strings.Contains(d.Warnings[i], "_dd to a name of your own") || strings.Count(d.Warnings[i], "\n") != 0 {
			t.Errorf("warning %d: %s", i, d.Warnings[i])
		}
	}
}

func TestDDNameRejects(t *testing.T) {
	smp := func(c map[string]any) map[string]any {
		return c["distribution"].(map[string]any)["smp"].(map[string]any)
	}
	lib := func(c map[string]any, i int) map[string]any {
		return c["distribution"].(map[string]any)["library"].([]any)[i].(map[string]any)
	}
	for name, tc := range map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"invalid":           {func(c map[string]any) { smp(c)["target_dd"] = "1BAD" }, "not a valid ddname"},
		"too long":          {func(c map[string]any) { smp(c)["lklib_dd"] = "TOOLONGDD" }, "not a valid ddname"},
		"procedure LINKLIB": {func(c map[string]any) { smp(c)["target_dd"] = "linklib" }, "is a DD of the SMP procedures"},
		"procedure AMACLIB": {func(c map[string]any) { lib(c, 1)["distlib_dd"] = "AMACLIB" }, "is a DD of the SMP procedures"},
		"JOBLIB":            {func(c map[string]any) { smp(c)["distlib_dd"] = "JOBLIB" }, "is a DD of the SMP procedures"},
		"SMPTLIB":           {func(c map[string]any) { smp(c)["lklib_dd"] = "SMPTLIB" }, "is a DD of the SMP procedures"},
		"SMP prefix":        {func(c map[string]any) { smp(c)["lklib_dd"] = "SMPXYZ" }, "is a DD of the SMP procedures"},
		"twice":             {func(c map[string]any) { lib(c, 1)["target_dd"] = "BRXSAMP" }, "both yield the ddname 'BRXSAMP'"},
		"against smp":       {func(c map[string]any) { lib(c, 1)["target_dd"] = "BRXLINK" }, "both yield the ddname 'BRXLINK'"},
		"against a default": {func(c map[string]any) { lib(c, 1)["target_dd"] = "BRXLOAD" }, "both yield the ddname 'BRXLOAD'"},
	} {
		c := brexxCfg()
		tc.mutate(c)
		_, err := Parse(c, "V1R0M0")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

func TestDDNameKeysOnATextLibrary(t *testing.T) {
	for _, k := range []string{"target_dd", "distlib_dd"} {
		c := elemCfg(map[string]any{"dir": "samplib", "target": "ABC.SAMPLIB", k: "ABCSAMP"})
		if _, err := Parse(c, "V1R0M0"); err == nil || !strings.Contains(err.Error(), k+" only means something with element") {
			t.Errorf("%s on a text library: %v", k, err)
		}
	}
}

func TestDDNameFromAnUnusableQualifier(t *testing.T) {
	// a last qualifier with a hyphen is a data set name and no ddname; a key
	// gives the library one
	c := elemCfg(map[string]any{"dir": "m", "target": "ABC.MY-MAC", "element": "MAC"})
	if _, err := Parse(c, "V1R0M0"); err == nil || !strings.Contains(err.Error(), "or name one with target_dd") {
		t.Errorf("without the key: %v", err)
	}
	c = elemCfg(map[string]any{"dir": "m", "target": "ABC.MY-MAC", "element": "MAC", "target_dd": "ABCMAC", "distlib": "ABC.AMYMAC", "stage": "ABC.TMYMAC"})
	if _, err := Parse(c, "V1R0M0"); err != nil {
		t.Error(err)
	}
}

func TestMemberNamedLikeAnOwnDDNameGetsAnotherTXLIB(t *testing.T) {
	d, err := Parse(brexxCfg(), "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	d.Libraries[0].Elements = sortedElements([]string{"BRXSAMP", "OK"})
	assignDDs(d)
	got := d.Libraries[0].Elements
	if got[0].DD != "TX000001" || got[1].DD != "OK" {
		t.Errorf("TXLIB ddnames %+v", got)
	}
}
