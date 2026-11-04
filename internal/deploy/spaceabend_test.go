package deploy

import (
	"strings"
	"testing"
	"time"

	"github.com/mvslovers/mbt/internal/mvsmf"
)

// the spool of a deploy whose IEBCOPY step filled a library without a
// secondary extent (job log section, then the step's own messages)
const sd37Spool = ` 0.55.26 JOB   34  IEC031I D37-04,IFG0554T,MBTDEPL,MERGE,TARGET,252,WORK01,
 0.55.26 JOB   34  IEC031I BREXX370.DEV.LINKLIB
 0.55.26 JOB   34  IEF450I MBTDEPL MERGE - ABEND SD37 U0000 - TIME=00.55.26
IEC031I D37-04,IFG0554T,MBTDEPL,MERGE,TARGET,252,WORK01,
IEC031I BREXX370.DEV.LINKLIB
`

func TestSpaceAbend(t *testing.T) {
	code, dsn, step, ok := SpaceAbend(sd37Spool)
	if !ok || code != "D37" || dsn != "BREXX370.DEV.LINKLIB" || step != "MERGE" {
		t.Errorf("got %q %q %q %v", code, dsn, step, ok)
	}
	if _, _, _, ok := SpaceAbend(" IEF450I MBTDEPL MERGE - ABEND S0C4 U0000\n"); ok {
		t.Error("S0C4 is not an out-of-space abend")
	}
}

func TestReceiveFailureSpaceAbend(t *testing.T) {
	r := &mvsmf.JobResult{JobName: "MBTDEPL", JobID: "JOB00034", Status: "ABEND", RC: 9999, Spool: sd37Spool}
	h, d := ReceiveFailure(r, "BREXX370.DEV.LINKLIB", time.Minute, "build/deploy.spool")
	for _, want := range []string{"ran out of space", "SD37", "MERGE", "BREXX370.DEV.LINKLIB"} {
		if !strings.Contains(h, want) {
			t.Errorf("headline lacks %q: %s", want, h)
		}
	}
	if strings.Contains(h, "abended") {
		t.Errorf("headline still says abended: %s", h)
	}
	if len(d) == 0 || !strings.Contains(d[0], "secondary") {
		t.Errorf("details: %v", d)
	}
	// any other abend keeps the plain wording
	r.Spool = " IEF450I MBTDEPL MERGE - ABEND S0C4 U0000\n"
	if h, _ := ReceiveFailure(r, "X", time.Minute, ""); !strings.Contains(h, "abended") {
		t.Errorf("S0C4: %s", h)
	}
}
