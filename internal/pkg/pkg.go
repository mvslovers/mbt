// Package pkg writes a project's release artifacts into dist/ -- mbt v2's
// `make package` minus the SMP installation package (internal/dist):
//
//   - <name>-<version>-load.xmit: every module packed into one load library
//     by ld370 --pack, its RECEIVE name <NAME>.<VRM>.LINKLIB;
//   - <name>-<version>-lib.tar.gz: the [lib] headers and archive, for
//     projects that depend on this one.
//
// The tarball is written here rather than by the host's tar: a macOS tar adds
// an AppleDouble ._ member for every file with extended attributes (#161).
package pkg

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/version"
)

// Options for a package run.
type Options struct {
	BuildDir, DistDir string
	LD                string
	Log               func(string)
	// Mtime stamps every tar header; zero means the files' own times.
	Mtime time.Time
}

// Prefix is "<name>-<version>", the stem of every artifact.
func Prefix(p *project.Project) string { return p.Name + "-" + p.Version }

// LoadDSN is the RECEIVE name embedded in the load XMIT: HLQ-free, from the
// project file only, so packaging needs no MVS configuration.
func LoadDSN(p *project.Project) (string, error) {
	v, err := version.Parse(p.Version)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(p.Name) + "." + v.VRM() + ".LINKLIB", nil
}

// Run writes the artifacts.  The modules and the library must be built.
func Run(p *project.Project, o Options) error {
	root := p.Root
	dist := filepath.Join(root, o.DistDir)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return err
	}
	prefix := Prefix(p)
	if len(p.Modules) > 0 {
		dsn, err := LoadDSN(p)
		if err != nil {
			return err
		}
		o.Log(fmt.Sprintf("Packaging %s-load.xmit (%d module(s) -> %s)", prefix, len(p.Modules), dsn))
		args := []string{"--pack"}
		for _, m := range p.Modules {
			args = append(args, path.Join(o.BuildDir, m.Name+".iebcopy"))
		}
		args = append(args, "-o", path.Join(o.DistDir, prefix+"-load"), "-xmit", "--dsn", dsn)
		cmd := exec.Command(o.LD, args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ld370 --pack failed: %v\n%s", err, out)
		}
	}
	if p.Lib != nil {
		o.Log(fmt.Sprintf("Packaging %s-lib.tar.gz", prefix))
		if err := libTarball(p, o, filepath.Join(dist, prefix+"-lib.tar.gz")); err != nil {
			return err
		}
	}
	return nil
}

// libTarball: <prefix>/include/<each header, flat> and, when the [lib] has
// members, <prefix>/lib/<name>.a -- what v2's tar of build/pkg-lib held.
func libTarball(p *project.Project, o Options, out string) error {
	prefix := Prefix(p)
	type entry struct{ name, src string }
	entries := []entry{{prefix + "/", ""}, {prefix + "/include/", ""}}
	for _, h := range p.Lib.Headers {
		entries = append(entries, entry{prefix + "/include/" + path.Base(h), filepath.Join(p.Root, h)})
	}
	if len(p.Lib.Sources) > 0 {
		entries = append(entries, entry{prefix + "/lib/", ""},
			entry{prefix + "/lib/" + p.Lib.Name + ".a", filepath.Join(p.Root, o.BuildDir, p.Lib.Name+".a")})
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o755, Typeflag: tar.TypeDir, ModTime: o.Mtime, Format: tar.FormatPAX}
		var data []byte
		if e.src != "" {
			st, err := os.Stat(e.src)
			if err != nil {
				return fmt.Errorf("%s: %v", e.src, err)
			}
			if data, err = os.ReadFile(e.src); err != nil {
				return err
			}
			h.Typeflag, h.Mode, h.Size = tar.TypeReg, 0o644, int64(len(data))
			if o.Mtime.IsZero() {
				h.ModTime = st.ModTime()
			}
		} else if o.Mtime.IsZero() {
			h.ModTime = time.Now()
		}
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
