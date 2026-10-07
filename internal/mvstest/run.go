package mvstest

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
	"github.com/mvslovers/mbt/internal/deploy"
	"github.com/mvslovers/mbt/internal/mvsmf"
	"github.com/mvslovers/mbt/internal/version"
)

// Exit codes: 0 all passed, 1 tests failed, 2 configuration, 4 mainframe
// (a runner that did not run, or whose output could not be read), 5 a
// fixture member that did not load.
const (
	ExitOK, ExitFailed, ExitConfig, ExitMainframe, ExitDataset = 0, 1, 2, 4, 5
)

// TestDecl is what the runner needs of a [[test]].
type TestDecl struct {
	Name                 string
	Parm, ParmBatch, TSO *string
	Fixtures             []FixtureDecl
}

// FixtureDecl is one [[test.fixture]] as declared.
type FixtureDecl struct {
	DD      string
	Members []string // host files
}

// Options for one run.
type Options struct {
	Root, BuildDir, LD string
	Project, Version   string
	Tests              []TestDecl // every [[test]], in order
	TestTarget         string     // [test_deploy] target
	LinkTarget         string     // --target
	ProjectTarget      string     // [deploy] target
	Only               []string
	NoDeploy, Verbose  bool
	Out, Err           io.Writer
	Config             *config.Config
	Client             *mvsmf.Client
}

func (o *Options) log(f string, a ...any)  { fmt.Fprintf(o.Out, "[mbt] "+f+"\n", a...) }
func (o *Options) fail(f string, a ...any) { fmt.Fprintf(o.Err, "[mbt] ERROR: "+f+"\n", a...) }
func (o *Options) cont(f string, a ...any) { fmt.Fprintf(o.Err, "[mbt]        "+f+"\n", a...) }
func (o *Options) warn(f string, a ...any) { fmt.Fprintf(o.Err, "[mbt] WARNING: "+f+"\n", a...) }

var (
	passRE = regexp.MustCompile(`(?m)^\s*PASS:`)
	failRE = regexp.MustCompile(`(?m)^\s*FAIL:`)
)

func memberOf(path string) string {
	b := filepath.Base(path)
	if i := strings.LastIndex(b, "."); i > 0 {
		b = b[:i]
	}
	b = strings.ToUpper(b)
	if len(b) > 8 {
		b = b[:8]
	}
	return b
}

