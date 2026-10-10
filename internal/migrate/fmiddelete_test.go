package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An mbt 2 first level carried fmid and no delete (mvsMF 1.1.0). Kept
// explicit, the mbt.toml needs delete = [] to say there is no predecessor.
func TestExplicitFMIDGetsEmptyDelete(t *testing.T) {
	root := tree(t)
	v2 := strings.Replace(v2File, "fmid = \"TUFS140\"\ndelete = [\"TUFS130\"]   # the level this one replaces\n", "fmid = \"TUFS100\"\n", 1)
	if v2 == v2File {
		t.Fatal("fixture did not change")
	}
	os.WriteFile(filepath.Join(root, "project.toml"), []byte(v2), 0o644)
	res, err := Convert(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "fmid = \"TUFS100\"\ndelete = []") {
		t.Errorf("no delete = [] after the explicit fmid:\n%s", res.Text)
	}
	if diffs, err := Check(root, res.Text); err != nil || len(diffs) > 0 {
		t.Errorf("check: %v %v", err, diffs)
	}
}
