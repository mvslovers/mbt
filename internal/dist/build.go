package dist

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	jcltpl "github.com/mvslovers/mbt/templates/jcl"

	"github.com/mvslovers/mbt/internal/tpl"
	"github.com/mvslovers/mbt/internal/version"
)

// Options for one package run.
type Options struct {
	Root, DistDir, BuildDir string
	Log                     func(string)
	// Mtime stamps the archive members; zero means the files' own times.
	Mtime time.Time
}

// Module is what the SYSMOD needs of a [[module]].
type Module struct {
	Name    string
	Aliases []string
}

func render(name string, vars map[string]string) (string, error) {
	data, err := jcltpl.Templates.ReadFile(name)
	if err != nil {
		return "", err
	}
	return tpl.SafeSubstitute(string(data), vars), nil
}

// statsDate is the commit date, so xmit370's ISPF statistics are
// reproducible; outside a checkout the files' times are used.
func statsDate(root string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%cd", "--date=format:%Y-%m-%d")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Build writes <name>-<version>-dist.zip and .tar.gz into the dist dir.
// The load XMIT must already be there (mbt package builds it first).
func Build(raw map[string]any, name, ver string, modules []Module, o Options) error {
	v, err := version.Parse(ver)
	if err != nil {
		return &Error{err.Error()}
	}
	vrm := v.VRM()
	d, err := Parse(raw, vrm)
	if err != nil || d == nil {
		return err
	}
	if len(modules) == 0 {
		return errf("[distribution] needs at least one [[module]] to ship")
	}
	prefix := name + "-" + ver
	allocJob, instJob, loadXMIT := prefix+"-alloc.jcl", prefix+"-inst.jcl", prefix+"-load.xmit"
	out := filepath.Join(o.Root, o.DistDir)
	stage := filepath.Join(o.Root, o.BuildDir, "dist-stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(out, loadXMIT)); err != nil {
		return errf("%s not found -- the load library XMIT is built by 'make package' and must exist before the package is assembled", filepath.Join(o.DistDir, loadXMIT))
	}

	// ---- the shipped source libraries ----
	subst := [][2]string{{"@LINKLIB@", d.SMP.Target}, {"@VRM@", vrm}, {"@VERSION@", ver}, {"@FMID@", d.SMP.FMID}}
	for _, l := range d.Libraries {
		subst = append(subst, [2]string{"@" + l.targetDD() + "@", l.Target})
	}
	sd := statsDate(o.Root)
	srcRoot := filepath.Join(stage, "src")
	files := map[string]string{d.SMP.LKLib: loadXMIT}
	for _, l := range d.Libraries {
		fname := fmt.Sprintf("%s-%s.xmit", prefix, strings.ToLower(l.targetDD()))
		staged, err := stageLibrary(o.Root, l, subst, srcRoot)
		if err != nil {
			return err
		}
		args := []string{"create", "-o", filepath.Join(stage, fname), "--dsn", l.Target}
		if sd != "" {
			args = append(args, "--stats-date", sd)
		}
		cmd := exec.Command("xmit370", append(args, staged)...)
		cmd.Dir = o.Root
		if res, err := cmd.CombinedOutput(); err != nil {
			if _, lerr := exec.LookPath("xmit370"); lerr != nil {
				return errf("xmit370 not found on PATH. It is part of the cc370 toolchain (make -C cc370 install); the release CI already installs it.")
			}
			return errf("xmit370 failed for %s (%v):\n%s", staged, err, strings.TrimSpace(string(res)))
		}
		files[l.Target] = fname
		o.Log(fmt.Sprintf("Packaged %s (%s -> %s)", fname, l.Dir, l.Target))
	}
	os.RemoveAll(srcRoot)

	// ---- the SYSMOD and the two jobs ----
	names := make([]string, 0, len(modules))
	aliases := map[string][]string{}
	for _, m := range modules {
		names = append(names, m.Name)
		if len(m.Aliases) > 0 {
			aliases[m.Name] = m.Aliases
		}
	}
	mcs, err := AssembleMCS(d, names, name, ver, aliases)
	if err != nil {
		return err
	}
	plan := receivePlan(d, files)
	jcA, err := jobcard(jobName(name, "ALC"), upperCut(name, 12)+" ALLOC")
	if err != nil {
		return err
	}
	allocJCL, err := render("smpalloc.jcl.tpl", map[string]string{
		"JOBCARD": jcA, "PRODUCT": name, "VERSION": ver, "FMID": d.SMP.FMID,
		"INSTALL_JOB": instJob, "ALLOC_DDS": renderAllocDDs(d),
	})
	if err != nil {
		return err
	}
	summary := []string{"//*   DELOLD    make the RECEIVE targets absent"}
	for _, r := range plan {
		summary = append(summary, fmt.Sprintf("//*   %s %s", ljust(r.step, 9), r.file), "//*               -> "+r.dsn)
	}
	cleanupNote := "//*   CLEANUP   scratch the staging library, now spent"
	var acceptSummary, acceptStep, last string
	if d.SMP.AcceptFMID {
		acceptSummary = "//*   ACCEPT    make this level the base a RESTORE returns to"
		acceptStep = "//*\n" +
			"//* ---- ACCEPT -------------------------------------------------------\n" +
			"//* Accepting the FMID once fills the distribution library, so a\n" +
			"//* later RESTORE has a previous level to go back to. Without it\n" +
			"//* a RESTORE would DELETE the module instead of reverting it.\n" +
			"//* Service (PTFs) is deliberately never accepted.\n" +
			"//*\n" +
			fmt.Sprintf("//ACCEPT  EXEC SMPAPP,COND=%s\n", acceptCond(d)) +
			renderApplyDDs(d) + "\n" +
			"//SMPCNTL  DD  *\n" +
			fmt.Sprintf(" ACCEPT S(%s) DIS(WRITE) .\n", d.SMP.FMID) +
			"/*"
		last = "ACCEPT.HMASMP"
	} else {
		acceptSummary = "//*   (no ACCEPT -- this level never becomes the restore base)"
		acceptStep = "//*"
		last = "APPLY.HMASMP"
	}
	cleanup, err := renderCleanupStep(d, last)
	if err != nil {
		return err
	}
	jcI, err := jobcard(jobName(name, "INS"), upperCut(name, 12)+" INSTALL")
	if err != nil {
		return err
	}
	instJCL, err := render("smpinst.jcl.tpl", map[string]string{
		"JOBCARD": jcI, "PRODUCT": name, "VERSION": ver, "FMID": d.SMP.FMID,
		"ALLOC_JOB": allocJob, "EDIT_PREFIX": xmitEditPrefix, "LKLIB": d.SMP.LKLib,
		"DELIM": instreamDelimiter, "MCS": strings.TrimRight(mcs, "\n"),
		"RECEIVE_SUMMARY": strings.Join(summary, "\n"), "RECEIVE_STEPS": renderReceiveSteps(plan),
		"LAST_RECV": plan[len(plan)-1].step, "APPLY_DDS": renderApplyDDs(d),
		"ACCEPT_SUMMARY": acceptSummary + "\n" + cleanupNote, "ACCEPT_STEP": acceptStep,
		"CLEANUP_STEP": cleanup,
	})
	if err != nil {
		return err
	}
	if err := checkCardText(allocJCL, allocJob); err != nil {
		return err
	}
	if err := checkCardText(instJCL, instJob); err != nil {
		return err
	}
	os.WriteFile(filepath.Join(stage, allocJob), []byte(allocJCL), 0o644)
	os.WriteFile(filepath.Join(stage, instJob), []byte(instJCL), 0o644)
	o.Log(fmt.Sprintf("Generated %s and %s (FMID %s) in %s/", allocJob, instJob, d.SMP.FMID, filepath.Join(o.BuildDir, "dist-stage")))

	// ---- archive ----
	type item struct{ arc, src string }
	var payload []item
	if d.Readme != "" {
		p := filepath.Join(o.Root, d.Readme)
		if _, err := os.Stat(p); err != nil {
			return errf("[distribution] readme = '%s' does not exist", d.Readme)
		}
		payload = append(payload, item{"README.md", p})
	}
	payload = append(payload, item{loadXMIT, filepath.Join(out, loadXMIT)})
	for _, r := range plan {
		if r.dsn != d.SMP.LKLib {
			payload = append(payload, item{r.file, filepath.Join(stage, r.file)})
		}
	}
	payload = append(payload, item{allocJob, filepath.Join(stage, allocJob)}, item{instJob, filepath.Join(stage, instJob)})
	for _, e := range d.Extra {
		p := filepath.Join(o.Root, e)
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return errf("[distribution] extra = '%s' does not exist", e)
		}
		payload = append(payload, item{filepath.Base(e), p})
	}
	var arcs []archiveItem
	for _, it := range payload {
		arcs = append(arcs, archiveItem{prefix + "/" + it.arc, it.src})
	}
	if err := writeZip(filepath.Join(out, prefix+"-dist.zip"), arcs, o.Mtime); err != nil {
		return err
	}
	if err := writeTarGz(filepath.Join(out, prefix+"-dist.tar.gz"), arcs, o.Mtime); err != nil {
		return err
	}
	o.Log(fmt.Sprintf("Packaged %s-dist.zip and %s-dist.tar.gz (%d files)", prefix, prefix, len(payload)))
	return nil
}