// Run executes test-mvs and returns the exit code.
func Run(o Options) int {
	var tests []string
	want := map[string]bool{}
	for _, x := range o.Only {
		want[strings.ToUpper(x)] = true
	}
	decl := map[string]TestDecl{}
	for _, t := range o.Tests {
		if t.Name == "" {
			continue
		}
		decl[t.Name] = t
		if _, err := os.Stat(filepath.Join(o.Root, o.BuildDir, t.Name+".iebcopy")); err != nil {
			continue
		}
		if len(want) > 0 && !want[strings.ToUpper(t.Name)] {
			continue
		}
		tests = append(tests, t.Name)
	}
	if len(tests) == 0 {
		o.fail("no built test modules in %s/ (run 'mbt build --tests' first)", o.BuildDir)
		return ExitConfig
	}
	v, err := version.Parse(o.Version)
	if err != nil {
		o.fail("%v", err)
		return ExitConfig
	}
	hlq, name := o.Config.HLQ(), deploy.MVSQualifier(o.Project)

	// Fixtures first: a configuration error should not cost an upload.
	fixtures := map[string][]Fixture{}
	for _, t := range tests {
		d := decl[t]
		if len(d.Fixtures) == 0 {
			continue
		}
		var blocks []Fixture
		dds := map[string]bool{}
		for _, fx := range d.Fixtures {
			dd := strings.ToUpper(fx.DD)
			if dds[dd] {
				o.fail("fixture: test %s: fixture DD %s is declared twice -- list all its members in one [[test.fixture]] block", t, dd)
				return ExitConfig
			}
			dds[dd] = true
			var members [][2]string
			seen := map[string]bool{}
			for _, mf := range fx.Members {
				m := memberOf(mf)
				if seen[m] {
					continue
				}
				seen[m] = true
				data, err := os.ReadFile(filepath.Join(o.Root, mf))
				if err != nil {
					o.fail("fixture: %v", err)
					return ExitConfig
				}
				members = append(members, [2]string{m, string(data)})
			}
			blocks = append(blocks, Fixture{DD: fx.DD, PDS: fmt.Sprintf("%s.%s.FIX.%s.%s", hlq, name, t, dd), Members: members})
		}
		fixtures[t] = blocks
	}

	testlib := o.TestTarget
	if testlib == "" {
		testlib = fmt.Sprintf("%s.%s.%s.TESTLIB", hlq, name, v.VRM())
	}
	linklib := o.LinkTarget
	if linklib == "" {
		linklib = o.ProjectTarget
	}
	if linklib == "" {
		linklib = fmt.Sprintf("%s.%s.%s.LINKLIB", hlq, name, v.VRM())
	}
	o.log("Test library:  %s (%d test(s))", testlib, len(tests))
	c := o.Client
	if c == nil {
		port, err := o.Config.Port()
		if err != nil {
			o.fail("%v", err)
			return ExitConfig
		}
		c = mvsmf.New(o.Config.Host(), port, o.Config.User(), o.Config.Pass())
	}
	if !c.DatasetExists(linklib) {
		o.log("Runtime LINKLIB: %s not deployed yet -- running from TESTLIB only", linklib)
		linklib = ""
	} else {
		o.log("Runtime LINKLIB: %s", linklib)
	}

	if !o.NoDeploy {
		if code := o.deployTests(c, tests, testlib); code != ExitOK {
			return code
		}
	}

	for _, t := range tests {
		blocks, ok := fixtures[t]
		if !ok {
			continue
		}
		legacy := fmt.Sprintf("%s.%s.FIX.%s", hlq, name, t)
		err := func() error {
			if c.DatasetExists(legacy) {
				if err := c.DeleteDataset(legacy); err != nil {
					return err
				}
			}
			for _, fx := range blocks {
				if c.DatasetExists(fx.PDS) {
					if err := c.DeleteDataset(fx.PDS); err != nil {
						return err
					}
				}
				sp := FixtureSpace(fx.Members)
				if err := c.CreateDataset(fx.PDS, "PO", "FB", 80, 3120, sp, "SYSDA", ""); err != nil {
					return err
				}
				o.log("Fixture %s (%d member(s) for %s %s, TRK(%v,%v,%v))", fx.PDS, len(fx.Members), t, fx.DD, sp[1], sp[2], sp[3])
			}
			return nil
		}()
		if err != nil {
			o.fail("fixture alloc failed for %s: %v", t, err)
			return ExitMainframe
		}
	}

	parms := map[string]Parm{}
	for _, t := range tests {
		d := decl[t]
		p := Parm{Batch: d.ParmBatch, TSO: d.TSO}
		if p.Batch == nil {
			p.Batch = d.Parm
		}
		if p.TSO == nil {
			p.TSO = d.Parm
		}
		if p.Batch != nil || p.TSO != nil {
			parms[t] = p
		}
	}
	jc := mvsmf.Jobcard("MBTTEST", o.Config.JobClass(), o.Config.MsgClass(), "MBT TEST")
	jcl, steps, fxSteps, stepOrder, fxOrder := GenRunner(jc, tests, testlib, linklib, fixtures, parms)
	runnerPath := filepath.Join(o.BuildDir, "test-runner.jcl")
	os.WriteFile(filepath.Join(o.Root, runnerPath), []byte(jcl), 0o644)
	o.log("Runner JCL -> %s (%d step(s))", runnerPath, len(steps)+len(fxSteps))

	timeout, _ := strconv.Atoi(os.Getenv("MBT_TEST_TIMEOUT"))
	if timeout <= 0 {
		timeout = 10 * (len(steps) + len(fxSteps))
		if timeout < 120 {
			timeout = 120
		}
	}
	res, err := c.Submit(jcl, mvsmf.SubmitOptions{Timeout: time.Duration(timeout) * time.Second})
	if err != nil {
		o.fail("runner submit failed: %v", err)
		return ExitMainframe
	}
	spool := res.Spool
	spoolPath := filepath.Join(o.BuildDir, "test-runner.spool")
	os.WriteFile(filepath.Join(o.Root, spoolPath), []byte(spool), 0o644)
	job := res.JobName
	if job == "" {
		job = "MBTTEST"
	}
	rows := map[string]map[string]Verdict{}
	for _, s := range stepOrder {
		st := steps[s]
		if rows[st.Test] == nil {
			rows[st.Test] = map[string]Verdict{}
		}
		rc, status := StepRC(spool, job, s)
		rows[st.Test][st.Leg] = Verdict{rc, status}
	}
	if h, d := JobFailure(res.Status, spool, rows, job, res.JobID, timeout, runnerPath, spoolPath, res.SpoolErrors); h != "" {
		o.fail("%s", h)
		for _, l := range d {
			o.cont("%s", l)
		}
		return ExitMainframe
	}
	np, nf := len(passRE.FindAllStringIndex(spool, -1)), len(failRE.FindAllStringIndex(spool, -1))
	fxFailed := FixtureFailedTests(spool, job, fxSteps, fxOrder, res.SpoolErrors)
	lines, failed, unread := Matrix(rows, res.SpoolErrors, fxFailed)
	fmt.Fprintln(o.Out)
	for _, l := range lines {
		fmt.Fprintln(o.Out, l)
	}
	fmt.Fprintf(o.Out, "\n  job %s %s  | assertions (batch+tso): %d PASS, %d FAIL\n", job, res.JobID, np, nf)
	fh, fd := FixtureFailures(spool, job, fxSteps, fxOrder, res.SpoolErrors)
	if fh != "" {
		o.fail("%s", fh)
		for _, l := range fd {
			o.cont("%s", l)
		}
		o.cont("the full spool is in %s", spoolPath)
	}
	if unread > 0 {
		o.fail("%d step(s) have no verdict because the spool could not be read in full -- shown as ?? above, not as failures", unread)
		for _, l := range ReadbackDetails(res.SpoolErrors) {
			o.cont("%s", l)
		}
	} else if len(res.SpoolErrors) > 0 {
		o.warn("part of the spool could not be read (%d request(s) failed); every step still has a return code, but the assertion tally may be short", len(res.SpoolErrors))
		for i, l := range res.SpoolErrors {
			if i >= maxSpoolErrors {
				break
			}
			o.cont("%s", l)
		}
	}
	switch {
	case fh != "":
		return ExitDataset
	case failed > 0:
		o.log("%d step(s) FAILED", failed)
		return ExitFailed
	case unread > 0:
		return ExitMainframe
	}
	o.log("all test steps passed")
	return ExitOK
}

