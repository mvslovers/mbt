package project

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// LoadV2 reads an mbt v2 project.toml.  The rules are v2's
// (scripts/mbtconfig.py at v2.2.0), so a project builds the same under both.
func LoadV2(root, file string) (*Project, error) {
	raw := map[string]any{}
	if _, err := toml.DecodeFile(filepath.Join(root, file), &raw); err != nil {
		return nil, configErr("%s: %v", file, err)
	}
	return fromRaw(root, file, raw)
}

// fromRaw builds the model from a v2-shaped table: a v2 project.toml as
// decoded, or an mbt.toml translated by LoadV3.
func fromRaw(root, file string, raw map[string]any) (*Project, error) {
	p := &Project{Root: root, File: file, Raw: raw}
	proj := table(raw, "project")
	p.Name = str(proj, "name", "unknown")
	p.Version = str(proj, "version", "0.0.0")
	p.Type = str(proj, "type", "application")

	build := table(raw, "build")
	p.CFlags = strs(build, "cflags")
	p.ASFlags = strs(build, "asflags")

	if err := validateNames(raw); err != nil {
		return nil, err
	}

	for _, m := range tables(raw, "module") {
		u, err := p.unit(m, false)
		if err != nil {
			return nil, err
		}
		p.Modules = append(p.Modules, u)
	}
	for _, t := range tables(raw, "test") {
		// mvs = false: a host-only test, never cross-compiled or linked.
		if b, ok := t["mvs"].(bool); ok && !b {
			continue
		}
		u, err := p.unit(t, true)
		if err != nil {
			return nil, err
		}
		p.Tests = append(p.Tests, u)
	}

	// One warning for the whole project (#158): rexx370 names crt1 ~60 times.
	var legacy []string
	for _, kind := range []string{"module", "test"} {
		for _, m := range tables(raw, kind) {
			if b, ok := m["mvs"].(bool); ok && !b {
				continue
			}
			if s, _ := m["startup"].(string); s == "crt0" || s == "crt1" {
				legacy = append(legacy, str(m, "name", "?"))
			}
		}
	}
	if len(legacy) > 0 {
		shown := legacy
		more := ""
		if len(shown) > 5 {
			shown, more = shown[:5], ", ..."
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"%d module(s)/test(s) name startup = \"crt0\" or \"crt1\" (%s%s); the CRT comes out of libc.a now -- drop the key, with [toolchain] libc370 >= 2.3.0 (#158)",
			len(legacy), strings.Join(shown, ", "), more))
	}

	if lib := table(raw, "lib"); len(lib) > 0 {
		srcs, empty := resolveSources(root, strs(lib, "sources"), nil)
		p.warnEmpty(empty)
		p.Lib = &Lib{Name: str(lib, "name", p.Name), Sources: srcs, Headers: strs(lib, "headers")}
	}
	if in := table(raw, "internal"); len(in) > 0 {
		srcs, empty := resolveSources(root, strs(in, "sources"), strs(in, "exclude"))
		p.warnEmpty(empty)
		p.HasInternal = true
		p.Internal = srcs
	}
	return p, nil
}

func (p *Project) warnEmpty(patterns []string) {
	for _, pat := range patterns {
		p.Warnings = append(p.Warnings, fmt.Sprintf("pattern '%s' matched no files", pat))
	}
}

func (p *Project) unit(m map[string]any, test bool) (*Unit, error) {
	name := str(m, "name", "")
	u := &Unit{Name: name, Test: test, Entry: str(m, "entry", "@@CRT0"), Raw: m}

	switch v := m["startup"].(type) {
	case nil:
		u.Startup, u.Startfile = StartupC, "crt0"
	case bool:
		if v {
			return nil, badStartup(name, v)
		}
		u.Startup = StartupNone
	case string:
		switch v {
		case "crt0", "crt1":
			u.Startup, u.Startfile = StartupC, v
		case "crtm":
			u.Startup = StartupCRTM
		default:
			return nil, badStartup(name, v)
		}
	default:
		return nil, badStartup(name, v)
	}
	if d, ok := m["dep_startup"]; ok {
		b, isBool := d.(bool)
		if !isBool {
			return nil, configErr("[[module]] %s: 'dep_startup' must be true or false, not %v", name, pyRepr(d))
		}
		u.DepStartup, u.DepStartupSet = b, true
	}
	if ac, ok := m["ac"].(int64); ok {
		u.AC = int(ac)
	}
	attrs, err := p.linkAttrs(m, name)
	if err != nil {
		return nil, err
	}
	u.Attrs = attrs
	u.Aliases = strs(m, "aliases")
	srcs, empty := resolveSources(p.Root, strs(m, "sources"), strs(m, "exclude"))
	p.warnEmpty(empty)
	u.Sources = srcs
	return u, nil
}

func badStartup(name string, v any) error {
	return configErr("[[module]] %s: startup = %s is not a valid value (leave it out for a C program; false for an own entry; \"crtm\")", name, pyRepr(v))
}

