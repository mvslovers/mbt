package project

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mvslovers/mbt/internal/plugins"
	"github.com/mvslovers/mbt/internal/tn3270"
	"github.com/mvslovers/mbt/internal/tools"
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
	"":             {"schema", "project", "toolchain", "dependencies", "build", "lib", "internal", "module", "tests", "test", "deploy", "distribution", "smp", "release", "tools", "plugins", "tso"},
	"project":      {"name", "version", "kind"},
	"toolchain":    {"cc370", "libc370", "mbt"},
	"build":        {"include", "cflags", "asflags", "cobflags", "pliflags", "max_rc", "host"},
	"build.host":   {"cc", "cflags", "sources", "replace"},
	"lib":          {"name", "sources", "headers"},
	"internal":     {"sources", "exclude"},
	"module":       {"sources", "exclude", "rent", "reus", "refr", "entry", "startup", "dep_startup", "ac", "aliases"},
	"tests":        {"rent", "reus", "refr", "exclude"},
	"test":         {"sources", "exclude", "rent", "reus", "refr", "entry", "startup", "dep_startup", "ac", "host", "mvs", "parm", "fixtures"},
	"deploy":       {"target", "test_target"},
	"distribution": {"readme", "extra", "library"},
	"library":      {"target", "element", "distlib", "stage"},
	"smp":          {"prefix", "fmid", "delete", "system", "prereq", "accept_fmid", "procedure", "lklib", "target", "distlib"},
	"release":      {"version_files"},
	"tso":          {"fixtures", "size"},
}

var qualRE = regexp.MustCompile(`^[A-Z@#$][A-Z0-9@#$]{0,7}$`)

// strList is a TOML list of strings.
func strList(v any) ([]string, bool) {
	l, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l))
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
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

// v3Plain are the top-level tables that are one table each, [name]; v3Keyed
// hold one table per entry, [name.ENTRY].
var (
	v3Plain = []string{"project", "toolchain", "dependencies", "build", "lib", "internal", "tests", "deploy", "distribution", "smp", "release", "tools", "plugins"}
	v3Keyed = map[string]string{"module": "NAME", "test": "NAME", "tso": "NAME"}
)

// tableAt returns parent[key] as a table, nil when it is not there. path is
// the key as written in the file ("distribution.library"); entry is the name
// of the entries of a table that holds one table each ("DIR"), "" for a
// plain table. An array of tables ([[path]]) or a value that is no table is
// an error, never a table that is silently left out.
func tableAt(parent map[string]any, key, path, entry string) (map[string]any, error) {
	v, ok := parent[key]
	if !ok {
		return nil, nil
	}
	want := "[" + path + "]"
	if entry != "" {
		want = "[" + path + "." + entry + "]"
	}
	switch x := v.(type) {
	case map[string]any:
		return x, nil
	case []map[string]any, []any:
		if entry != "" {
			return nil, configErr("mbt.toml: [[%s]] is the v2 spelling -- write one table per entry, %s (mbt migrate converts a project.toml)", path, want)
		}
		return nil, configErr("mbt.toml: [[%s]] is an array of tables -- write one table, %s", path, want)
	}
	return nil, configErr("mbt.toml: %s is %s, not a table -- write %s", path, kindOf(v), want)
}

// kindOf names the TOML type of a decoded value, for a message.
func kindOf(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case int64:
		return "an integer"
	case float64:
		return "a float"
	case bool:
		return "a boolean"
	case []any, []map[string]any:
		return "an array"
	case map[string]any:
		return "a table"
	}
	return "a value of another type"
}

// checkForms refuses every table written in a form other than the one the
// schema has, before anything reads it: the readers below skip what is not
// the table they look for.
func checkForms(in map[string]any) error {
	for _, k := range v3Plain {
		if _, err := tableAt(in, k, k, ""); err != nil {
			return err
		}
	}
	for k, entry := range v3Keyed {
		if _, err := tableAt(in, k, k, entry); err != nil {
			return err
		}
	}
	if b, _ := in["build"].(map[string]any); b != nil {
		if _, err := tableAt(b, "host", "build.host", ""); err != nil {
			return err
		}
	}
	if d, _ := in["distribution"].(map[string]any); d != nil {
		if _, err := tableAt(d, "library", "distribution.library", "DIR"); err != nil {
			return err
		}
	}
	if deps, _ := in["dependencies"].(map[string]any); deps != nil {
		for k, v := range deps {
			if _, ok := v.(string); !ok {
				return configErr("mbt.toml: [dependencies] %q is %s, not a version range -- write %q = \"^1\"", k, kindOf(v), k)
			}
		}
	}
	return nil
}