// stageLibrary copies a library directory with placeholders substituted;
// subdirectories and dot-files are skipped, as xmit370 does.  Text is read
// with universal newlines, as v2 read it.
func stageLibrary(root string, l Library, subst [][2]string, stageRoot string) (string, error) {
	src := filepath.Join(root, l.Dir)
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return "", errf("[[distribution.library]] dir = '%s' is not a directory", l.Dir)
	}
	dest := filepath.Join(stageRoot, strings.ToLower(l.targetDD()))
	os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	entries, _ := os.ReadDir(src)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	count := 0
	for _, e := range entries {
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := memberName(filepath.Join(l.Dir, e.Name())); err != nil {
			return "", err
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return "", err
		}
		text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
		for _, s := range subst {
			text = strings.ReplaceAll(text, s[0], s[1])
		}
		if err := os.WriteFile(filepath.Join(dest, e.Name()), []byte(text), 0o644); err != nil {
			return "", err
		}
		count++
	}
	if count == 0 {
		return "", errf("[[distribution.library]] dir = '%s' contains no members", l.Dir)
	}
	return dest, nil
}

type archiveItem struct{ name, src string }

func mtimeOf(src string, fixed time.Time) time.Time {
	if !fixed.IsZero() {
		return fixed
	}
	if st, err := os.Stat(src); err == nil {
		return st.ModTime()
	}
	return time.Now()
}

func writeZip(path string, items []archiveItem, mtime time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	for _, it := range items {
		data, err := os.ReadFile(it.src)
		if err != nil {
			return err
		}
		w, err := z.CreateHeader(&zip.FileHeader{Name: it.name, Method: zip.Deflate, Modified: mtimeOf(it.src, mtime)})
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := z.Close(); err != nil {
		return err
	}
	return f.Close()
}

func writeTarGz(path string, items []archiveItem, mtime time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, it := range items {
		data, err := os.ReadFile(it.src)
		if err != nil {
			return err
		}
		h := &tar.Header{Name: it.name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: mtimeOf(it.src, mtime), Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}
