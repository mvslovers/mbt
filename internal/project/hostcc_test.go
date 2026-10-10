package project

import "testing"

// [build.host] cc names the host compiler of the native test build; the
// host-test code reads it, so the schema must accept it.
func TestBuildHostCC(t *testing.T) {
	root := v3Tree(t, v3Base+"\n[build.host]\ncc = \"gcc-14\"\n")
	p, err := LoadV3(root, FileV3)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := p.Raw["host"].(map[string]any)
	if cc, _ := host["cc"].(string); cc != "gcc-14" {
		t.Errorf("host table %v", p.Raw["host"])
	}
}
