package mvstest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mbt#243: the fixtures of a TSO test, loaded by a job of their own; the
// lines go over as they are, columns 73-80 included.
func TestBuildFixturesAndJob(t *testing.T) {
	root := t.TempDir()
	seq := strings.Repeat("X", 72) + "SEQ00010" // 80 columns, code in 73-80
	os.MkdirAll(filepath.Join(root, "rxlib"), 0o755)
	os.WriteFile(filepath.Join(root, "rxlib", "fssapi.rexx"), []byte("/* REXX */\n"+seq+"\n"), 0o644)
	os.WriteFile(filepath.Join(root, "t.rexx"), []byte("say 'hi'\n"), 0o644)
	fx, err := BuildFixtures(root, "IBMUSER", "BREXX370", "FSSMENU",
		[]FixtureDecl{{DD: "RXLIB", Members: []string{"rxlib/fssapi.rexx"}}, {DD: "TSOTEST", Members: []string{"t.rexx"}}})
	if err != nil {
		t.Fatal(err)
	}
	if fx[0].PDS != "IBMUSER.BREXX370.FIX.FSSMENU.RXLIB" || fx[0].Members[0][0] != "FSSAPI" || fx[1].PDS != "IBMUSER.BREXX370.FIX.FSSMENU.TSOTEST" {
		t.Errorf("fixtures %+v", fx)
	}
	jcl, steps, order := GenFixtureJob("//MBTTSOFX JOB (1),'X',CLASS=A", []string{"FSSMENU"}, map[string][]Fixture{"FSSMENU": fx})
	if len(steps) != 2 || len(order) != 2 || steps["FX001"].PDS != fx[0].PDS || steps["FX002"].Member != "T" {
		t.Errorf("steps %v %v", steps, order)
	}
	if !strings.Contains(jcl, "\n"+seq+"\n") || !strings.Contains(jcl, "SYSUT2   DD DSN=IBMUSER.BREXX370.FIX.FSSMENU.RXLIB(FSSAPI),DISP=SHR") {
		t.Errorf("jcl:\n%s", jcl)
	}
	if strings.Contains(jcl, "PGM=IKJEFT01") || strings.Contains(jcl, "STEPLIB") {
		t.Errorf("a fixture job runs no test:\n%s", jcl)
	}

	// a line past column 80 would be cut by the load: refused, naming it
	os.WriteFile(filepath.Join(root, "long.rexx"), []byte("ok\n"+strings.Repeat("Y", 81)+"\n"), 0o644)
	_, err = BuildFixtures(root, "IBMUSER", "BREXX370", "FSSMENU", []FixtureDecl{{DD: "RXLIB", Members: []string{"long.rexx"}}})
	if err == nil || !strings.Contains(err.Error(), "long.rexx line 2 has 81 columns") {
		t.Errorf("long line: %v", err)
	}
	// one key twice
	_, err = BuildFixtures(root, "IBMUSER", "BREXX370", "FSSMENU", []FixtureDecl{{DD: "RXLIB", Members: []string{"t.rexx"}}, {DD: "rxlib", Members: []string{"t.rexx"}}})
	if err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("twice: %v", err)
	}
}
