// Package deploy packs the built modules into one LINKLIB XMIT and RECEIVEs
// it on MVS -- mbt v2's `make deploy` (scripts/mbtdeploy.py).
//
//  1. ld370 --pack build/NAME.iebcopy ... -o build/{project}.deploy -xmit
//  2. upload to {HLQ}.MBT.XMIT.IN
//  3. DELETE the target if it exists (RECEIVE refuses to merge, mbt#105)
//  4. a TSO RECEIVE job: staging -> target
//  5. delete the staging dataset (kept when the RECEIVE failed, for a retry)
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
	Out, Err           io.Writer
	Config             *config.Config
	Client             *mvsmf.Client // nil: from Config
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
		o.fail("no built modules in %s/ (run 'make' or 'make <module>' first)", o.BuildDir)
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
	if o.DryRun {
		o.log("[dry-run] would upload %s -> staging", filepath.Base(xmit))
		o.log("[dry-run] would delete + RECEIVE -> %s", target)
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
	staging := o.Config.HLQ() + ".MBT.XMIT.IN"
	keep := false
	defer func() {
		if !keep && c.DatasetExists(staging) {
			if err := c.DeleteDataset(staging); err != nil {
				o.warn("could not delete staging dataset %s", staging)
			}
		}
	}()
	step := func() error {
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
		if c.DatasetExists(target) {
			o.log("Deleting existing %s (replace)...", target)
			if err := c.DeleteDataset(target); err != nil {
				return err
			}
		}
		o.log("RECEIVE %s -> %s...", staging, target)
		return Receive(&o, c, staging, target, filepath.Join(o.BuildDir, "receive.spool"), len(data))
	}
	if err := step(); err != nil {
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
	vol := o.Config.Volume()
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