func (o *Options) deployTests(c *mvsmf.Client, tests []string, testlib string) int {
	args := []string{"--pack"}
	for _, t := range tests {
		args = append(args, filepath.Join(o.BuildDir, t+".iebcopy"))
	}
	out := filepath.Join(o.BuildDir, o.Project+".test")
	args = append(args, "-o", out, "-xmit", "--dsn", testlib)
	if o.Verbose {
		o.log("+ %s %s", o.LD, strings.Join(args, " "))
	}
	cmd := exec.Command(o.LD, args...)
	cmd.Dir = o.Root
	if res, err := cmd.CombinedOutput(); err != nil {
		o.fail("%s --pack failed (%v):\n%s", o.LD, err, strings.TrimSpace(string(res)))
		return ExitConfig
	}
	xmit := out + ".xmit"
	data, err := os.ReadFile(filepath.Join(o.Root, xmit))
	if err != nil {
		o.fail("%v", err)
		return ExitConfig
	}
	staging := o.Config.HLQ() + ".MBT.XMIT.IN"
	keep := false
	defer func() {
		if !keep && c.DatasetExists(staging) {
			c.DeleteDataset(staging)
		}
	}()
	dopts := &deploy.Options{Root: o.Root, Verbose: o.Verbose, Out: o.Out, Err: o.Err, Config: o.Config}
	err = func() error {
		if c.DatasetExists(staging) {
			if err := c.DeleteDataset(staging); err != nil {
				return err
			}
		}
		if err := c.CreateDataset(staging, "PS", "FB", 80, 3120, deploy.StagingSpace(len(data)), "SYSDA", ""); err != nil {
			return err
		}
		o.log("Uploading %s -> %s...", filepath.Base(xmit), staging)
		if err := c.UploadBinary(staging, data); err != nil {
			return err
		}
		if c.DatasetExists(testlib) {
			if err := c.DeleteDataset(testlib); err != nil {
				return err
			}
		}
		o.log("RECEIVE %s -> %s...", staging, testlib)
		return deploy.Receive(dopts, c, staging, testlib, filepath.Join(o.BuildDir, "testlib-receive.spool"), len(data))
	}()
	if err != nil {
		if re, ok := err.(*deploy.ReceiveError); ok {
			keep = true
			o.fail("%s", re.Headline)
			for _, d := range re.Details {
				o.cont("%s", d)
			}
			o.cont("%s kept for a retry of the RECEIVE", staging)
			return ExitMainframe
		}
		o.fail("test deploy failed: %v", err)
		return ExitMainframe
	}
	return ExitOK
}

// JobFailure: did the runner fail as a whole?  Only when no step got a
// verdict (#74); then a JCL error, an expired poll, an unreadable spool (#87)
// or plainly no return codes.
func JobFailure(status, spool string, rows map[string]map[string]Verdict, job, jobid string, timeout int, runnerPath, spoolPath string, errs []string) (string, []string) {
	if len(rows) == 0 {
		return "", nil
	}
	for _, legs := range rows {
		for _, v := range legs {
			if v.Status != NoRC {
				return "", nil
			}
		}
	}
	if mvsmf.JCLErrorRE.MatchString(spool) || status == "JCL ERROR" {
		return fmt.Sprintf("runner job %s %s was rejected -- no test ran", job, jobid),
			append(mvsmf.JCLDiagnostics(spool), "the generated JCL is in "+runnerPath)
	}
	if status == "TIMEOUT" {
		return fmt.Sprintf("runner job %s %s did not finish within %ds -- no test result", job, jobid, timeout),
			[]string{"the job may still be running -- check it on MVS before rerunning", "raise the poll with MBT_TEST_TIMEOUT=<seconds>"}
	}
	if len(errs) > 0 {
		d := ReadbackDetails(errs)
		if spool != "" {
			d = append(d, "what was read is in "+spoolPath)
		}
		return fmt.Sprintf("runner job %s %s ran, but its output could not be read -- no test result", job, jobid), d
	}
	return fmt.Sprintf("runner job %s %s produced no return code for any step -- no test ran", job, jobid),
		[]string{"the full spool is in " + spoolPath, "the generated JCL is in " + runnerPath}
}
