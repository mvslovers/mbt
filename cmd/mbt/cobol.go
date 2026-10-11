package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/tools"
)

// cobolSource is the first COBOL source the build compiles -- of a module,
// a test, the [lib] archive or [internal] -- "" if none.
func cobolSource(p *project.Project) string {
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
			if project.IsCOBOL(s) {
				return s
			}
		}
	}
	return ""
}

// findCOBC resolves cobc370 for a project with COBOL sources, in this
// order: MBT_COBC370 (a path or a name on PATH), a [tools] entry named
// cobc370 (fetched and pinned like any tool), cobc370 on PATH. Not found is
// a configuration error naming all three.
func findCOBC(p *project.Project) (string, error) {
	if v := os.Getenv("MBT_COBC370"); v != "" {
		bin, err := exec.LookPath(v)
		if err != nil {
			return "", fmt.Errorf("MBT_COBC370=%s: %v", v, err)
		}
		return bin, nil
	}
	tl, err := tools.Declared(p.Raw)
	if err != nil {
		return "", err
	}
	for _, t := range tl {
		if t.Name == "cobc370" {
			return tools.Ensure(p.Root, t, tools.Options{Log: func(s string) { fmt.Printf("[mbt] %s\n", s) }})
		}
	}
	if bin, err := exec.LookPath("cobc370"); err == nil {
		return bin, nil
	}
	return "", &project.ConfigError{Msg: fmt.Sprintf(
		"%s is COBOL, and cobc370 was not found: not in MBT_COBC370, not a [tools] entry, not on PATH "+
			"(cobc370 at commit c531019 or later: -MMD writes the copybook dependencies)", cobolSource(p))}
}
