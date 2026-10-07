package project

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mvslovers/mbt/internal/version"
)

// LoadV3 reads an mbt.toml (schema 3, internals/mbt-3-schema.md).  It
// translates the file into the shape of a v2 project.toml -- conventions
// filled in, tests discovered, modules and tests in name order -- and builds
// the model from that, so both schemas share every rule after loading.
func LoadV3(root, file string) (*Project, error) {
	in := map[string]any{}
	if _, err := toml.DecodeFile(filepath.Join(root, file), &in); err != nil {
		return nil, configErr("%s: %v", file, err)
	}
	raw, distErr, err := translateV3(root, in)
	if err != nil {
		return nil, err
	}
	p, err := fromRaw(root, file, raw)
	if err != nil {
		return nil, err
	}
	p.Schema = 3
	p.DistError = distErr
	return p, nil
}

var v3Keys = map[string][]string{
	"":             {"schema", "project", "toolchain", "dependencies", "build", "lib", "internal", "module", "tests", "test", "deploy", "distribution", "smp", "release", "tools"},
	"project":      {"name", "version", "kind"},
	"toolchain":    {"cc370", "libc370", "mbt"},
	"build":        {"include", "cflags", "asflags", "host"},
	"build.host":   {"cflags", "sources", "replace"},
	"lib":          {"name", "sources", "headers"},
	"internal":     {"sources", "exclude"},
	"module":       {"sources", "exclude", "rent", "reus", "refr", "entry", "startup", "dep_startup", "ac", "aliases"},
	"tests":        {"rent", "reus", "refr", "exclude"},
	"test":         {"sources", "exclude", "rent", "reus", "refr", "entry", "startup", "dep_startup", "ac", "host", "mvs", "parm", "fixtures"},
	"deploy":       {"target", "test_target"},
	"distribution": {"readme", "extra", "library"},
	"library":      {"target"},
	"smp":          {"prefix", "fmid", "delete", "system", "prereq", "accept_fmid", "lklib", "target", "distlib"},
	"release":      {"version_files"},
}

