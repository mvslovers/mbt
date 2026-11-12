package dist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An assembler card that uses all 80 columns: a continuation in 72 and a
// sequence number in 73-80. The job's own cards stop at 71; this one has to
// travel unchanged.
var asmCard80 = "         DC    C'A LONG CONSTANT UP TO THE CONTINUATION COLUMN'        X00000010"

const asmSource = `IGX00099 CSECT
         BR    14
`

func systemModule() Module {
	return Module{Name: "IGX00099", System: &SystemModule{
		Syslib: "LPALIB", Distlib: "AOST4", Parm: "MAP,LIST,NCAL,RENT,REUS,REFR", Source: "asm/igx00099.asm"}}
}

// buildSystem builds a package of ABCMOD and IGX00099 and returns the
// install and the accept job.
func buildSystem(t *testing.T, source string, smpExtra map[string]any, mods ...Module) (inst, acc string, err error) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dist"), 0o755)
	os.MkdirAll(filepath.Join(root, "asm"), 0o755)
	os.WriteFile(filepath.Join(root, "dist", "abc-1.0.0-load.xmit"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "asm", "igx00099.asm"), []byte(source), 0o644)
	cfg := elemCfg()
	smp := cfg["distribution"].(map[string]any)["smp"].(map[string]any)
	// a target of its own DD name: LINKLIB would be an override of the
	// procedures', and AOST4 would then need procedure (tested on its own)
	smp["target"] = "ABC.ABCLINK"
	for k, v := range smpExtra {
		smp[k] = v
	}
	if mods == nil {
		mods = []Module{{Name: "ABCMOD"}, systemModule()}
	}
	err = Build(cfg, "abc", "1.0.0", mods, Options{
		Root: root, DistDir: "dist", BuildDir: "build",
		Log: func(string) {}, Mtime: time.Unix(0, 0)})
	if err != nil {
		return "", "", err
	}
	stage := filepath.Join(root, "build", "dist-stage")
	b, _ := os.ReadFile(filepath.Join(stage, "abc-1.0.0-inst.jcl"))
	a, _ := os.ReadFile(filepath.Join(stage, "abc-1.0.0-accept.jcl"))
	return string(b), string(a), nil
}

func TestSystemModuleInstallJob(t *testing.T) {
	inst, acc, err := buildSystem(t, asmSource+asmCard80+"\n         END   IGX00099\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	checkJobText(t, "install job", inst, []string{
		// the JCLIN: COPYLOAD keeps the load module, IEWL links the system one
		"  COPY INDD=AABCLOD,OUTDD=ABCLINK\n  SELECT MEMBER=(ABCMOD)\n/*\n" +
			"//IGX00099 EXEC PGM=IEWL,PARM='MAP,LIST,NCAL,RENT,REUS,REFR'\n" +
			"//AOST4    DD  DISP=SHR,DSN=SYS1.AOST4\n" +
			"//SYSLMOD  DD  DISP=SHR,DSN=SYS1.LPALIB\n" +
			"//SYSLIN   DD  *\n" +
			"  INCLUDE AOST4(IGX00099)\n" +
			"  NAME IGX00099(R)\n/*\n" +
			"++MOD(ABCMOD) LKLIB(LKLIB) DISTLIB(AABCLOD) .\n" +
			// its ++MOD is the last card: the object deck goes right behind it
			"++MOD(IGX00099) DISTLIB(AOST4) .\n@@\n",
		"   /* Load modules are copied from the LKLIB, not re-bound.      */\n" +
			"   /* IGX00099 is assembled at install, linked into LPALIB.      */ .\n",
		"//GENER1  EXEC PGM=IEBGENER,COND=(0,NE,RECV1)\n//SYSPRINT DD  SYSOUT=*\n//SYSIN    DD  DUMMY\n//SYSUT1   DD  DATA,DLM=@@\n++FUNCTION(TABC100) .\n",
		"//SYSUT2   DD  DSN=&&MCS,DISP=(NEW,PASS),UNIT=SYSDA,\n",
		"//ASSEM   EXEC PGM=IFOX00,PARM='LOAD,NODECK',\n//             COND=((0,NE,RECV1),(0,NE,GENER1))\n",
		"//SYSLIB   DD  DISP=SHR,DSN=SYS1.MACLIB,DCB=BLKSIZE=32720\n//         DD  DISP=SHR,DSN=SYS1.AMODGEN\n",
		"//SYSGO    DD  DSN=&&MCS,DISP=(MOD,PASS)\n//SYSIN    DD  DATA,DLM=@@\nIGX00099 CSECT\n",
		"\n" + asmCard80 + "\n         END   IGX00099\n@@\n",
		"//RECV    EXEC SMPREC,COND=((0,NE,RECV1),(0,NE,GENER1),(0,NE,ASSEM))\n//SMPPTFIN DD  DSN=&&MCS,DISP=(OLD,PASS)\n//SMPCNTL  DD  *\n",
		"//*   GENER1    write the SYSMOD to a temporary data set\n//*   ASSEM     assemble IGX00099 behind its ++MOD (IFOX00)\n//*   RECV ",
		"SYS1.LPALIB", "CLPA",
		// AOST4 is not in TK5's SMPAPP: a card of its own, ahead of the product's
		"//APPLYCHK EXEC SMPAPP,COND=((0,NE,RECV1),(0,NE,GENER1),(0,NE,ASSEM),\n//             (0,NE,RECV.HMASMP))\n//HMASMP.AOST4    DD  DISP=SHR,DSN=SYS1.AOST4\n//HMASMP.ABCLINK ",
	}, []string{"//SMPPTFIN DD  DATA", "HMASMP.LPALIB", "SELECT MEMBER=(ABCMOD,IGX00099)"})
	checkJobText(t, "accept job", acc, []string{
		"//ACCEPT  EXEC SMPAPP,COND=(0,NE,GUARD.HMASMP)\n//HMASMP.AOST4    DD  DISP=SHR,DSN=SYS1.AOST4\n",
		// the ACCEPT ends 4 with a system module (HMA2471), the CLEANUP still runs
		"//             COND=((0,NE,GUARD.HMASMP),(4,LT,ACCEPT.HMASMP))\n",
		"HMA2471", "HMA2391",
	}, nil)
}

