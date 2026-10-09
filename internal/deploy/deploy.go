// Package deploy packs the built modules into one LINKLIB XMIT and puts
// them into the target library on MVS, without ever deleting it -- so a
// running server that holds it DISP=SHR need not stop (mbt#105):
//
//  1. ld370 --pack build/NAME.iebcopy ... -o build/{project}.deploy -xmit
//  2. upload to {HLQ}.MBT.XMIT.IN
//  3. a target that does not exist is allocated with room to spare (RECEIVE
//     would size it to its contents, full from the start); an existing one
//     is checked for free space first
//  4. one job: TSO RECEIVE into {HLQ}.{PROJECT}.MBTDPLY, then IEBCOPY
//     replaces the members in the target, DISP=SHR
//  5. the member list proves the modules are there; the staging data sets
//     go (the XMIT is kept when the job failed, for a retry)
package deploy

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/config"
	"github.com/mvslovers/mbt/internal/mvsmf"
	"github.com/mvslovers/mbt/internal/tpl"
	"github.com/mvslovers/mbt/internal/version"
	jcltpl "github.com/mvslovers/mbt/templates/jcl"
)

// Exit codes (spec 11.1).
const (
	ExitOK, ExitBuild, ExitConfig, ExitMainframe = 0, 1, 2, 4
)

// Options for one deploy.
type Options struct {
	Root, BuildDir, LD string
	Project, Version   string
	Modules            []string // [[module]] names, in order
	Target             string   // --target
	ProjectTarget      string   // [deploy] target
	Only               []string // --module
	DryRun, Verbose    bool
	// Volume is where Receive puts a library (mbt test --mvs's TESTLIB):
	// "" for the target's volume
	Volume string
	// IgnoreSpace copies although the free-space estimate says no (the
	// library's secondary extents may cover it); Reallocate deletes the
	// target and allocates it again, larger, on its volume -- only while
	// nothing holds it
	IgnoreSpace, Reallocate bool
	Out, Err                io.Writer
	Config                  *config.Config
	Client                  *mvsmf.Client // nil: from Config
}

var notQualifier = regexp.MustCompile(`[^A-Z0-9@#$]`)

// MVSQualifier: the name as a dataset qualifier (A-Z 0-9 @ # $, 8 at most).
func MVSQualifier(name string) string {
	q := notQualifier.ReplaceAllString(strings.ToUpper(name), "")
	if len(q) > 8 {
		q = q[:8]
	}
	return q
}

func (o *Options) log(f string, a ...any) { fmt.Fprintf(o.Out, "[mbt] "+f+"\n", a...) }

// warn goes to Out, as mbtdeploy's _log_warn printed to stdout.
func (o *Options) warn(f string, a ...any) { fmt.Fprintf(o.Out, "[mbt] WARNING: "+f+"\n", a...) }
func (o *Options) fail(f string, a ...any) { fmt.Fprintf(o.Err, "[mbt] ERROR: "+f+"\n", a...) }
func (o *Options) cont(f string, a ...any) { fmt.Fprintf(o.Err, "[mbt]        "+f+"\n", a...) }

// StagingSpace sizes the FB/80 staging dataset in tracks.
func StagingSpace(n int) []any {
	t := n/40000 + 30
	if t < 50 {
		t = 50
	}
	s := t / 4
	if s < 20 {
		s = 20
	}
	return []any{"TRK", t, s}
}

// ReceiveTimeout scales the RECEIVE poll with the XMIT: an expired poll
// reports a failure while the job may still be writing, and the obvious retry
// deletes the dataset it is filling (#78, #57).  MBT_DEPLOY_TIMEOUT overrides.
func ReceiveTimeout(n int) time.Duration {
	if e, _ := strconv.Atoi(os.Getenv("MBT_DEPLOY_TIMEOUT")); e > 0 {
		return time.Duration(e) * time.Second
	}
	return time.Duration(300+n/(1024*1024)*60) * time.Second
}

