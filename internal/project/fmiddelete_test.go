package project

import (
	"strings"
	"testing"
)

// An explicit fmid says nothing about its predecessor, and an empty delete
// is right only for a product's first level: anywhere else the new SYSMOD
// does not own the modules and SMP installs nothing at RC 0 (NOT SEL). So
// the file must say which it is.
func TestExplicitFMIDNeedsDelete(t *testing.T) {
	explicit := func(del string) string {
		return strings.Replace(strings.Replace(v3Base, "1.4.0-dev", "1.4.1-dev", 1), `prefix = "TUFS"`, `fmid = "TUFS141"`+del, 1)
	}
	if _, err := LoadV3(v3Tree(t, explicit("")), FileV3); err == nil || !strings.Contains(err.Error(), "delete = []") {
		t.Errorf("fmid without delete: %v", err)
	}
	if _, err := LoadV3(v3Tree(t, explicit("\ndelete = []")), FileV3); err != nil {
		t.Errorf("first level, delete = []: %v", err)
	}
	if _, err := LoadV3(v3Tree(t, explicit("\ndelete = [\"TUFS140\"]")), FileV3); err != nil {
		t.Errorf("patch with delete: %v", err)
	}
}