func translateV3(root string, in map[string]any) (map[string]any, string, error) {
	if err := checkKeys(in, "", ""); err != nil {
		return nil, "", err
	}
	if s, ok := in["schema"].(int64); !ok || s != 3 {
		return nil, "", configErr("mbt.toml: 'schema = 3' is required")
	}
	if err := checkForms(in); err != nil {
		return nil, "", err
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
	for _, dir := range IncludeDirs(root, strs(build, "include")) {
		cflags = append(cflags, "-I", dir)
	}
	for _, f := range strs(build, "cflags") {
		cflags = append(cflags, f)
	}
	b := map[string]any{}
	if len(cflags) > 0 {
		b["cflags"] = cflags
	}
	if a, ok := build["cobflags"]; ok {
		b["cobflags"] = a
	}
	if a, ok := build["pliflags"]; ok {
		b["pliflags"] = a
	}
	if a, ok := build["asflags"]; ok {
		b["asflags"] = a
	}
	if a, ok := build["max_rc"]; ok {
		b["max_rc"] = a
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
		if err := derivedDSN("[deploy] target", target); err != nil {
			return nil, "", err
		}
	}
	if target != "" {
		out["deploy"] = map[string]any{"target": target}
	}
	if tt := str(dep, "test_target", ""); tt != "" {
		out["test_deploy"] = map[string]any{"target": tt}
	}

	distErr := ""
	if d := table(in, "distribution"); d != nil {
		dist, msg, err := v3Distribution(root, in, d, upper, v)
		if err != nil {
			return nil, "", err
		}
		out["distribution"], distErr = dist, msg
	} else if table(in, "smp") != nil {
		return nil, "", configErr("mbt.toml: [smp] without [distribution] -- nothing would be packaged")
	}

	// [tso.NAME] fixtures = { KEY = [files] }: data sets a TSO test needs
	// (mbt test --tso, mbt#243); NAME is the test file's name, KEY the last
	// qualifier of its data set. size = "COLSxROWS": the terminal for this
	// test, ahead of the target's [tn3270] size
	if ts := table(in, "tso"); ts != nil {
		tso := map[string]any{}
		for n, v := range ts {
			e, ok := v.(map[string]any)
			if !ok {
				return nil, "", configErr("mbt.toml: [tso.%s] must be a table", quoteKey(n))
			}
			if err := checkKeys(e, "tso", "[tso."+quoteKey(n)+"]"); err != nil {
				return nil, "", err
			}
			if !qualRE.MatchString(strings.ToUpper(n)) {
				return nil, "", configErr("mbt.toml: [tso.%s]: the name of a TSO test is a data set qualifier (1-8 characters, a letter first) -- its file is test/tso/<name>.lua", quoteKey(n))
			}
			fx := map[string]any{}
			if raw, ok := e["fixtures"]; ok {
				fm, ok := raw.(map[string]any)
				if !ok {
					return nil, "", configErr("mbt.toml: [tso.%s] fixtures must be a table: { KEY = [\"file\", ...] }", quoteKey(n))
				}
				for k, l := range fm {
					if !qualRE.MatchString(strings.ToUpper(k)) {
						return nil, "", configErr("mbt.toml: [tso.%s] fixtures: %q is the last qualifier of a data set (1-8 characters, a letter first)", quoteKey(n), k)
					}
					files, ok := strList(l)
					if !ok || len(files) == 0 {
						return nil, "", configErr("mbt.toml: [tso.%s] fixtures.%s must be a non-empty list of files", quoteKey(n), k)
					}
					fx[strings.ToUpper(k)] = files
				}
			}
			up := strings.ToUpper(n)
			if _, dup := tso[up]; dup {
				return nil, "", configErr("mbt.toml: [tso.%s] is declared twice (names are not case-sensitive)", quoteKey(n))
			}
			entry := map[string]any{"fixtures": fx}
			if raw, ok := e["size"]; ok {
				z, isStr := raw.(string)
				if !isStr {
					return nil, "", configErr("mbt.toml: [tso.%s] size must be a string, COLSxROWS (e.g. \"80x43\")", quoteKey(n))
				}
				if _, err := tn3270.ParseSize(z); err != nil {
					return nil, "", configErr("mbt.toml: [tso.%s]: %v", quoteKey(n), err)
				}
				entry["size"] = z
			}
			tso[up] = entry
		}
		out["tso"] = tso
	}

	for _, k := range []string{"tools", "plugins"} {
		if t := table(in, k); t != nil {
			out[k] = t
		}
	}
	// their keys are checked here, not only where a tool or plugin is needed,
	// so that every command refuses a misspelt one
	if _, err := tools.Declared(out); err != nil {
		return nil, "", configErr("mbt.toml: %v", err)
	}
	if _, err := plugins.Declared(out); err != nil {
		return nil, "", configErr("mbt.toml: %v", err)
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
	t, err := tableAt(in, kind, kind, "NAME")
	if t == nil || err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(t))
	entries := map[string]map[string]any{}
	for n, v := range t {
		e, ok := v.(map[string]any)
		if !ok {
			return nil, nil, configErr("mbt.toml: [%s] holds one table per entry, [%s.NAME]; '%s' is %s", kind, kind, n, kindOf(v))
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
		if _, err := tableAt(entries[n], "fixtures", "test."+quoteKey(n)+".fixtures", ""); err != nil {
			return nil, err
		}
		switch pv := entries[n]["parm"].(type) {
		case nil, string, map[string]any:
		default:
			return nil, configErr("mbt.toml: test.%s.parm is %s -- write a string, or a table [test.%s.parm] with batch and tso", quoteKey(n), kindOf(pv), quoteKey(n))
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

func v3Distribution(root string, in, d map[string]any, upper string, v version.Version) (map[string]any, string, error) {
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
			l, ok := libs[dir].(map[string]any)
			if !ok {
				return nil, "", configErr("mbt.toml: [distribution.library] holds one table per directory, [distribution.library.DIR]; '%s' is %s", dir, kindOf(libs[dir]))
			}
			if err := checkKeys(l, "library", fmt.Sprintf("[distribution.library.%s]", quoteKey(dir))); err != nil {
				return nil, "", err
			}
			t := str(l, "target", "")
			if t == "" {
				t = upper + "." + strings.ToUpper(path.Base(dir))
				if err := derivedDSN(fmt.Sprintf("[distribution.library.%s] target", quoteKey(dir)), t); err != nil {
					return nil, "", err
				}
			}
			entry := map[string]any{"dir": dir, "target": t}
			for _, k := range []string{"element", "distlib", "stage"} {
				if x, ok := l[k]; ok {
					entry[k] = x
				}
			}
			list = append(list, entry)
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
	for _, k := range []string{"lklib", "target", "distlib"} {
		if _, set := s[k]; !set {
			if err := derivedDSN("[smp] "+k, smp[k].(string)); err != nil {
				return nil, "", err
			}
		}
	}
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
		if v.IsPre() {
			distErr = fmt.Sprintf("version %s is a prerelease: the derived FMID %s is the release's, and an install of this build would spend it and block the release and patch releases of the previous minor (NOT SEL) -- set [smp] fmid and delete explicitly, to a throwaway id (Reference, SMP chapter)", v, fmid)
		} else if v.Patch != 0 {
			distErr = fmt.Sprintf("version %s is a patch release: its FMID would be %s again, the minor's id, and a patch is a PTF, which mbt does not build yet -- set [smp] fmid (and delete) explicitly", v, fmid)
		}
		if _, explicit := s["delete"]; !explicit {
			if !delOK {
				return nil, "", configErr("mbt.toml: [smp]: %s.0.0 has no previous minor to delete -- set delete explicitly ([] for a first level)", fmt.Sprint(v.Major))
			}
			smp["delete"] = []any{del}
			// a patch of the previous minor released under its own FMID
			// (explicit, until mbt builds PTFs) owns the modules now: deleting
			// the minor's id would leave it in place, and SMP would install
			// nothing at RC 0 (the "NOT SEL" wall)
			if v.Patch == 0 && distErr == "" {
				if pt := ReleasedPatches(root, v.Major, v.Minor-1); len(pt) > 0 {
					distErr = fmt.Sprintf("%s was released after %d.%d.0 (tag %s): if it shipped under an FMID of its own, %s.0 must delete that one, not the derived %s -- set [smp] delete explicitly", strings.TrimPrefix(pt[len(pt)-1], "v"), v.Major, v.Minor-1, pt[len(pt)-1], fmt.Sprintf("%d.%d", v.Major, v.Minor), del)
				}
			}
		}
	} else if _, explicit := s["delete"]; !explicit {
		// an explicit fmid says nothing about its predecessor; an empty delete
		// is right only for a first level -- anywhere else the new SYSMOD does
		// not own the modules and SMP installs nothing at RC 0 (NOT SEL)
		return nil, "", configErr("mbt.toml: [smp] fmid is explicit, so delete must be too: the FMID this one replaces, or delete = [] for a product's first level")
	}
	out["smp"] = smp
	return out, distErr, nil
}

// IncludeDirs is the include path of an mbt.toml: the project's include/
// first, by convention, when it exists and the list does not name it already;
// then [build] include, which adds to it.
func IncludeDirs(root string, listed []string) []string {
	var out []string
	named := false
	for _, d := range listed {
		if filepath.Clean(d) == DefaultInclude {
			named = true
		}
	}
	if st, err := os.Stat(filepath.Join(root, DefaultInclude)); err == nil && st.IsDir() && !named {
		out = append(out, DefaultInclude)
	}
	return append(out, listed...)
}

// DefaultInclude is the include directory every C project gets.
const DefaultInclude = "include"

// DefaultCFlags lead every cc370 compile line, before the project's own.
var DefaultCFlags = append([]string{"-O1"}, DefaultWarnings...)

// DefaultWarnings lead the host build of an mbt.toml project's tests too.
var DefaultWarnings = []string{"-Wall", "-Wextra", "-Werror"}

// DefaultStd is the C dialect of an mbt.toml project, on the cc370 line and
// the host build alike: the ecosystem's C99 (gnu99). cc370 by itself compiles
// gnu89. A -std in the project's cflags comes later on the line and wins.
const DefaultStd = "-std=gnu99"

// EffectiveCFlags is a project's cflags as the compiler sees them, for
// comparing two of them: the -I dirs first, in their order (their position
// among other flags changes nothing, their order among themselves does), a
// repeated -I dropped; then the rest, without -Wall/-Wextra/-Werror, which
// DefaultCFlags set already -- unless a -Wno... comes before them, after
// which a repeat re-enables something.
func EffectiveCFlags(f []string) []string {
	var inc, rest []string
	seen := map[string]bool{}
	wno := false
	for i := 0; i < len(f); i++ {
		switch x := f[i]; {
		case x == "-I" && i+1 < len(f):
			i++
			if d := filepath.Clean(f[i]); !seen[d] {
				seen[d] = true
				inc = append(inc, "-I", f[i])
			}
		case strings.HasPrefix(x, "-Wno"):
			wno = true
			rest = append(rest, x)
		case (x == "-Wall" || x == "-Wextra" || x == "-Werror") && !wno:
		default:
			rest = append(rest, x)
		}
	}
	return append(inc, rest...)
}

// derivedDSN refuses a data set name mbt derived from the project name when
// it is none: a qualifier is 1-8 characters, A-Z 0-9 @ # $ and -, not
// starting with a digit or -. A started task names these libraries in its
// STEPLIB, so mbt does not shorten one quietly; the project names it.
func derivedDSN(key, dsn string) error {
	for _, q := range strings.Split(dsn, ".") {
		ok := len(q) >= 1 && len(q) <= 8 && !strings.ContainsAny(q[:1], "0123456789-")
		for _, r := range q {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@#$-", r)) {
				ok = false
			}
		}
		if !ok {
			why := fmt.Sprintf("qualifier %s is not 1-8 characters of A-Z 0-9 @ # $ -", q)
			if len(q) > 8 {
				why = fmt.Sprintf("qualifier %s is %d characters, at most 8", q, len(q))
			}
			return configErr("mbt.toml: %s: the default %s is not a valid dataset name (%s) -- set %s", key, dsn, why, key)
		}
	}
	return nil
}

// ReleasedPatches lists the release tags vX.Y.Z (Z > 0, no prerelease) of
// the minor X.Y in root's git repository, oldest first. A checkout without
// tags (a shallow CI clone) lists none, so the check bites where releases
// are made: mbt release and mbt package run locally first.
var ReleasedPatches = func(root string, major, minor int) []string {
	out, err := exec.Command("git", "-C", root, "tag", "-l", fmt.Sprintf("v%d.%d.*", major, minor)).Output()
	if err != nil {
		return nil
	}
	var tags []string
	for _, t := range strings.Fields(string(out)) {
		v, err := version.Parse(strings.TrimPrefix(t, "v"))
		if err == nil && v.Patch > 0 && v.Pre == "" {
			tags = append(tags, t)
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		a, _ := version.Parse(strings.TrimPrefix(tags[i], "v"))
		b, _ := version.Parse(strings.TrimPrefix(tags[j], "v"))
		return version.Compare(a, b) < 0
	})
	return tags
}
