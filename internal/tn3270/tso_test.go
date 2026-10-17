package tn3270

import "testing"

// isISPFPrimary drives the new logon/logoff ISPF handling (mbt#272). The
// state machine itself is exercised live against a stand whose logon proc
// starts ISPF; this covers the pure screen test offline.
func TestIsISPFPrimary(t *testing.T) {
	yes := []string{
		"              ISPF/PDF PRIMARY OPTION MENU\n Option ===>\n",
		"  Menu  Utilities  Compilers  Options\n          ISPF Primary Option Menu\n Option  ===>              \n 0  Settings\n",
		"option   ===> =x\nwelcome to ispf\n", // collapsed spacing, lower case
	}
	no := []string{
		"READY\n",
		"IKJ56700A ENTER USERID -\n",
		"***\n",
		" Option ===> \n",           // an Option line but no ISPF anywhere
		"ISPF\n Command ===> 3.4\n", // ISPF, but a Command line, not the primary Option menu
		"",
	}
	for _, s := range yes {
		if !isISPFPrimary(s) {
			t.Errorf("isISPFPrimary = false, want true:\n%q", s)
		}
	}
	for _, s := range no {
		if isISPFPrimary(s) {
			t.Errorf("isISPFPrimary = true, want false:\n%q", s)
		}
	}
}
