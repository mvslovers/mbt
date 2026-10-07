package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mvslovers/mbt/internal/project"
	"github.com/mvslovers/mbt/internal/version"
)

// Result is a converted project file and what the conversion changed beyond
// spelling.
type Result struct {
	Text    string
	Notices []string
}

type converter struct {
	root    string
	raw     map[string]any
	name    string // project name, upper case
	ver     version.Version
	out     []*block
	notices []string
	// tests
	testDefaults [2]bool // rent, reus
	implicit     map[string]bool
	excluded     []string
	testsBlock   *block
}

func (c *converter) notice(format string, a ...any) {
	c.notices = append(c.notices, fmt.Sprintf(format, a...))
}

// Convert reads root/project.toml and returns its mbt.toml.
func Convert(root string) (*Result, error) {
	data, err := os.ReadFile(filepath.Join(root, project.FileV2))
	if err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("project.toml: %v", err)
	}
	// the v2 rules must accept the file before it is converted
	if _, err := project.LoadV2(root, project.FileV2); err != nil {
		return nil, err
	}
	c := &converter{root: root, raw: raw, implicit: map[string]bool{}}
	proj, _ := raw["project"].(map[string]any)
	n, _ := proj["name"].(string)
	c.name = strings.ToUpper(n)
	vs, _ := proj["version"].(string)
	if c.ver, err = version.Parse(vs); err != nil {
		return nil, fmt.Errorf("project.toml: %v", err)
	}
	c.planTests()
	c.makefile()
	if rawTable(raw, "deploy") == nil && len(rawList(raw, "module")) > 0 {
		c.notice("no [deploy] target: mbt deploy goes to %s.DEV.LINKLIB by convention now (mbt 2 used {HLQ}.%s.{VRM}.LINKLIB)", c.name, c.name)
	}

	s := scan(string(data))
	counters := map[string]int{}
	var lastTest *block
	for _, b := range s.Blocks {
		idx := counters[b.Path]
		if b.Array {
			counters[b.Path]++
		}
		if b.Path == "test.fixture" {
			// a fixture belongs to the [[test]] above it
			c.fixture(lastTest, b, counters["test"]-1, idx-c.fixturesBefore(counters["test"]-1))
			continue
		}
		ob, err := c.block(b, idx)
		if err != nil {
			return nil, err
		}
		if b.Path == "test" {
			lastTest = ob
		}
	}
	if c.testsBlock == nil && len(c.excluded) > 0 {
		c.emit([]string{"", "# Files under test/ that are not tests (test/**/*.c and *.asm are, by default)."}, "[tests]", c.excludeLines())
	}
	text := c.render(s.Trailer)
	return &Result{Text: text, Notices: c.notices}, nil
}

// makefile names what a Makefile does beyond including mbt: that work has
// to move into mbt/init.lua and [tools] before the Makefile can go.
func (c *converter) makefile() {
	data, err := os.ReadFile(filepath.Join(c.root, "Makefile"))
	if err != nil {
		return
	}
	var targets []string
	extra := false
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "MBT_ROOT") || strings.HasPrefix(t, "include $(MBT_ROOT)") {
			continue
		}
		extra = true
		if m := makeTargetRE.FindStringSubmatch(l); m != nil && m[1] != ".PHONY" {
			targets = append(targets, m[1])
		}
	}
	if extra {
		c.notice("the Makefile does more than include mbt (targets: %s) -- move that into mbt/init.lua (mbt.task, mbt.command) and [tools] of mbt.toml before removing it; run-mvs/stop-mvs go away (CI starts its own MVS, a personal shortcut belongs in ~/.mbt/init.lua)", strings.Join(targets, ", "))
	}
}

var makeTargetRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:([^=]|$)`)

// fixturesBefore counts the fixtures of earlier tests (the scan numbers
// [[test.fixture]] across the whole file).
func (c *converter) fixturesBefore(test int) int {
	n := 0
	tests, _ := c.raw["test"].([]map[string]any)
	for i := 0; i < test && i < len(tests); i++ {
		if f, ok := tests[i]["fixture"].([]map[string]any); ok {
			n += len(f)
		}
	}
	return n
}

func (c *converter) emit(lead []string, header string, entries ...[]string) *block {
	b := &block{Lead: lead, Header: header}
	for _, e := range entries {
		b.Entries = append(b.Entries, &entry{Lines: e})
	}
	c.out = append(c.out, b)
	return b
}

func add(b *block, lead []string, lines []string) {
	b.Entries = append(b.Entries, &entry{Lead: lead, Lines: lines})
}

func verbatim(b *block, e *entry) { add(b, e.Lead, e.Lines) }

func rawTable(m map[string]any, k string) map[string]any {
	t, _ := m[k].(map[string]any)
	return t
}

func rawList(m map[string]any, k string) []map[string]any {
	l, _ := m[k].([]map[string]any)
	return l
}

func strList(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func tomlString(s string) string { return fmt.Sprintf("%q", s) }

func tomlList(l []string) string {
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = tomlString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func quoteKey(n string) string {
	for _, c := range n {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return tomlString(n)
		}
	}
	return n
}

func withComment(line, comment string) string {
	if comment == "" {
		return line
	}
	return line + "  " + comment
}

func (c *converter) block(b *block, idx int) (*block, error) {
	switch b.Path {
	case "":
		if len(b.Entries) > 0 {
			return nil, fmt.Errorf("project.toml: keys before the first table are not part of the v2 schema")
		}
		if len(b.Lead) > 0 {
			c.emit(b.Lead, "")
		}
		return nil, nil
	case "project":
		ob := c.emit(b.Lead, "[project]")
		for _, e := range b.Entries {
			switch e.Key {
			case "name", "version":
				verbatim(ob, e)
			case "type":
				add(ob, e.Lead, rekey(e, "kind"))
			default:
				c.notice("dropped [project] %s: not part of schema 3", e.Key)
			}
		}
		return ob, nil
	case "toolchain", "dependencies", "lib", "internal", "deploy", "distribution":
		ob := c.emit(b.Lead, b.Header)
		for _, e := range b.Entries {
			verbatim(ob, e)
		}
		return ob, nil
	case "host":
		ob := c.emit(b.Lead, "[build.host]")
		for _, e := range b.Entries {
			verbatim(ob, e)
		}
		return ob, nil
	case "test_deploy":
		ob := c.emit(b.Lead, "[deploy]")
		for _, e := range b.Entries {
			if e.Key == "target" {
				add(ob, e.Lead, rekey(e, "test_target"))
			}
		}
		if rawTable(c.raw, "deploy") != nil {
			return nil, fmt.Errorf("project.toml: [test_deploy] next to [deploy] -- move its target into [deploy] test_target by hand")
		}
		return ob, nil
	case "build":
		return c.build(b), nil
	case "release":
		var keep []*entry
		var dropped []string
		for _, e := range b.Entries {
			if e.Key != "version_files" {
				keep = append(keep, e)
				continue
			}
			var files []string
			for _, f := range strList(rawTable(c.raw, "release")["version_files"]) {
				if f != "VERSION" {
					files = append(files, f)
				}
			}
			if len(files) > 0 {
				keep = append(keep, &entry{Lead: e.Lead, Lines: []string{withComment("version_files = "+tomlList(files), inlineComment(e.Lines[len(e.Lines)-1]))}})
			} else {
				dropped = append(dropped, e.Lead...)
			}
		}
		if len(keep) == 0 {
			if hasComment(append(append([]string{}, b.Lead...), dropped...)) {
				c.notice("dropped the comments of [release]: it held only version_files = [\"VERSION\"], and VERSION is no longer read")
			}
			return nil, nil
		}
		ob := c.emit(b.Lead, "[release]")
		for _, e := range keep {
			add(ob, e.Lead, e.Lines)
		}
		return ob, nil
	case "distribution.smp":
		return c.smp(b)
	case "distribution.library":
		lib := rawList(rawTable(c.raw, "distribution"), "library")[idx]
		dir, _ := lib["dir"].(string)
		ob := c.emit(b.Lead, "[distribution.library."+quoteKey(dir)+"]")
		for _, e := range b.Entries {
			if e.Key == "dir" {
				ob.Lead = append(ob.Lead, e.Lead...)
				continue
			}
			verbatim(ob, e)
		}
		return ob, nil
	case "module":
		return c.unit(b, rawList(c.raw, "module")[idx], "module")
	case "test":
		return c.unit(b, rawList(c.raw, "test")[idx], "test")
	}
	return nil, fmt.Errorf("project.toml: [%s] has no place in schema 3 -- convert it by hand", b.Path)
}

// build splits leading "-I", dir pairs of cflags into include.
func (c *converter) build(b *block) *block {
	ob := c.emit(b.Lead, "[build]")
	for _, e := range b.Entries {
		if e.Key != "cflags" {
			verbatim(ob, e)
			continue
		}
		flags := strList(rawTable(c.raw, "build")["cflags"])
		var inc []string
		i := 0
		for i+1 < len(flags) && flags[i] == "-I" {
			inc = append(inc, flags[i+1])
			i += 2
		}
		if len(inc) == 0 || len(e.Lines) > 1 && hasComment(e.Lines[1:]) {
			verbatim(ob, e)
			continue
		}
		comment := inlineComment(e.Lines[len(e.Lines)-1])
		add(ob, e.Lead, []string{"include = " + tomlList(inc)})
		if rest := flags[i:]; len(rest) > 0 {
			add(ob, nil, []string{withComment("cflags = "+tomlList(rest), comment)})
		} else if comment != "" {
			ob.Entries[len(ob.Entries)-1].Lines[0] += "  " + comment
		}
	}
	return ob
}

func (c *converter) smp(b *block) (*block, error) {
	smp := rawTable(rawTable(c.raw, "distribution"), "smp")
	fmid, _ := smp["fmid"].(string)
	del := strList(smp["delete"])
	derivable := false
	prefix := ""
	if len(fmid) == 7 {
		prefix = fmid[:4]
		f, d, dok, err := project.DeriveFMID(prefix, c.ver)
		derivable = err == nil && f == fmid && c.ver.Patch == 0
		if derivable && dok && !reflect.DeepEqual(del, []string{d}) {
			derivable = false
		}
		if derivable && !dok {
			derivable = false // x.0.0: delete stays explicit
		}
	}
	ob := c.emit(b.Lead, "[smp]")
	defaults := project.SMPDefaults(c.name)
	for _, e := range b.Entries {
		switch e.Key {
		case "fmid":
			if derivable {
				lead := e.Lead
				if ic := inlineComment(e.Lines[len(e.Lines)-1]); ic != "" {
					lead = append(append([]string{}, lead...), "# "+strings.TrimSpace(strings.TrimPrefix(ic, "#")))
				}
				add(ob, lead, []string{fmt.Sprintf("prefix = %s  # FMID and delete follow from the version", tomlString(prefix))})
				continue
			}
			verbatim(ob, e)
		case "delete":
			if derivable {
				if hasComment(e.Lead) || inlineComment(e.Lines[len(e.Lines)-1]) != "" {
					c.notice("dropped the comment on [distribution.smp] delete: the id is derived now")
				}
				continue
			}
			verbatim(ob, e)
		default:
			def, known := defaults[e.Key]
			if known && reflect.DeepEqual(def, normalize(smp[e.Key])) && !hasComment(e.Lead) && inlineComment(e.Lines[len(e.Lines)-1]) == "" {
				continue // the convention, said again
			}
			verbatim(ob, e)
		}
	}
	return ob, nil
}

func normalize(v any) any {
	if l, ok := v.([]any); ok && len(l) == 0 {
		return []any{}
	}
	return v
}

// planTests settles the [tests] defaults, which tests need no entry of their
// own, and which discovered files are not tests.
func (c *converter) planTests() {
	tests := rawList(c.raw, "test")
	count := map[[2]bool]int{}
	for _, t := range tests {
		r, _ := t["rent"].(bool)
		u, _ := t["reus"].(bool)
		count[[2]bool{r, u}]++
	}
	best, bestN := [2]bool{}, -1
	for _, k := range [][2]bool{{false, false}, {true, true}, {false, true}, {true, false}} {
		if count[k] > bestN {
			best, bestN = k, count[k]
		}
	}
	c.testDefaults = best

	discovered := project.DiscoverTests(c.root)
	isDiscovered := map[string]bool{}
	for _, f := range discovered {
		isDiscovered[f] = true
	}
	claimed := map[string]bool{}
	for _, t := range tests {
		n, _ := t["name"].(string)
		srcs := strList(t["sources"])
		if len(srcs) == 1 && isDiscovered[srcs[0]] && project.TestName(srcs[0]) == n && len(strList(t["exclude"])) == 0 {
			c.implicit[n] = true
			continue
		}
		for _, s := range srcs {
			claimed[s] = true
		}
	}
	for _, f := range discovered {
		if claimed[f] {
			continue
		}
		n := project.TestName(f)
		if c.implicit[n] {
			if t := testNamed(tests, n); t != nil && strList(t["sources"])[0] == f {
				continue
			}
		}
		c.excluded = append(c.excluded, f)
	}
}

func (c *converter) excludeLines() []string {
	ex := []string{"exclude = ["}
	for _, f := range c.excluded {
		ex = append(ex, "  "+tomlString(f)+",")
	}
	return append(ex, "]")
}

func testNamed(tests []map[string]any, n string) map[string]any {
	for _, t := range tests {
		if t["name"] == n {
			return t
		}
	}
	return nil
}

func (c *converter) unit(b *block, raw map[string]any, kind string) (*block, error) {
	name, _ := raw["name"].(string)
	lead := b.Lead
	if kind == "test" && c.testsBlock == nil {
		// [tests] goes where the tests begin, under their banner
		var lines [][]string
		lines = append(lines, []string{fmt.Sprintf("rent = %v", c.testDefaults[0])}, []string{fmt.Sprintf("reus = %v", c.testDefaults[1])})
		if len(c.excluded) > 0 {
			lines = append(lines, c.excludeLines())
		}
		c.testsBlock = c.emit(append(append([]string{}, lead...), "# Defaults for every test; test/**/*.c and *.asm are tests unless excluded."), "[tests]", lines...)
		lead = []string{""}
	}
	ob := &block{Lead: lead, Header: "[" + kind + "." + quoteKey(name) + "]"}
	_, hasRent := raw["rent"]
	_, hasReus := raw["reus"]
	_, hasNorent := raw["norent"]
	_, hasNoreus := raw["noreus"]
	for _, e := range b.Entries {
		ic := inlineComment(e.Lines[len(e.Lines)-1])
		commented := hasComment(e.Lead) || ic != ""
		switch e.Key {
		case "name":
			ob.Lead = append(ob.Lead, e.Lead...)
		case "startup":
			if s, _ := raw["startup"].(string); s == "crt0" || s == "crt1" {
				if commented {
					c.notice("%s %s: dropped startup = %q and its comment -- the CRT comes out of libc.a", kind, name, s)
				}
				continue
			}
			verbatim(ob, e)
		case "norent", "noreus":
			a := strings.TrimPrefix(e.Key, "no")
			if v, _ := raw[e.Key].(bool); v {
				add(ob, e.Lead, rekey(&entry{Key: e.Key, Lines: []string{strings.Replace(e.Lines[0], "true", "false", 1)}}, a))
			}
		case "sources":
			if kind == "test" && c.implicit[name] && !commented {
				continue
			}
			verbatim(ob, e)
		case "rent", "reus":
			if kind == "test" {
				v, _ := raw[e.Key].(bool)
				def := c.testDefaults[0]
				if e.Key == "reus" {
					def = c.testDefaults[1]
				}
				if v == def && !commented {
					continue
				}
			}
			verbatim(ob, e)
		case "parm_batch":
			add(ob, e.Lead, rekey(e, "parm.batch"))
		case "parm_tso":
			add(ob, e.Lead, rekey(e, "parm.tso"))
		default:
			verbatim(ob, e)
		}
	}
	for _, a := range []string{"rent", "reus"} {
		declared := (a == "rent" && (hasRent || hasNorent)) || (a == "reus" && (hasReus || hasNoreus))
		if declared {
			continue
		}
		if kind == "module" {
			add(ob, nil, []string{a + " = false  # undeclared in project.toml: ld370's default"})
			c.notice("module %s: '%s' was not declared; written as false, which is what ld370 did without it -- check it", name, a)
		} else if def := c.testDefaults[map[string]int{"rent": 0, "reus": 1}[a]]; def {
			add(ob, nil, []string{a + " = false  # undeclared in project.toml: ld370's default"})
		}
	}
	// a discovered test with nothing to say gets no entry; render skips it
	// unless a fixture fills it
	c.out = append(c.out, ob)
	return ob, nil
}

func (c *converter) fixture(test *block, b *block, ti, fi int) {
	t := rawList(c.raw, "test")[ti]
	fx := rawList(t, "fixture")[fi]
	dd, _ := fx["dd"].(string)
	lead := append([]string{}, b.Lead...)
	for _, e := range b.Entries {
		switch e.Key {
		case "dd":
			lead = append(lead, e.Lead...)
		case "members":
			add(test, append(lead, e.Lead...), rekey(e, "fixtures."+quoteKey(dd)))
			lead = nil
		}
	}
}

func (c *converter) render(trailer []string) string {
	var sb strings.Builder
	w := func(lines []string) {
		for _, l := range lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	// schema = 3 goes first, after a comment that heads the whole file
	first := 0
	if len(c.out) > 0 && c.out[0].Header == "" {
		w(c.out[0].Lead)
		first = 1
		sb.WriteString("schema = 3\n")
	} else if len(c.out) > 0 {
		lead := c.out[0].Lead
		cut := 0
		for i, l := range lead {
			if strings.TrimSpace(l) == "" {
				cut = i + 1
			}
		}
		w(lead[:cut])
		sb.WriteString("schema = 3\n\n")
		c.out[0].Lead = lead[cut:]
	}
	for _, b := range c.out[first:] {
		if strings.HasPrefix(b.Header, "[test.") && len(b.Entries) == 0 && !hasComment(b.Lead) {
			continue
		}
		w(b.Lead)
		if b.Header != "" {
			sb.WriteString(b.Header + "\n")
		}
		for _, e := range b.Entries {
			w(e.Lead)
			w(e.Lines)
		}
	}
	w(trailer)
	return collapseBlank(sb.String())
}

// collapseBlank keeps at most one blank line in a row.
func collapseBlank(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i, l := range lines {
		if strings.TrimSpace(l) == "" && i > 0 && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimLeft(strings.Join(out, "\n"), "\n")
}

// Check loads the converted file next to the original and compares the two
// models; it returns every difference that is not an intended one.
func Check(root, text string) ([]string, error) {
	tmp := filepath.Join(".mbt", "migrate-check.toml")
	if err := os.MkdirAll(filepath.Join(root, ".mbt"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, tmp), []byte(text), 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(filepath.Join(root, tmp))
	v3, err := project.LoadV3(root, tmp)
	if err != nil {
		kept := filepath.Join(".mbt", "migrate-failed.toml")
		os.WriteFile(filepath.Join(root, kept), []byte(text), 0o644)
		return nil, fmt.Errorf("the converted file does not load (kept as %s): %v", kept, err)
	}
	v2, err := project.LoadV2(root, project.FileV2)
	if err != nil {
		return nil, err
	}
	return compare(v2, v3), nil
}

func compare(a, b *project.Project) []string {
	var d []string
	diff := func(what string, x, y any) {
		if !reflect.DeepEqual(x, y) {
			d = append(d, fmt.Sprintf("%s: %v -> %v", what, x, y))
		}
	}
	diff("name", a.Name, b.Name)
	diff("version", a.Version, b.Version)
	diff("type", a.Type, b.Type)
	diff("cflags", a.CFlags, b.CFlags)
	diff("asflags", a.ASFlags, b.ASFlags)
	diff("lib", a.Lib, b.Lib)
	diff("internal", a.Internal, b.Internal)
	units := func(kind string, x, y []*project.Unit, rx, ry []map[string]any) {
		sortUnits(x)
		sortUnits(y)
		if len(x) != len(y) {
			d = append(d, fmt.Sprintf("%s count: %d -> %d", kind, len(x), len(y)))
			return
		}
		for i := range x {
			u, v := *x[i], *y[i]
			u.Raw, v.Raw = nil, nil
			u.Startfile, v.Startfile = "", "" // crt1 is gone: read only without the CRT in libc.a
			u.Attrs, v.Attrs = effective(u.Attrs), effective(v.Attrs)
			diff(kind+" "+u.Name, u, v)
		}
		props := func(l []map[string]any) map[string]map[string]any {
			m := map[string]map[string]any{}
			for _, t := range l {
				n, _ := t["name"].(string)
				p := map[string]any{}
				for _, k := range []string{"host", "mvs", "parm", "parm_batch", "parm_tso"} {
					if v, ok := t[k]; ok {
						p[k] = v
					}
				}
				var fx []string
				for _, f := range rawList(t, "fixture") {
					fx = append(fx, fmt.Sprint(f["dd"], strList(f["members"])))
				}
				sort.Strings(fx)
				if fx != nil {
					p["fixture"] = fx
				}
				m[n] = p
			}
			return m
		}
		diff(kind+" properties", props(rx), props(ry))
	}
	units("module", a.Modules, b.Modules, rawList(a.Raw, "module"), rawList(b.Raw, "module"))
	units("test", a.Tests, b.Tests, rawList(a.Raw, "test"), rawList(b.Raw, "test"))
	// host-only tests are not in Tests
	diff("host-only tests", hostOnly(a.Raw), hostOnly(b.Raw))
	if rawTable(a.Raw, "deploy") == nil && len(a.Modules) > 0 {
		// intended: no target in v2 means the DEV library by convention now
		a = shallowWith(a, "deploy", map[string]any{"target": strings.ToUpper(a.Name) + ".DEV.LINKLIB"})
	}
	for _, k := range []string{"toolchain", "dependencies", "host", "deploy", "test_deploy"} {
		diff("["+k+"]", fmt.Sprint(a.Raw[k]), fmt.Sprint(b.Raw[k]))
	}
	diff("[distribution]", distNorm(a), distNorm(b))
	var vf []string
	for _, f := range strList(rawTable(a.Raw, "release")["version_files"]) {
		if f != "VERSION" {
			vf = append(vf, f)
		}
	}
	diff("[release] version_files", vf, strList(rawTable(b.Raw, "release")["version_files"]))
	return d
}

// effective drops --norent and --noreus: ld370 marks a module neither RENT
// nor REUS unless asked, so they change nothing (ld370 --help).  Schema 3
// writes them for every module that is not RENT/REUS; v2 often left them out.
func effective(attrs []string) []string {
	var out []string
	for _, a := range attrs {
		if a != "--norent" && a != "--noreus" {
			out = append(out, a)
		}
	}
	return out
}

func shallowWith(p *project.Project, k string, v any) *project.Project {
	q := *p
	q.Raw = map[string]any{}
	for kk, vv := range p.Raw {
		q.Raw[kk] = vv
	}
	q.Raw[k] = v
	return &q
}

func sortUnits(l []*project.Unit) {
	sort.Slice(l, func(i, j int) bool { return l[i].Name < l[j].Name })
}

func hostOnly(raw map[string]any) []string {
	var n []string
	for _, t := range rawList(raw, "test") {
		if v, ok := t["mvs"].(bool); ok && !v {
			n = append(n, fmt.Sprint(t["name"], strList(t["sources"])))
		}
	}
	sort.Strings(n)
	return n
}

func distNorm(p *project.Project) string {
	d := rawTable(p.Raw, "distribution")
	if d == nil {
		return ""
	}
	smp := project.SMPDefaults(strings.ToUpper(p.Name))
	for k, v := range rawTable(d, "smp") {
		smp[k] = normalize(v)
	}
	var libs []string
	for _, l := range rawList(d, "library") {
		libs = append(libs, fmt.Sprint(l["dir"], "=", l["target"]))
	}
	sort.Strings(libs)
	return fmt.Sprint(d["readme"], d["extra"], libs, smp)
}
