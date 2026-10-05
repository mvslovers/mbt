// Package project holds mbt's model of a project and the loaders that fill it.
//
// One model, several front ends: the v2 project.toml now (the differential
// comparison against mbt v2 runs on unchanged v2 project files), mbt.toml
// schema 3 once that schema is settled.  Everything after loading works on the
// model only.
package project

import (
	"fmt"
	"path"
	"strings"
)

// Startup says how a module's C runtime comes in.
type Startup int

const (
	// StartupC: a C program; the CRT comes out of libc.a by the entry name
	// (or, on a libc370 before 2.3.0, from the startfile in Startfile).
	StartupC Startup = iota
	// StartupNone: an own entry point, no C runtime (startup = false).
	StartupNone
	// StartupCRTM: the nested startup, crtm.o (startup = "crtm").
	StartupCRTM
)

// Unit is a load module or a test: everything needed to link it.
type Unit struct {
	Name      string
	Test      bool
	Entry     string
	Startup   Startup
	Startfile string // crt0 or crt1, only read on a sysroot without the CRT in libc.a
	// DepStartup: @@START comes from a dependency (dep_startup = true).
	DepStartup bool
	// DepStartupSet: the project said true or false (#62 asks modules to).
	DepStartupSet bool
	AC            int
	Attrs         []string // ld370 flags: --rent/--norent/--reus/--noreus/--refr
	Aliases       []string
	Sources       []string
	// Raw keeps the unit's table for checks that read more than the model
	// (module-data reads rent and ac).
	Raw map[string]any
}

// Lib is the project's public static archive.
type Lib struct {
	Name    string
	Sources []string
	Headers []string
}

// Project is the loaded, validated project.
type Project struct {
	Root    string
	File    string
	Name    string
	Version string
	Type    string // application, library, module
	CFlags  []string
	ASFlags []string
	Modules []*Unit
	Tests   []*Unit
	Lib     *Lib
	// HasInternal: the project declares [internal], the private autocall
	// archive every module and test links against.
	HasInternal bool
	Internal    []string // its sources
	// Warnings are shown to the user on every build.
	Warnings []string
	// Raw is the whole parsed file, for the stages not modelled yet.
	Raw map[string]any
}

// ConfigError is an invalid project file: nothing is built (exit 2).
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

func configErr(format string, a ...any) error {
	return &ConfigError{Msg: fmt.Sprintf(format, a...)}
}

// ObjectPath maps a source to its object: build/<stem>.o, whatever directory
// the source is in (src/ufsd#cmd.c -> build/ufsd#cmd.o).
func ObjectPath(builddir, src string) string {
	base := path.Base(src)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	return path.Join(builddir, base+".o")
}

// Units returns modules and tests, in that order.
func (p *Project) Units() []*Unit {
	return append(append([]*Unit{}, p.Modules...), p.Tests...)
}

// InternalArchive is the path of the private autocall archive, or "".
func (p *Project) InternalArchive(builddir string) string {
	if !p.HasInternal {
		return ""
	}
	return path.Join(builddir, p.Name+"int.a")
}