func checkKeys(t map[string]any, kind, where string) error {
	allowed := map[string]bool{}
	for _, k := range v3Keys[kind] {
		allowed[k] = true
	}
	var bad []string
	for k := range t {
		if !allowed[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	if where == "" {
		where = "the top level"
	}
	return configErr("mbt.toml: unknown key '%s' in %s (internals/mbt-3-schema.md)", bad[0], where)
}

func translateV3(root string, in map[string]any) (map[string]any, string, error) {
	if err := checkKeys(in, "", ""); err != nil {
		return nil, "", err
	}
	if s, ok := in["schema"].(int64); !ok || s != 3 {
		return nil, "", configErr("mbt.toml: 'schema = 3' is required")
	}
	out := map[string]any{}

	proj := table(in, "project")
	if err := checkKeys(proj, "project", "[project]"); err != nil {
		return nil, "", err
	}
	name, _ := proj["name"].(string)
	ver, _ := proj["version"].(string)
	if name == "" || ver == "" {
		return nil, "", configErr("mbt.toml: [project] needs a name and a version")
	}
	v, err := version.Parse(ver)
	if err != nil {
		return nil, "", configErr("mbt.toml: [project] version: %v", err)
	}
	out["project"] = map[string]any{"name": name, "version": ver, "type": str(proj, "kind", "application")}

	tc := table(in, "toolchain")
	if err := checkKeys(tc, "toolchain", "[toolchain]"); err != nil {
		return nil, "", err
	}
	t := map[string]any{}
	for _, k := range []string{"cc370", "libc370"} {
		if v, ok := tc[k].(string); ok {
			t[k] = v
		}
	}
	if len(t) > 0 {
		out["toolchain"] = t
	}
	if d := table(in, "dependencies"); d != nil {
		out["dependencies"] = d
	}

	build := table(in, "build")
	if err := checkKeys(build, "build", "[build]"); err != nil {
		return nil, "", err
	}
	var cflags []any
	for _, dir := range strs(build, "include") {
		cflags = append(cflags, "-I", dir)
	}
	for _, f := range strs(build, "cflags") {
		cflags = append(cflags, f)
	}
	b := map[string]any{}
	if len(cflags) > 0 {
		b["cflags"] = cflags
	}
	if a, ok := build["asflags"]; ok {
		b["asflags"] = a
	}
	out["build"] = b
	if host := table(build, "host"); host != nil {
		if err := checkKeys(host, "build.host", "[build.host]"); err != nil {
			return nil, "", err
		}
		out["host"] = host
	}
	for _, k := range []string{"lib", "internal"} {
		if t := table(in, k); t != nil {
			if err := checkKeys(t, k, "["+k+"]"); err != nil {
				return nil, "", err
			}
			out[k] = t
		}
	}

	mods, err := v3Modules(in)
	if err != nil {
		return nil, "", err
	}
	if len(mods) > 0 {
		out["module"] = mods
	}
	tests, err := v3Tests(root, in)
	if err != nil {
		return nil, "", err
	}
	if len(tests) > 0 {
		out["test"] = tests
	}

	dep := table(in, "deploy")
	if err := checkKeys(dep, "deploy", "[deploy]"); err != nil {
		return nil, "", err
	}
	upper := strings.ToUpper(name)
	target := str(dep, "target", "")
	if target == "" && len(mods) > 0 {
		target = upper + ".DEV.LINKLIB"
	}
	if target != "" {
		out["deploy"] = map[string]any{"target": target}
	}
	if tt := str(dep, "test_target", ""); tt != "" {
		out["test_deploy"] = map[string]any{"target": tt}
	}

	distErr := ""
	if d := table(in, "distribution"); d != nil {
		dist, msg, err := v3Distribution(in, d, upper, v)
		if err != nil {
			return nil, "", err
		}
		out["distribution"], distErr = dist, msg
	} else if table(in, "smp") != nil {
		return nil, "", configErr("mbt.toml: [smp] without [distribution] -- nothing would be packaged")
	}

	// checked where it is used (internal/tools)
	if t := table(in, "tools"); t != nil {
		out["tools"] = t
	}

	rel := table(in, "release")
	if err := checkKeys(rel, "release", "[release]"); err != nil {
		return nil, "", err
	}
	if vf, ok := rel["version_files"]; ok {
		out["release"] = map[string]any{"version_files": vf}
	}
	return out, distErr, nil
}

// keyedTables returns the entries of [kind.NAME] tables, sorted by name.
func keyedTables(in map[string]any, kind string) ([]string, map[string]map[string]any, error) {
	t := table(in, kind)
	if t == nil {
		if _, isList := in[kind].([]map[string]any); isList {
			return nil, nil, configErr("mbt.toml: [[%s]] is the v2 spelling -- write [%s.NAME] (mbt migrate converts a project.toml)", kind, kind)
		}
		return nil, nil, nil
	}
	names := make([]string, 0, len(t))
	entries := map[string]map[string]any{}
	for n, v := range t {
		e, ok := v.(map[string]any)
		if !ok {
			return nil, nil, configErr("mbt.toml: %s.%s must be a table: [%s.%s]", kind, n, kind, n)
		}
		names = append(names, n)
		entries[n] = e
	}
	sort.Strings(names)
	return names, entries, nil
}

func quoteKey(n string) string {
	for _, c := range n {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return `"` + n + `"`
		}
	}
	return n
}

// v3Unit copies a module or test entry into its v2 form.
func v3Unit(kind, n string, e map[string]any) (map[string]any, error) {
	where := fmt.Sprintf("[%s.%s]", kind, quoteKey(n))
	if s, ok := e["startup"].(string); ok && (s == "crt0" || s == "crt1") {
		return nil, configErr("mbt.toml: %s: startup = \"%s\" is gone -- the C runtime comes out of libc.a (libc370 >= 2.3.0); leave the key out", where, s)
	}
	m := map[string]any{"name": n}
	for k, v := range e {
		m[k] = v
	}
	return m, nil
}

func v3Modules(in map[string]any) ([]map[string]any, error) {
	names, entries, err := keyedTables(in, "module")
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, n := range names {
		e := entries[n]
		where := fmt.Sprintf("[module.%s]", quoteKey(n))
		if err := checkKeys(e, "module", where); err != nil {
			return nil, err
		}
		for _, a := range []string{"rent", "reus"} {
			if _, ok := e[a]; !ok {
				return nil, configErr("mbt.toml: %s: '%s' is required -- declare it true or false (cc370#100)", where, a)
			}
		}
		m, err := v3Unit("module", n, e)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// DiscoverTests lists test/**/*.c and test/**/*.asm, sorted, relative to
// root with forward slashes.  Hidden files and directories are skipped.
func DiscoverTests(root string) []string {
	var files []string
	base := filepath.Join(root, "test")
	filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && p != base {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if ext := path.Ext(d.Name()); ext == ".c" || ext == ".asm" {
			rel, _ := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// TestName is the name a discovered test file gets: its stem in upper case.
func TestName(file string) string {
	b := path.Base(file)
	return strings.ToUpper(strings.TrimSuffix(b, path.Ext(b)))
}

func v3Tests(root string, in map[string]any) ([]map[string]any, error) {
	defs := table(in, "tests")
	if err := checkKeys(defs, "tests", "[tests]"); err != nil {
		return nil, err
	}
	names, entries, err := keyedTables(in, "test")
	if err != nil {
		return nil, err
	}

	excluded := map[string]bool{}
	for _, f := range strs(defs, "exclude") {
		excluded[f] = true
	}
	claimed := map[string]bool{} // files some entry lists in its sources
	for _, n := range names {
		if err := checkKeys(entries[n], "test", fmt.Sprintf("[test.%s]", quoteKey(n))); err != nil {
			return nil, err
		}
		for _, s := range strs(entries[n], "sources") {
			claimed[s] = true
		}
	}
	found := map[string]string{} // name -> discovered file
	for _, f := range DiscoverTests(root) {
		if excluded[f] || claimed[f] {
			continue
		}
		n := TestName(f)
		if prev, dup := found[n]; dup {
			return nil, configErr("mbt.toml: %s and %s are both test %s -- exclude one in [tests] or give it an entry with its sources", prev, f, n)
		}
		if e, has := entries[n]; has {
			if _, explicit := e["sources"]; explicit {
				return nil, configErr("mbt.toml: %s would be test %s, but [test.%s] lists other sources -- add it to [tests] exclude", f, n, quoteKey(n))
			}
		}
		if err := checkMember(n, "test"); err != nil {
			return nil, configErr("mbt.toml: %s is discovered as a test, and %s -- add it to [tests] exclude or give it an entry", f, strings.TrimPrefix(err.Error(), "project.toml: "))
		}
		found[n] = f
	}

	all := map[string]map[string]any{}
	for n, f := range found {
		all[n] = map[string]any{"sources": []any{f}}
	}
	for _, n := range names {
		e := entries[n]
		if _, explicit := e["sources"]; !explicit {
			f, ok := found[n]
			if !ok {
				return nil, configErr("mbt.toml: [test.%s] has no sources, and no test/**/%s.c or .asm was found", quoteKey(n), strings.ToLower(n))
			}
			e = copyMap(e)
			e["sources"] = []any{f}
		}
		all[n] = e
	}

	order := make([]string, 0, len(all))
	for n := range all {
		order = append(order, n)
	}
	sort.Strings(order)
	var out []map[string]any
	for _, n := range order {
		e := copyMap(all[n])
		for _, a := range []string{"rent", "reus", "refr"} {
			if _, own := e[a]; own {
				continue
			}
			if d, ok := defs[a]; ok {
				e[a] = d
			} else if a != "refr" {
				return nil, configErr("mbt.toml: test %s has no '%s' -- declare it in [tests] or in [test.%s]", n, a, quoteKey(n))
			}
		}
		if p, ok := e["parm"].(map[string]any); ok {
			delete(e, "parm")
			for k, v := range p {
				if k != "batch" && k != "tso" {
					return nil, configErr("mbt.toml: [test.%s] parm has '%s' -- only batch and tso", quoteKey(n), k)
				}
				e["parm_"+k] = v
			}
		}
		if fx, ok := e["fixtures"].(map[string]any); ok {
			delete(e, "fixtures")
			dds := make([]string, 0, len(fx))
			for dd := range fx {
				dds = append(dds, dd)
			}
			sort.Strings(dds)
			var list []map[string]any
			for _, dd := range dds {
				list = append(list, map[string]any{"dd": dd, "members": fx[dd]})
			}
			e["fixture"] = list
		}
		m, err := v3Unit("test", n, e)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func copyMap(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// DeriveFMID returns the FMID of a version and the id it deletes: one FMID
// per minor (design §6.4).  deleteOK is false for x.0.0, which has no
// previous minor to derive from.
func DeriveFMID(prefix string, v version.Version) (fmid, del string, deleteOK bool, err error) {
	if v.Major > 9 || v.Minor > 9 || v.Patch > 9 {
		return "", "", false, fmt.Errorf("version %s has a component above 9 -- an FMID has one digit per component", v)
	}
	fmid = fmt.Sprintf("%s%d%d0", prefix, v.Major, v.Minor)
	if v.Minor == 0 {
		return fmid, "", false, nil
	}
	return fmid, fmt.Sprintf("%s%d%d0", prefix, v.Major, v.Minor-1), true, nil
}

// SMPDefaults are the conventional dataset names of a product.
func SMPDefaults(upper string) map[string]any {
	p4 := upper
	if len(p4) > 4 {
		p4 = p4[:4]
	}
	return map[string]any{
		"system": "Z038", "prereq": []any{}, "accept_fmid": true,
		"lklib": upper + "." + p4 + "LOAD", "target": upper + ".LINKLIB", "distlib": upper + ".A" + p4 + "LOD",
	}
}

func v3Distribution(in, d map[string]any, upper string, v version.Version) (map[string]any, string, error) {
	if err := checkKeys(d, "distribution", "[distribution]"); err != nil {
		return nil, "", err
	}
	out := map[string]any{}
	for _, k := range []string{"readme", "extra"} {
		if x, ok := d[k]; ok {
			out[k] = x
		}
	}
	if libs := table(d, "library"); libs != nil {
		dirs := make([]string, 0, len(libs))
		for dir := range libs {
			dirs = append(dirs, dir)
		}
		sort.Strings(dirs)
		var list []map[string]any
		for _, dir := range dirs {
			l, _ := libs[dir].(map[string]any)
			if err := checkKeys(l, "library", fmt.Sprintf("[distribution.library.%s]", quoteKey(dir))); err != nil {
				return nil, "", err
			}
			t := str(l, "target", upper+"."+strings.ToUpper(path.Base(dir)))
			list = append(list, map[string]any{"dir": dir, "target": t})
		}
		out["library"] = list
	}

	s := table(in, "smp")
	if s == nil {
		return nil, "", configErr("mbt.toml: [distribution] needs [smp] (at least prefix = \"T...\")")
	}
	if err := checkKeys(s, "smp", "[smp]"); err != nil {
		return nil, "", err
	}
	smp := SMPDefaults(upper)
	for k, x := range s {
		if k != "prefix" {
			smp[k] = x
		}
	}
	distErr := ""
	prefix := str(s, "prefix", "")
	_, explicitFMID := s["fmid"]
	if !explicitFMID {
		if prefix == "" {
			return nil, "", configErr("mbt.toml: [smp] needs prefix (or an explicit fmid)")
		}
		fmid, del, delOK, err := DeriveFMID(prefix, v)
		if err != nil {
			return nil, "", configErr("mbt.toml: [smp]: %v", err)
		}
		smp["fmid"] = fmid
		if v.Patch != 0 {
			distErr = fmt.Sprintf("version %s is a patch release: its FMID would be %s again, the minor's id, and a patch is a PTF, which mbt does not build yet -- set [smp] fmid (and delete) explicitly", v, fmid)
		}
		if _, explicit := s["delete"]; !explicit {
			if !delOK {
				return nil, "", configErr("mbt.toml: [smp]: %s.0.0 has no previous minor to delete -- set delete explicitly ([] for a first level)", fmt.Sprint(v.Major))
			}
			smp["delete"] = []any{del}
		}
	} else if _, explicit := s["delete"]; !explicit {
		smp["delete"] = []any{}
	}
	out["smp"] = smp
	return out, distErr, nil
}