// With procedure named, the card is there only where the procedure lacks the
// library: MVS/CE's SMPAPP has AOST4, TK5's does not.
func TestSystemModuleDDsFollowTheProcedure(t *testing.T) {
	inst, _, err := buildSystem(t, asmSource, map[string]any{"procedure": "mvsce"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(inst, "HMASMP.AOST4") {
		t.Errorf("MVS/CE has AOST4 in SMPAPP, no card needed:\n%s", inst)
	}
	inst, _, err = buildSystem(t, asmSource, map[string]any{"procedure": "tk5"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inst, "//HMASMP.AOST4    DD  DISP=SHR,DSN=SYS1.AOST4\n") {
		t.Errorf("TK5 has no AOST4 in SMPAPP, the card is needed:\n%s", inst)
	}
}

// Without a procedure, a product library that overrides a procedure DD
// (LINKLIB here) puts AOST4 in an order that differs between the systems:
// an override ahead of LINKLIB on MVS/CE, an addition behind it on TK5.
func TestSystemModuleOrderMustNameTheProcedure(t *testing.T) {
	_, _, err := buildSystem(t, asmSource, map[string]any{"target": "ABC.LINKLIB"})
	if err == nil || !strings.Contains(err.Error(), "Set `procedure`") {
		t.Errorf("want the procedure error, got %v", err)
	}
}

func TestSystemModuleRejects(t *testing.T) {
	sys := func(f func(*SystemModule)) Module {
		m := systemModule()
		s := *m.System
		f(&s)
		m.System = &s
		return m
	}
	for name, c := range map[string]struct {
		source string
		mods   []Module
		want   string
	}{
		"syslib":     {asmSource, []Module{{Name: "ABCMOD"}, sys(func(s *SystemModule) { s.Syslib = "SAMPLIB" })}, "syslib = 'SAMPLIB'"},
		"distlib":    {asmSource, []Module{{Name: "ABCMOD"}, sys(func(s *SystemModule) { s.Distlib = "AMACLIB" })}, "distlib = 'AMACLIB'"},
		"no load":    {asmSource, []Module{systemModule()}, "at least one has to go into the product's target"},
		"two":        {asmSource, []Module{{Name: "ABCMOD"}, systemModule(), func() Module { m := systemModule(); m.Name = "IGX00098"; return m }()}, "already shipped as source"},
		"alias":      {asmSource, []Module{{Name: "ABCMOD"}, func() Module { m := systemModule(); m.Aliases = []string{"X"}; return m }()}, "aliases"},
		"tab":        {"IGX00099 CSECT\n\tBR 14\n", nil, "a tab"},
		"delimiter":  {asmSource + "@@ NOT A CARD\n", nil, "ends the DD DATA stream"},
		"81 columns": {asmCard80 + "1\n", nil, "exceeds the 80"},
		"latin1":     {"* café\n" + asmSource, nil, "outside plain ASCII"},
		"empty":      {"", nil, "is empty"},
	} {
		_, _, err := buildSystem(t, c.source, nil, c.mods...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", name, c.want, err)
		}
	}
}

// Without a system module the RECV is what it always was: the SYSMOD inline,
// byte for byte what mbt 2 writes (smpinst.jcl.tpl had it as fixed text).
func TestNoSystemModuleKeepsTheInlineSysmod(t *testing.T) {
	d := &Distribution{}
	got := receiveSysmod(d, "++FUNCTION(TABC100) .\n++VER(Z038) .\n", "RECV3", nil)
	want := "//RECV    EXEC SMPREC,COND=(0,NE,RECV3)\n//SMPPTFIN DD  DATA,DLM=@@\n++FUNCTION(TABC100) .\n++VER(Z038) .\n@@"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	inst, _, err := buildSystem(t, asmSource, nil, Module{Name: "ABCMOD"})
	if err != nil {
		t.Fatal(err)
	}
	checkJobText(t, "install job", inst, []string{"//RECV    EXEC SMPREC,COND=(0,NE,RECV1)\n//SMPPTFIN DD  DATA,DLM=@@\n++FUNCTION(TABC100) .\n"},
		[]string{"GENER1", "ASSEM", "&&MCS", "AOST4", "LPALIB", "HMA2471"})
}
