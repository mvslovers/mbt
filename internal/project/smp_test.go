package project

import (
	"strings"
	"testing"
)

const smpModuleBase = `schema = 3
[project]
name = "abc"
version = "1.0.0"
[module.ABCMOD]
sources = ["src/abc.c"]
rent = false
reus = false
[module.IGX00099]
entry = "IGX00099"
startup = false
sources = ["asm/igx.asm"]
rent = true
reus = true
refr = true
`

func smpModuleTree(t *testing.T, extra string) string {
	return writeTree(t, map[string]string{
		"mbt.toml": smpModuleBase + extra, "src/abc.c": "", "asm/igx.asm": "", "asm/two.asm": "",
	})
}

func TestModuleSMP(t *testing.T) {
	root := smpModuleTree(t, `[module.IGX00099.smp]
syslib = "lpalib"
distlib = "AOST4"
source = true
`)
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var m *Unit
	for _, u := range p.Modules {
		if u.Name == "IGX00099" {
			m = u
		}
	}
	if m == nil || m.SMP == nil {
		t.Fatalf("IGX00099 has no smp: %+v", m)
	}
	if m.SMP.Syslib != "LPALIB" || m.SMP.Distlib != "AOST4" || !m.SMP.Source || m.SMP.File != "asm/igx.asm" {
		t.Errorf("smp = %+v", *m.SMP)
	}
	if got := m.LinkParm(); got != "MAP,LIST,NCAL,RENT,REUS,REFR" {
		t.Errorf("derived PARM = %s", got)
	}
	var load []string
	for _, u := range p.ShippedAsLoad() {
		load = append(load, u.Name)
	}
	if strings.Join(load, ",") != "ABCMOD" {
		t.Errorf("the load XMIT would carry %v; a source module stays out", load)
	}
	if len(p.Shipped()) != 2 {
		t.Errorf("both modules are shipped, the source one as source: %d", len(p.Shipped()))
	}
}

func TestModuleSMPParm(t *testing.T) {
	p, err := Load(smpModuleTree(t, `[module.IGX00099.smp]
syslib = "LPALIB"
distlib = "AOST4"
source = true
parm = "map,list,ncal,rent"
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range p.Modules {
		if u.Name == "IGX00099" && u.LinkParm() != "MAP,LIST,NCAL,RENT" {
			t.Errorf("PARM = %s", u.LinkParm())
		}
	}
}

func TestModuleSMPRejects(t *testing.T) {
	for name, c := range map[string]struct{ extra, want string }{
		"no source":     {"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\n", "source = true is required"},
		"source false":  {"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\nsource = false\n", "source = true is required"},
		"no syslib":     {"[module.IGX00099.smp]\ndistlib = \"AOST4\"\nsource = true\n", "'syslib' is required"},
		"no distlib":    {"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\nsource = true\n", "'distlib' is required"},
		"unknown key":   {"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\nsource = true\nsorce = 1\n", "unknown key 'sorce'"},
		"not a table":   {"", ""},
		"quoted parm":   {"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\nsource = true\nparm = \"'MAP'\"\n", "without quotes"},
		"a C source":    {"[module.ABCMOD.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\nsource = true\n", "exactly one assembler source"},
		"two sources":   {"", ""},
		"with an alias": {"", ""},
	} {
		toml := smpModuleBase + c.extra
		switch name {
		case "not a table":
			toml = strings.Replace(smpModuleBase, "refr = true\n", "refr = true\nsmp = \"LPALIB\"\n", 1)
			c.want = "must be a table"
		case "two sources":
			toml = strings.Replace(smpModuleBase, `sources = ["asm/igx.asm"]`, `sources = ["asm/igx.asm", "asm/two.asm"]`, 1) +
				"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\nsource = true\n"
			c.want = "exactly one assembler source"
		case "with an alias":
			toml = strings.Replace(smpModuleBase, "refr = true\n", "refr = true\naliases = [\"IGXALIAS\"]\n", 1) +
				"[module.IGX00099.smp]\nsyslib = \"LPALIB\"\ndistlib = \"AOST4\"\nsource = true\n"
			c.want = "aliases"
		}
		root := writeTree(t, map[string]string{"mbt.toml": toml, "src/abc.c": "", "asm/igx.asm": "", "asm/two.asm": ""})
		_, err := Load(root)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", name, c.want, err)
		}
	}
}

// mbt 2 does not know the key: a project.toml with it is refused, so its
// package stays what mbt 2 makes of it.
func TestModuleSMPRefusedInProjectToml(t *testing.T) {
	_, err := load(t, `
[project]
name = "x"
[[module]]
name = "IGX00099"
sources = ["asm/igx.asm"]
smp = { syslib = "LPALIB", distlib = "AOST4", source = true }
`, "asm/igx.asm")
	if err == nil || !strings.Contains(err.Error(), "mbt.toml key") {
		t.Errorf("project.toml with smp on a module: %v", err)
	}
}
