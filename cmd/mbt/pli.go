package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mvslovers/mbt/internal/project"
)

// pl1Source is the first PL/I source the build compiles -- of a module, a
// test, the [lib] archive or [internal] -- "" if none.
func pl1Source(p *project.Project) string {
	var all [][]string
	for _, u := range p.Units() {
		all = append(all, u.Sources)
	}
	if p.Lib != nil {
		all = append(all, p.Lib.Sources)
	}
	all = append(all, p.Internal)
	for _, l := range all {
		for _, s := range l {
			if project.IsPLI(s) {
				return s
			}
		}
	}
	return ""
}

// findPL1C resolves pl1c370 for a project with PL/I sources. Unlike cobc370
// (third-party, fetched via a [tools] entry), pl1c370 is our own compiler and
// lives in the sysroot beside cc370, placed there by pl1c370's `make install`.
// Order: MBT_PL1C370 (a path or a name on PATH), the sysroot's bin, pl1c370 on
// PATH. Not found is a configuration error naming all three.
func findPL1C(p *project.Project, sysroot string) (string, error) {
	if v := os.Getenv("MBT_PL1C370"); v != "" {
		bin, err := exec.LookPath(v)
		if err != nil {
			return "", fmt.Errorf("MBT_PL1C370=%s: %v", v, err)
		}
		return bin, nil
	}
	// sysroot is "<bindir>/../cc370" (toolchain.Sysroot); the compiler sits in
	// <bindir>, beside cc370/as370/ld370.
	if sysroot != "" {
		cand := filepath.Join(filepath.Dir(sysroot), "bin", "pl1c370")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	if bin, err := exec.LookPath("pl1c370"); err == nil {
		return bin, nil
	}
	return "", &project.ConfigError{Msg: fmt.Sprintf(
		"%s is PL/I, and pl1c370 was not found: not in MBT_PL1C370, not in the sysroot "+
			"(<sysroot>/../bin/pl1c370), not on PATH -- install it with pl1c370's `make install` "+
			"(it copies the compiler and pl1rt.a into the sysroot)", pl1Source(p))}
}
