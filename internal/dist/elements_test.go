package dist

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func elemCfg(libs ...map[string]any) map[string]any {
	var l []any
	for _, x := range libs {
		l = append(l, x)
	}
	return map[string]any{"distribution": map[string]any{
		"smp": map[string]any{"fmid": "TABC100", "system": "Z038", "lklib": "ABC.LKLIB",
			"target": "ABC.LINKLIB", "distlib": "ABC.AABCLOD"},
		"library": l,
	}}
}

func parseElem(t *testing.T, libs ...map[string]any) *Distribution {
	t.Helper()
	d, err := Parse(elemCfg(libs...), "V1R0M0")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func withElements(d *Distribution, dir string, names ...string) {
	for i := range d.Libraries {
		if d.Libraries[i].Dir == dir {
			d.Libraries[i].Elements = sortedElements(append([]string(nil), names...))
		}
	}
	assignDDs(d)
}

func TestElementDefaults(t *testing.T) {
	d := parseElem(t, map[string]any{"dir": "rxlib", "target": "ABC.RXLIB", "element": "mac"})
	l := d.Libraries[0]
	if l.Element != "MAC" || l.DistLib != "ABC.ARXLIB" || l.Stage != "ABC.TRXLIB" {
		t.Errorf("defaults: %+v", l)
	}
	if got := d.received(); got[1] != "ABC.TRXLIB" {
		t.Errorf("a delivered library is received into its stage, got %v", got)
	}
	d = parseElem(t, map[string]any{"dir": "rxlib", "target": "ABC.RXLIB", "element": "SRC", "distlib": "abc.rxdlib", "stage": "abc.rxstage"})
	if l := d.Libraries[0]; l.DistLib != "ABC.RXDLIB" || l.Stage != "ABC.RXSTAGE" {
		t.Errorf("explicit: %+v", l)
	}
}

func TestElementRejects(t *testing.T) {
	lib := func(m map[string]any) map[string]any {
		out := map[string]any{"dir": "rxlib", "target": "ABC.RXLIB", "element": "MAC"}
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	for name, l := range map[string]map[string]any{
		"unknown element":    lib(map[string]any{"element": "MOD"}),
		"stage w/o element":  {"dir": "x", "target": "ABC.X", "stage": "ABC.TX"},
		"distlib w/o elem":   {"dir": "x", "target": "ABC.X", "distlib": "ABC.AX"},
		"default too long":   lib(map[string]any{"target": "ABC.SAMPLIB1"}),
		"distlib = target":   lib(map[string]any{"distlib": "ABC.RXLIB"}),
		"stage = distlib":    lib(map[string]any{"stage": "ABC.ARXLIB"}),
		"distlib = smp dist": lib(map[string]any{"distlib": "ABC.AABCLOD"}),
		"lrecl key":          lib(map[string]any{"lrecl": 255}),
		"recfm key":          lib(map[string]any{"recfm": "VB"}),
	} {
		if _, err := Parse(elemCfg(l), "V1R0M0"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestElementStatementsAndDDs(t *testing.T) {
	d := parseElem(t,
		map[string]any{"dir": "rxlib", "target": "ABC.RXLIB", "element": "MAC"},
		map[string]any{"dir": "samples", "target": "ABC.SAMPLES", "element": "SRC"},
		map[string]any{"dir": "plain", "target": "ABC.PLAIN"})
	withElements(d, "rxlib", "ALPHA", "SMPLOG", "RXLIB")
	withElements(d, "samples", "ALPHA", "ZED")

	mcs, err := AssembleMCS(d, []string{"ABCMOD"}, "abc", "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"++MAC(ALPHA) DISTLIB(ARXLIB) SYSLIB(RXLIB) TXLIB(ALPHA) .",
		"++MAC(RXLIB) DISTLIB(ARXLIB) SYSLIB(RXLIB) TXLIB(TX000001) .",
		"++MAC(SMPLOG) DISTLIB(ARXLIB) SYSLIB(RXLIB) TXLIB(TX000002) .",
		"++SRC(ALPHA) DISTLIB(ASAMPLES) SYSLIB(SAMPLES) TXLIB(TX000003) .",
		"++SRC(ZED) DISTLIB(ASAMPLES) SYSLIB(SAMPLES) TXLIB(ZED) .",
	} {
		if !strings.Contains(mcs, want+"\n") {
			t.Errorf("MCS lacks %q:\n%s", want, mcs)
		}
	}
	if strings.Index(mcs, "++MAC(ALPHA)") > strings.Index(mcs, "++JCLIN") {
		t.Errorf("elements must come before the JCLIN, whose data runs to the next ++")
	}

	apply := renderApplyDDs(d)
	lines := strings.Split(apply, "\n")
	for _, want := range []string{
		"//HMASMP.RXLIB    DD  DISP=SHR,DSN=ABC.RXLIB",
		"//HMASMP.ASAMPLES DD  DISP=SHR,DSN=ABC.ASAMPLES",
		"//HMASMP.ALPHA    DD  DISP=SHR,DSN=ABC.TRXLIB(ALPHA)",
		"//HMASMP.TX000003 DD  DISP=SHR,DSN=ABC.TSAMPLES(ALPHA)",
	} {
		if !strings.Contains(apply, want+"\n") && !strings.HasSuffix(apply, want) {
			t.Errorf("APPLY DDs lack %q:\n%s", want, apply)
		}
	}
	if len(lines) != 3+2*2+5 {
		t.Errorf("want 3 SMP + 4 element-library + 5 TXLIB cards, got %d:\n%s", len(lines), apply)
	}
	// a library that overrides a procedure DD still goes before the additions
	d2 := parseElem(t, map[string]any{"dir": "m", "target": "ABC.SAMPLIB", "element": "SRC", "distlib": "ABC.ASAMPLIB"})
	withElements(d2, "m", "ONE")
	got := strings.Split(renderApplyDDs(d2), "\n")
	if !strings.HasPrefix(got[0], "//HMASMP.LINKLIB") || !strings.HasPrefix(got[1], "//HMASMP.SAMPLIB") || !strings.HasPrefix(got[2], "//HMASMP.ASAMPLIB") {
		t.Errorf("overrides first:\n%s", strings.Join(got, "\n"))
	}
	// the RECEIVE step reads the same members, without a step qualifier
	recv := renderRecvDDs(d)
	if !strings.HasPrefix(recv, "\n//ALPHA    DD  DISP=SHR,DSN=ABC.TRXLIB(ALPHA)") || strings.Contains(recv, "HMASMP") {
		t.Errorf("RECEIVE DDs:\n%s", recv)
	}
}

func TestLongTXCardContinues(t *testing.T) {
	card := txCard("HMASMP.", "ABCDEFGH", "VERYLONGHLQ.WITHMANY.QUALIFIERS.TSAMPLES", "MEMBER")
	for _, l := range strings.Split(card, "\n") {
		if len(l) > maxCardCol {
			t.Errorf("%d columns: %s", len(l), l)
		}
	}
	if !strings.Contains(card, "\n//             DSN=") {
		t.Errorf("no continuation:\n%s", card)
	}
}

func TestElementAlloc(t *testing.T) {
	d := parseElem(t, map[string]any{"dir": "rxlib", "target": "ABC.RXLIB", "element": "MAC"})
	withElements(d, "rxlib", "A", "B")
	d.Libraries[0].records = 1000
	got := renderAllocDDs(d)
	for _, want := range []string{
		"//RXLIB    DD  DSN=ABC.RXLIB,DISP=(NEW,CATLG,DELETE),\n//            UNIT=SYSDA,SPACE=(CYL,(5,2,20)),\n//            DCB=(DSORG=PO,RECFM=FB,LRECL=80,BLKSIZE=3120)",
		"//ARXLIB   DD  DSN=ABC.ARXLIB,DISP=(NEW,CATLG,DELETE),\n//            UNIT=SYSDA,SPACE=(CYL,(5,2,20)),\n//            DCB=(DSORG=PO,RECFM=FB,LRECL=80,BLKSIZE=3120)",
		"//LINKLIB  DD  DSN=ABC.LINKLIB,DISP=(NEW,CATLG,DELETE),\n//            UNIT=SYSDA,SPACE=(CYL,(5,2,20)),\n//            DCB=(DSORG=PO,RECFM=U,BLKSIZE=15040)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("alloc lacks:\n%s\nin:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ABC.TRXLIB") {
		t.Errorf("the staging library is received, not allocated:\n%s", got)
	}
	l := d.Libraries[0]
	l.records = 400000 * 8 / 80 // 8 cylinders of card images -> twice that
	l.Elements = make([]Element, 300)
	if p, s, dir := l.elementSpace(); p != 16 || s != 8 || dir != 110 {
		t.Errorf("space %d,%d,%d", p, s, dir)
	}
}

func TestElementCleanupAndCond(t *testing.T) {
	d := parseElem(t, map[string]any{"dir": "m", "target": "ABC.MACS", "element": "MAC"})
	if acceptCond(d) != "(0,NE,APPLY.HMASMP)" {
		t.Errorf("MAC applies at RC 0: %s", acceptCond(d))
	}
	c, err := renderCleanupStep(d, "ACCEPT.HMASMP")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c, "  DELETE ABC.LKLIB NONVSAM SCRATCH PURGE\n  DELETE ABC.TMACS NONVSAM SCRATCH PURGE\n") {
		t.Errorf("stage not deleted:\n%s", c)
	}
	if strings.Contains(c, "ABC.MACS ") || strings.Contains(c, "ABC.AMACS") {
		t.Errorf("target or distribution library deleted:\n%s", c)
	}
	d = parseElem(t, map[string]any{"dir": "s", "target": "ABC.SRCS", "element": "SRC"})
	if acceptCond(d) != "(4,LT,APPLY.HMASMP)" {
		t.Errorf("++SRC leaves the APPLY at RC 4 (HMA4441): %s", acceptCond(d))
	}
}

func TestElementNameChecks(t *testing.T) {
	d := parseElem(t,
		map[string]any{"dir": "a", "target": "ABC.MACA", "element": "MAC"},
		map[string]any{"dir": "b", "target": "ABC.MACB", "element": "MAC"},
		map[string]any{"dir": "c", "target": "ABC.SRCC", "element": "SRC"})
	withElements(d, "a", "ONE", "TWO")
	withElements(d, "c", "ONE")
	if err := checkElementNames(d); err != nil {
		t.Errorf("a MAC and an SRC may share a name: %v", err)
	}
	withElements(d, "b", "TWO")
	err := checkElementNames(d)
	if err == nil || !strings.Contains(err.Error(), "MAC TWO") {
		t.Errorf("two MAC TWO accepted: %v", err)
	}
}

func TestElementReportSRC(t *testing.T) {
	d := parseElem(t, map[string]any{"dir": "s", "target": "ABC.SAMP", "element": "SRC"})
	withElements(d, "s", "LISTALC", "XMAS")
	rep := strings.Join(ElementReport(d), "\n")
	for _, want := range []string{"2 SRC elements for ABC.SAMP", "LIST CDS MOD(LISTALC,XMAS) .", "LIST CDS SRC(LISTALC,XMAS) .", "assemble"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
}

func TestElementReport(t *testing.T) {
	d := parseElem(t, map[string]any{"dir": "a", "target": "ABC.MACA", "element": "MAC"})
	names := make([]string, 40)
	for i := range names {
		names[i] = fmt.Sprintf("MEMBER%02d", i)
	}
	withElements(d, "a", names...)
	rep := strings.Join(ElementReport(d), "\n")
	if !strings.Contains(rep, "40 MAC elements for ABC.MACA") || !strings.Contains(rep, "NOT SEL") ||
		!strings.Contains(rep, "LIST CDS MAC(MEMBER00,") || !strings.HasSuffix(rep, "MEMBER39) .") {
		t.Errorf("report:\n%s", rep)
	}
	for _, l := range strings.Split(rep, "\n") {
		if strings.HasPrefix(l, "    ") && len(l) > 80 {
			t.Errorf("long line: %s", l)
		}
	}
}

// ---- the member check -------------------------------------------------------

func TestCheckMemberAccepts80ColumnsWithSequenceNumbers(t *testing.T) {
	line := strings.Repeat("A", 72) + "00000010"
	text := line + "\n" + strings.Repeat(" ", 71) + "X" + "00000020\n"
	out, latin1, problems := checkMember("m", text)
	if len(problems) != 0 || latin1 || out != text {
		t.Errorf("clean 80-column member: problems %v latin1 %v\n%q", problems, latin1, out)
	}
}

func TestCheckMemberRejects(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"81 columns":        {"ok\n" + strings.Repeat("B", 81) + "\n", "m.mac:2:81: line is 81 columns"},
		"tab past 80":       {"\t\t\t\t\t\t\t\t\t\tX\n", "m.mac:1:81: line is 81 columns"},
		"em dash":           {"a -- b\nx \u2014 y\n", "m.mac:2:3: '\u2014' (U+2014) has no CP037 equivalent"},
		"byte order mark":   {"\ufeffhello\n", "m.mac:1:1:"},
		"control character": {"a\x01b\n", "m.mac:1:2: control character U+0001"},
		"C1 control":        {"a\u0085b\n", "m.mac:1:2: control character U+0085"},
		"cjk":               {"\u65e5\u672c\n", "U+65E5"},
	} {
		_, _, problems := checkMember("m.mac", tc.text)
		if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), tc.want) {
			t.Errorf("%s: want %q, got %v", name, tc.want, problems)
		}
	}
}

func TestCheckMemberWidthLikeXmit370(t *testing.T) {
	// xmit370 expands tabs and drops trailing blanks before it measures
	for name, text := range map[string]string{
		"80 chars then blanks":    strings.Repeat("C", 80) + strings.Repeat(" ", 20) + "\n",
		"tab to column 80":        strings.Repeat("D", 72) + "\tE\n"[:1] + "\n",
		"trailing tab":            strings.Repeat("F", 79) + "\t\n",
		"crlf free, no last \\n":  strings.Repeat("G", 80),
		"accented, valid UTF-8":   "caf\u00e9 " + strings.Repeat("H", 70) + "\n",
		"accented, raw Latin-1":   "caf\xe9\n",
		"blank line and one char": "\n\nX\n",
	} {
		if _, _, problems := checkMember("m", text); len(problems) != 0 {
			t.Errorf("%s: %v", name, problems)
		}
	}
}

func TestCheckMemberLatin1(t *testing.T) {
	out, latin1, problems := checkMember("m", "caf\u00e9 \u00a3 \u00ac\n")
	if len(problems) != 0 || !latin1 || out != "caf\xe9 \xa3 \xac\n" {
		t.Errorf("UTF-8 to Latin-1: %v %v %q", problems, latin1, out)
	}
	out, latin1, problems = checkMember("m", "caf\xe9\n")
	if len(problems) != 0 || !latin1 || out != "caf\xe9\n" {
		t.Errorf("Latin-1 stays: %v %v %q", problems, latin1, out)
	}
	if _, latin1, _ := checkMember("m", "plain ascii\n"); latin1 {
		t.Error("--latin1 for plain ASCII")
	}
}

// ---- staging and the XMIT ---------------------------------------------------

func writeLib(t *testing.T, root, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(root, dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStageLibraryNamesTheFileAndLine(t *testing.T) {
	root := t.TempDir()
	writeLib(t, root, "lib", map[string]string{
		"good.mac": "ok\n",
		"long.mac": "ok\n" + strings.Repeat("x", 90) + "\n",
		"dash.mac": "a \u2013 b\n",
	})
	l := &Library{Dir: "lib", Target: "ABC.LIB", Element: "MAC", DistLib: "ABC.ALIB", Stage: "ABC.TLIB"}
	_, err := stageLibrary(root, l, nil, filepath.Join(root, "stage"))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"lib/long.mac:2:81: line is 90 columns", "lib/dash.mac:1:3: '\u2013' (U+2013) has no CP037 equivalent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "good.mac") {
		t.Errorf("a good member is named:\n%v", err)
	}
}

func TestStageLibraryDuplicateAndEmptyMembers(t *testing.T) {
	root := t.TempDir()
	writeLib(t, root, "dup", map[string]string{"one.mac": "x\n", "one.txt": "y\n"})
	l := &Library{Dir: "dup", Target: "ABC.DUP", Element: "MAC", DistLib: "ABC.ADUP", Stage: "ABC.TDUP"}
	if _, err := stageLibrary(root, l, nil, filepath.Join(root, "s")); err == nil || !strings.Contains(err.Error(), "member ONE") {
		t.Errorf("duplicate member: %v", err)
	}
	writeLib(t, root, "empty", map[string]string{"blank.mac": "  \n\n"})
	l = &Library{Dir: "empty", Target: "ABC.EMPTY", Element: "MAC", DistLib: "ABC.AEMPTY", Stage: "ABC.TEMPTY"}
	if _, err := stageLibrary(root, l, nil, filepath.Join(root, "s")); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty member: %v", err)
	}
}

func TestStageLibraryLeavesColumnsAlone(t *testing.T) {
	root := t.TempDir()
	member := strings.Repeat("M", 72) + "00000100\n" + "@VRM@ stays\n"
	writeLib(t, root, "lib", map[string]string{"seq.mac": member})
	l := &Library{Dir: "lib", Target: "ABC.LIB", Element: "MAC", DistLib: "ABC.ALIB", Stage: "ABC.TLIB"}
	dest, err := stageLibrary(root, l, [][2]string{{"@VRM@", "V1R0M0"}}, filepath.Join(root, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "seq.mac"))
	if string(got) != member {
		t.Errorf("the member changed:\n%q", got)
	}
	if len(l.Elements) != 1 || l.Elements[0].Name != "SEQ" || l.records != 2 || l.needsLatin1 {
		t.Errorf("%+v records %d", l.Elements, l.records)
	}
}

// Latin-1 characters have to come out as the CP037 byte, not as the two bytes
// of their UTF-8 spelling: run the real xmit370 on what stageLibrary produced.
func TestLatin1ThroughXmit370(t *testing.T) {
	if _, err := exec.LookPath("xmit370"); err != nil {
		t.Skip("xmit370 not on PATH")
	}
	root := t.TempDir()
	writeLib(t, root, "lib", map[string]string{"cafe.mac": "caf\u00e9 \u00a3 \u00ac [\u00a2]\n"})
	l := &Library{Dir: "lib", Target: "ABC.LIB", Element: "MAC", DistLib: "ABC.ALIB", Stage: "ABC.TLIB"}
	dest, err := stageLibrary(root, l, nil, filepath.Join(root, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	if !l.needsLatin1 {
		t.Fatal("not marked for --latin1")
	}
	out := filepath.Join(root, "lib.xmit")
	if res, err := exec.Command("xmit370", "create", "-o", out, "--dsn", "ABC.TLIB", "--latin1", dest).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, res)
	}
	data, _ := os.ReadFile(out)
	// c a f e-acute, blank, pound, blank, not, blank, [ cent ]  in CP037
	want := []byte{0x83, 0x81, 0x86, 0x51, 0x40, 0xB1, 0x40, 0x5F, 0x40, 0xBA, 0x4A, 0xBB}
	if !bytes.Contains(data, want) {
		t.Errorf("CP037 bytes % X not in the XMIT", want)
	}
	if bytes.Contains(data, []byte{0xC3, 0xA9}) {
		t.Error("the UTF-8 spelling reached the XMIT")
	}
}

func TestBuildEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("xmit370"); err != nil {
		t.Skip("xmit370 not on PATH")
	}
	root := t.TempDir()
	writeLib(t, root, "rxlib", map[string]string{"alpha.rexx": "/* a */\n", "beta.rexx": strings.Repeat("b", 72) + "00000010\n"})
	writeLib(t, root, "samples", map[string]string{"gamma.rexx": "say 'x'\n"})
	dist := filepath.Join(root, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, "abc-1.0.0-load.xmit"), []byte("x"), 0o644)
	cfg := elemCfg(
		map[string]any{"dir": "rxlib", "target": "ABC.RXLIB", "element": "MAC"},
		map[string]any{"dir": "samples", "target": "ABC.SAMPLES", "element": "SRC"})
	var logs []string
	err := Build(cfg, "abc", "1.0.0", []Module{{Name: "ABCMOD"}}, Options{
		Root: root, DistDir: "dist", BuildDir: "build",
		Log: func(s string) { logs = append(logs, s) }, Mtime: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	inst, _ := os.ReadFile(filepath.Join(root, "build", "dist-stage", "abc-1.0.0-inst.jcl"))
	alloc, _ := os.ReadFile(filepath.Join(root, "build", "dist-stage", "abc-1.0.0-alloc.jcl"))
	text := string(inst)
	for _, want := range []string{
		"++MAC(ALPHA) DISTLIB(ARXLIB) SYSLIB(RXLIB) TXLIB(ALPHA) .",
		"++SRC(GAMMA) DISTLIB(ASAMPLES) SYSLIB(SAMPLES) TXLIB(GAMMA) .",
		"RECEIVE INDSN('CHANGE.ME.TRXLIB')",
		"DATASET('ABC.TRXLIB')",
		"//ALPHA    DD  DISP=SHR,DSN=ABC.TRXLIB(ALPHA)\n//BETA ",
		"//APPLY   EXEC SMPAPP,COND=(4,LT,APPLYCHK.HMASMP)",
		"//ACCEPT  EXEC SMPAPP,COND=(4,LT,APPLY.HMASMP)",
		"  DELETE ABC.TSAMPLES NONVSAM SCRATCH PURGE",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("install job lacks %q", want)
		}
	}
	if n := strings.Count(text, "DSN=ABC.TRXLIB(ALPHA)"); n != 4 {
		t.Errorf("ALPHA is read from its staging member in RECEIVE, APPLY CHECK, APPLY and ACCEPT, found %d cards", n)
	}
	if !strings.Contains(string(alloc), "RECFM=FB,LRECL=80,BLKSIZE=3120") {
		t.Errorf("alloc job:\n%s", alloc)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "LIST CDS MAC(ALPHA,BETA) .") {
		t.Errorf("no name list in the log:\n%s", strings.Join(logs, "\n"))
	}

	writeLib(t, root, "rxlib", map[string]string{"bad.rexx": strings.Repeat("z", 81) + "\n"})
	err = Build(cfg, "abc", "1.0.0", []Module{{Name: "ABCMOD"}}, Options{Root: root, DistDir: "dist", BuildDir: "build", Log: func(string) {}})
	if err == nil || !strings.Contains(err.Error(), "rxlib/bad.rexx:1:81") {
		t.Errorf("an 81-column line must stop the build: %v", err)
	}
}
