package build

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// CheckStaged refuses to compile while a declared dependency is not staged in
// .mbt/deps: its headers are missing, and the compiler would bury that under
// one "No such file or directory" per file. mbt deps stages it (from the SHA
// cache when the pinned archive is there).
func CheckStaged(root string, raw map[string]any) error {
	decl, _ := raw["dependencies"].(map[string]any)
	var missing []string
	for key := range decl {
		repo := path.Base(key)
		if st, err := os.Stat(filepath.Join(root, ".mbt", "deps", repo)); err != nil || !st.IsDir() {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return &Error{Code: 3, Msg: fmt.Sprintf("dependencies not staged: %s -- run 'mbt deps'", strings.Join(missing, ", "))}
}
