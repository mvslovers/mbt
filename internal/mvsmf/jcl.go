package mvsmf

import (
	"fmt"
	"regexp"
	"strings"
)

// Jobcard is mbt's standard JOB card (v2's mbt/jcl.py jobcard).
func Jobcard(jobname, jobclass, msgclass, description string) string {
	jn := strings.ToUpper(jobname)
	if len(jn) > 8 {
		jn = jn[:8]
	}
	field := jn + " "
	if len(jn) < 8 {
		field = fmt.Sprintf("%-8s", jn)
	}
	return fmt.Sprintf("//%sJOB (%s),'%s',\n//          CLASS=%s,MSGCLASS=%s,\n//          MSGLEVEL=(1,1),\n//          NOTIFY=&SYSUID",
		field, jobclass, description, jobclass, msgclass)
}

// JCLErrorRE matches JES's "job not run" line: not one step was attached.
var JCLErrorRE = regexp.MustCompile(`(?m)^.*IEF452I\s+(\S+)\s+JOB NOT RUN\s+-\s+JCL ERROR.*$`)

var (
	jclDiagRE = regexp.MustCompile(`(?m)^(.*?)(IEF6\d\dI\b.*)$`)
	stmtRE    = regexp.MustCompile(`^\s*(\d+)\s*$`)
)

// JCLDiagnostics returns the IEF6nnI lines of a rejected job, with the
// statement number where the interpreter gave one; deduplicated, at most 5.
func JCLDiagnostics(spool string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range jclDiagRE.FindAllStringSubmatch(spool, -1) {
		line := strings.TrimRight(m[2], " \t\r\n\f\v")
		if s := stmtRE.FindStringSubmatch(m[1]); s != nil {
			line = fmt.Sprintf("%s    (STMT %s)", line, s[1])
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) >= 5 {
			break
		}
	}
	return out
}