// Run deploys; it returns the exit code.
func Run(o Options) int {
	var built []string
	for _, m := range o.Modules {
		if _, err := os.Stat(filepath.Join(o.Root, o.BuildDir, m+".iebcopy")); err == nil {
			built = append(built, m)
		}
	}
	if len(o.Only) > 0 {
		want, known := map[string]bool{}, map[string]bool{}
		for _, m := range o.Only {
			want[strings.ToUpper(m)] = true
		}
		for _, m := range o.Modules {
			known[strings.ToUpper(m)] = true
		}
		var unknown []string
		for m := range want {
			if !known[m] {
				unknown = append(unknown, m)
			}
		}
		if len(unknown) > 0 {
			sortStrings(unknown)
			o.fail("unknown module(s): %s", strings.Join(unknown, ", "))
			return ExitConfig
		}
		var keep []string
		for _, m := range built {
			if want[strings.ToUpper(m)] {
				keep = append(keep, m)
			}
		}
		built = keep
	}
	if len(built) == 0 {
		o.fail("no built modules in %s/ (run 'mbt build' or 'mbt build <module>' first)", o.BuildDir)
		return ExitConfig
	}
	target := o.Target
	if target == "" {
		target = o.ProjectTarget
	}
	if target == "" {
		v, err := version.Parse(o.Version)
		if err != nil {
			o.fail("%v", err)
			return ExitConfig
		}
		target = fmt.Sprintf("%s.%s.%s.LINKLIB", o.Config.HLQ(), MVSQualifier(o.Project), v.VRM())
	}
	o.log("Deploy target: %s", target)
	o.log("Modules (%d): %s", len(built), strings.Join(built, ", "))

	args := []string{"--pack"}
	for _, m := range built {
		args = append(args, filepath.Join(o.BuildDir, m+".iebcopy"))
	}
	out := filepath.Join(o.BuildDir, o.Project+".deploy")
	args = append(args, "-o", out, "-xmit", "--dsn", target)
	if o.Verbose {
		o.log("+ %s %s", o.LD, strings.Join(args, " "))
	}
	cmd := exec.Command(o.LD, args...)
	cmd.Dir = o.Root
	if res, err := cmd.CombinedOutput(); err != nil {
		o.fail("%s --pack failed (%v):\n%s", o.LD, err, strings.TrimSpace(string(res)))
		return ExitBuild
	}
	xmit := out + ".xmit"
	o.log("Packed %d module(s) -> %s", len(built), filepath.Base(xmit))
	data, err := os.ReadFile(filepath.Join(o.Root, xmit))
	if err != nil {
		o.fail("%v", err)
		return ExitBuild
	}
	staging := o.Config.HLQ() + ".MBT.XMIT.IN"
	stage := fmt.Sprintf("%s.%s.MBTDPLY", o.Config.HLQ(), MVSQualifier(o.Project))
	if o.DryRun {
		o.log("[dry-run] would upload %s -> %s", filepath.Base(xmit), staging)
		o.log("[dry-run] would RECEIVE into %s, then IEBCOPY replace into %s (DISP=SHR; allocated first if missing)", stage, target)
		return ExitOK
	}

	c := o.Client
	if c == nil {
		port, err := o.Config.Port()
		if err != nil {
			o.fail("%v", err)
			return ExitConfig
		}
		c = mvsmf.New(o.Config.Host(), port, o.Config.User(), o.Config.Pass())
	}
	keep := false
	defer func() {
		if !keep && c.DatasetExists(staging) {
			if err := c.DeleteDataset(staging); err != nil {
				o.warn("could not delete staging dataset %s", staging)
			}
		}
	}()
	bytes := 0
	for _, m := range built {
		if st, err := os.Stat(filepath.Join(o.Root, o.BuildDir, m+".iebcopy")); err == nil {
			bytes += int(st.Size())
		}
	}
	step := func() error {
		attrs, exists := c.DatasetAttrs(target)
		fresh := false
		var alloc *Alloc
		if exists && o.Reallocate {
			vol := attrs["vol"]
			o.log("Reallocating %s on %s (--reallocate: deleted and allocated again, larger)...", target, vol)
			if err := c.DeleteDataset(target); err != nil {
				return fmt.Errorf("--reallocate: cannot delete %s, most likely a running server holds it -- stop it first: %v", target, err)
			}
			n, ok := NeededTracks(attrs["dev"], bytes, len(built))
			if !ok {
				n = 30
			}
			if _, total, ok := FreeTracks(attrs); ok && total > n {
				n = total // never smaller than it was
			}
			alloc = &Alloc{Volume: vol, Space: NewLibrarySpace(n)}
			fresh = true
		}
		if fresh {
			// just allocated for these modules: nothing to measure
		} else if !exists {
			n, ok := NeededTracks("3350", bytes, len(built))
			if !ok {
				n = 30
			}
			vol := o.Config.Volume()
			o.log("Allocating %s (%s, room for several deploys)...", target, orAny(vol))
			alloc = &Alloc{Volume: vol, Space: NewLibrarySpace(n)}
		} else if free, total, ok := FreeTracks(attrs); !ok {
			o.warn("cannot measure the free space of %s (device %s, %s) -- copying without the check", target, attrs["dev"], attrs["spacu"])
		} else if need, _ := NeededTracks(attrs["dev"], bytes, len(built)); need > free && !o.IgnoreSpace {
			return &SpaceError{Msg: fmt.Sprintf("%s has about %d of %d tracks free (%s%% used, %s extent(s)); the %d module(s) need about %d -- a replaced member's old space stays dead until the library is compressed",
				target, free, total, attrs["used"], attrs["extx"], len(built), need)}
		}
		if c.DatasetExists(staging) {
			if err := c.DeleteDataset(staging); err != nil {
				return err
			}
		}
		if err := c.CreateDataset(staging, "PS", "FB", 80, 3120, StagingSpace(len(data)), "SYSDA", ""); err != nil {
			return err
		}
		o.log("Uploading %s -> %s...", filepath.Base(xmit), staging)
		if err := c.UploadBinary(staging, data); err != nil {
			return err
		}
		if c.DatasetExists(stage) {
			if err := c.DeleteDataset(stage); err != nil {
				return err
			}
		}
		o.log("RECEIVE %s -> %s, IEBCOPY -> %s (replace, DISP=SHR)...", staging, stage, target)
		if err := Merge(&o, c, staging, stage, target, filepath.Join(o.BuildDir, "deploy.spool"), len(data), alloc); err != nil {
			return err
		}
		// mvsMF cannot place a data set; JCL can, and this checks it did
		if alloc != nil && alloc.Volume != "" {
			if a, ok := c.DatasetAttrs(target); ok && a["vol"] != alloc.Volume {
				return &ReceiveError{Headline: fmt.Sprintf("%s was allocated on %s, not on %s as asked -- an APF entry naming %s does not cover it", target, a["vol"], alloc.Volume, alloc.Volume)}
			}
		}
		// the inventory, not the return code, says the modules are there
		have, err := c.Members(target)
		if err != nil {
			return fmt.Errorf("cannot list %s after the copy: %v", target, err)
		}
		in := map[string]bool{}
		for _, m := range have {
			in[m] = true
		}
		var missing []string
		for _, m := range built {
			if !in[strings.ToUpper(m)] {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			return &ReceiveError{Headline: fmt.Sprintf("the deploy job ended clean, but %s lacks %s", target, strings.Join(missing, ", ")),
				Details: []string{"see " + filepath.Join(o.BuildDir, "deploy.spool")}}
		}
		if c.DatasetExists(stage) {
			if err := c.DeleteDataset(stage); err != nil {
				o.warn("could not delete %s", stage)
			}
		}
		if a, ok := c.DatasetAttrs(target); ok {
			o.log("%s", Fill(target, a))
			if u, err := strconv.Atoi(a["used"]); err == nil && u >= 80 {
				o.warn("%s is %d%% full: compress it while nothing holds it (the server's stop), or 'mbt deploy --reallocate' while the server is stopped", target, u)
			}
		}
		return nil
	}
	if err := step(); err != nil {
		if se, ok := err.(*SpaceError); ok {
			o.fail("%s", se.Msg)
			o.cont("nothing was copied. Either compress it while nothing holds it (stop the server), or")
			o.cont("'mbt deploy --reallocate' while the server is stopped (same volume, larger), or")
			o.cont("'mbt deploy --ignore-space' if its secondary extents will cover it")
			o.cont("(the directory's free entries cannot be measured from here)")
			return ExitMainframe
		}
		if re, ok := err.(*ReceiveError); ok {
			keep = true
			o.fail("%s", re.Headline)
			for _, d := range re.Details {
				o.cont("%s", d)
			}
			o.cont("%s kept for a retry of the RECEIVE", staging)
			return ExitMainframe
		}
		o.fail("deploy failed: %v", err)
		return ExitMainframe
	}
	o.log("Deploy complete: %d module(s) -> %s", len(built), target)
	return ExitOK
}

// SpaceError: the target has too little room for the modules.
type SpaceError struct{ Msg string }

func (e *SpaceError) Error() string { return e.Msg }

func orAny(vol string) string {
	if vol == "" {
		return "any volume"
	}
	return vol
}

// Alloc is a target library the deploy job allocates first: mvsMF's
// create cannot name a volume, and an APF entry is data set plus volume.
type Alloc struct {
	Volume string // "" for anywhere
	Space  []any  // NewLibrarySpace: unit, primary, secondary, directory blocks
}

func (a *Alloc) step(target string) string {
	if a == nil {
		return ""
	}
	vol := ""
	if a.Volume != "" {
		vol = "VOL=SER=" + a.Volume + ","
	}
	// one card per operand: a 44-character DSN leaves no room beside it
	return fmt.Sprintf("//ALLOC   EXEC PGM=IEFBR14\n//LIB      DD DSN=%s,\n//            DISP=(NEW,CATLG,DELETE),\n//            UNIT=SYSDA,%s\n//            SPACE=(%s,(%v,%v,%v)),\n//            DCB=(DSORG=PO,RECFM=U,BLKSIZE=15040)\n",
		target, vol, a.Space[0], a.Space[1], a.Space[2], a.Space[3])
}

// Merge submits the deploy job -- allocate the target when alloc says so,
// RECEIVE xmitDSN into stage, IEBCOPY stage into target with replace,
// DISP=SHR -- and waits for it.
func Merge(o *Options, c *mvsmf.Client, xmitDSN, stage, target, spoolPath string, n int, alloc *Alloc) error {
	jc := mvsmf.Jobcard("MBTDEPL", o.Config.JobClass(), o.Config.MsgClass(), "MBT DEPLOY")
	cmd := fmt.Sprintf(" RECEIVE INDSN('%s') -\n  DATASET('%s')", xmitDSN, stage)
	if vol := o.Config.Volume(); vol != "" {
		cmd += fmt.Sprintf(" -\n  VOLUME('%s')", vol)
	}
	t, _ := jcltpl.Templates.ReadFile("deploymerge.jcl.tpl")
	jcl := tpl.SafeSubstitute(string(t), map[string]string{"JOBCARD": jc, "XMIT_DSN": xmitDSN, "STAGE_DSN": stage, "TARGET_DSN": target, "RECEIVE_CMD": cmd, "ALLOC_STEP": alloc.step(target)})
	if o.Verbose {
		o.log("+ RECEIVE INDSN('%s') DATASET('%s'); IEBCOPY COPY OUTDD=TARGET,INDD=((STAGE,R)) -> %s", xmitDSN, stage, target)
	}
	timeout := ReceiveTimeout(n)
	res, err := c.Submit(jcl, mvsmf.SubmitOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	if res.Spool != "" {
		if err := os.WriteFile(filepath.Join(o.Root, spoolPath), []byte(res.Spool), 0o644); err != nil {
			o.warn("could not write %s: %v", spoolPath, err)
			spoolPath = ""
		}
	} else {
		spoolPath = ""
	}
	if h, d := ReceiveFailure(res, target, timeout, spoolPath); h != "" {
		h = strings.Replace(h, "RECEIVE job", "deploy job", 1)
		h = strings.Replace(h, target+" was not written", target+" may hold some of the new members (IEBCOPY replaces one by one) -- see the spool", 1)
		return &ReceiveError{h, d}
	}
	return nil
}

// ReceiveError is a RECEIVE job that did not come back clean, with its
// diagnosis.
type ReceiveError struct {
	Headline string
	Details  []string
}

func (e *ReceiveError) Error() string { return e.Headline }

// Receive submits a TSO RECEIVE of xmitDSN into target and waits for it; the
// spool goes to spoolPath (relative to the project root).
func Receive(o *Options, c *mvsmf.Client, xmitDSN, target, spoolPath string, n int) error {
	jc := mvsmf.Jobcard("MBTDEPL", o.Config.JobClass(), o.Config.MsgClass(), "MBT DEPLOY")
	vol := o.Volume
	if vol == "" {
		vol = o.Config.Volume()
	}
	cmd := fmt.Sprintf(" RECEIVE INDSN('%s') -\n  DATASET('%s')", xmitDSN, target)
	if vol != "" {
		cmd += fmt.Sprintf(" -\n  VOLUME('%s')", vol)
	}
	if o.Verbose {
		v := ""
		if vol != "" {
			v = fmt.Sprintf(" VOLUME('%s')", vol)
		}
		o.log("+ RECEIVE INDSN('%s') DATASET('%s')%s", xmitDSN, target, v)
	}
	t, _ := jcltpl.Templates.ReadFile("receive.jcl.tpl")
	jcl := tpl.SafeSubstitute(string(t), map[string]string{"JOBCARD": jc, "XMIT_DSN": xmitDSN, "TARGET_DSN": target, "RECEIVE_CMD": cmd})
	timeout := ReceiveTimeout(n)
	res, err := c.Submit(jcl, mvsmf.SubmitOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	if res.Spool != "" {
		if err := os.WriteFile(filepath.Join(o.Root, spoolPath), []byte(res.Spool), 0o644); err != nil {
			o.warn("could not write %s: %v", spoolPath, err)
			spoolPath = ""
		}
	} else {
		spoolPath = ""
	}
	if h, d := ReceiveFailure(res, target, timeout, spoolPath); h != "" {
		return &ReceiveError{h, d}
	}
	return nil
}

// ReceiveFailure classifies a RECEIVE job; "" when it worked.  A TIMEOUT or
// an outcome the spool does not name is "unknown", never "failed": a rerun
// deletes the target first, so a false alarm would turn destructive.
func ReceiveFailure(r *mvsmf.JobResult, target string, timeout time.Duration, spoolPath string) (string, []string) {
	if r.Status == "CC" && r.RC <= 4 {
		return "", nil
	}
	job := r.JobName + " " + r.JobID
	var hint []string
	if spoolPath != "" {
		hint = []string{"the full spool is in " + spoolPath}
	}
	if r.Status == "TIMEOUT" || r.Status == "ACTIVE" {
		return fmt.Sprintf("RECEIVE job %s did not finish within %ds -- outcome unknown", job, int(timeout.Seconds())),
			append([]string{
				fmt.Sprintf("the job may still be running and writing %s", target),
				"check it on MVS before rerunning -- a rerun deletes that dataset first",
				"raise the poll with MBT_DEPLOY_TIMEOUT=<seconds>",
			}, hint...)
	}
	notWritten := target + " was not written"
	if r.Status == "JCL ERROR" || mvsmf.JCLErrorRE.MatchString(r.Spool) {
		return fmt.Sprintf("RECEIVE job %s was rejected -- %s", job, notWritten), append(mvsmf.JCLDiagnostics(r.Spool), hint...)
	}
	if r.Status == "ABEND" {
		return fmt.Sprintf("RECEIVE job %s abended -- %s", job, notWritten), hint
	}
	if r.Status == "CC" {
		return fmt.Sprintf("RECEIVE job %s failed with RC=%d -- %s", job, r.RC, notWritten), hint
	}
	return fmt.Sprintf("RECEIVE job %s ended with no usable status (%s) -- outcome unknown", job, r.Status),
		append([]string{fmt.Sprintf("check %s on MVS before rerunning -- a rerun deletes it first", target)}, hint...)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