// linkAttrs: a declared attribute goes to ld370 in both directions, so the
// module does not depend on ld370's default (cc370#100).  norent/noreus are
// the old spelling.
func (p *Project) linkAttrs(m map[string]any, name string) ([]string, error) {
	want := map[string]bool{}
	for _, a := range []string{"rent", "reus", "refr"} {
		if v, ok := m[a]; ok {
			b, isBool := v.(bool)
			if !isBool {
				return nil, configErr("[[module]] %s: '%s' must be true or false, not %s", name, a, pyRepr(v))
			}
			want[a] = b
		}
	}
	for _, o := range [][2]string{{"norent", "rent"}, {"noreus", "reus"}} {
		v, ok := m[o[0]]
		if !ok {
			continue
		}
		if _, set := want[o[1]]; set {
			return nil, configErr("[[module]] %s: both '%s' and '%s' are set; keep '%s' only", name, o[0], o[1], o[1])
		}
		if b, _ := v.(bool); b {
			p.Warnings = append(p.Warnings, fmt.Sprintf("[[module]] %s: '%s' is deprecated, write '%s = false'", name, o[0], o[1]))
			want[o[1]] = false
		}
	}
	var flags []string
	for _, a := range []string{"rent", "reus", "refr"} {
		b, ok := want[a]
		switch {
		case !ok:
		case b:
			flags = append(flags, "--"+a)
		case a != "refr":
			flags = append(flags, "--no"+a)
		}
	}
	return flags, nil
}

var memberRE = regexp.MustCompile(`^[A-Z@#$][A-Z0-9@#$]{0,7}$`)

func checkMember(name any, kind string) error {
	s, ok := name.(string)
	if !ok || s == "" {
		return configErr("project.toml: a [[%s]] entry has no usable name", kind)
	}
	if len(s) > 8 {
		return configErr("project.toml: %s name \"%s\" is %d characters -- MVS member names are 8 at most", kind, s, len(s))
	}
	if !memberRE.MatchString(s) {
		return configErr("project.toml: %s name \"%s\" is not a valid MVS member name -- use A-Z 0-9 @ # $, not starting with a digit", kind, s)
	}
	return nil
}

func validateNames(raw map[string]any) error {
	for _, kind := range []string{"module", "test"} {
		for _, e := range tables(raw, kind) {
			if err := checkMember(e["name"], kind); err != nil {
				return err
			}
		}
	}
	for _, t := range tables(raw, "test") {
		if _, ok := t["aliases"]; ok {
			return configErr("project.toml: test \"%s\" has aliases -- only a [[module]] can have them", str(t, "name", ""))
		}
	}
	owner := map[string]string{}
	for _, m := range tables(raw, "module") {
		n := str(m, "name", "")
		owner[n] = n
	}
	for _, m := range tables(raw, "module") {
		n := str(m, "name", "")
		al, ok := m["aliases"]
		if !ok {
			continue
		}
		list, isList := al.([]any)
		if !isList {
			return configErr("project.toml: module \"%s\" aliases must be a list of names, e.g. aliases = [\"REXX\", \"RX\"]", n)
		}
		for _, a := range list {
			s, isStr := a.(string)
			if !isStr || s == "" {
				return configErr("project.toml: module \"%s\" has an alias that is not a name: %s", n, pyRepr(a))
			}
			if err := checkMember(s, "module "+n+" alias"); err != nil {
				return err
			}
			if other, taken := owner[s]; taken {
				what := "module"
				if other != s {
					what = fmt.Sprintf("an alias of module \"%s\"", other)
				}
				return configErr("project.toml: alias \"%s\" of module \"%s\" is already %s -- one name per library", s, n, what)
			}
			owner[s] = n
		}
	}
	return nil
}

// -- TOML access helpers ------------------------------------------------

func table(m map[string]any, key string) map[string]any {
	t, _ := m[key].(map[string]any)
	return t
}

func tables(m map[string]any, key string) []map[string]any {
	switch v := m[key].(type) {
	case []map[string]any:
		return v
	case []any:
		var out []map[string]any
		for _, e := range v {
			if t, ok := e.(map[string]any); ok {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

func str(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return def
}

func strs(m map[string]any, key string) []string {
	var out []string
	if l, ok := m[key].([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// pyRepr renders a value the way the v2 messages did (Python repr).
func pyRepr(v any) string {
	switch x := v.(type) {
	case string:
		return "'" + x + "'"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return fmt.Sprint(v)
}

// Resolve expands source patterns with the build's rules (sorted per
// pattern, excludes removed, duplicates dropped).
func (p *Project) Resolve(patterns, exclude []string) []string {
	out, _ := resolveSources(p.Root, patterns, exclude)
	return out
}

// RawTables returns the tables of an array of tables in the raw file
// ("module", "test"), host-only tests included.
func (p *Project) RawTables(key string) []map[string]any { return tables(p.Raw, key) }

// RawStrs reads a string list from a raw table.
func RawStrs(m map[string]any, key string) []string { return strs(m, key) }
