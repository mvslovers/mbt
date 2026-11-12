package project

import (
	"path"
	"strconv"
	"strings"
)

// ModuleSMP is a module's [module.NAME.smp]: the module goes into a system
// library of the SMP procedures (SYS1.LPALIB, say) instead of the product's
// target, link-edited there by a JCLIN step of its own (mbt#380).
type ModuleSMP struct {
	// Syslib and Distlib are DD names of the SMP procedures: the system
	// library the module is linked into, and the distribution library SMP
	// keeps it in. The SMP package checks them against the procedures.
	Syslib, Distlib string
	// Source: the module travels as its assembler source and the install
	// job assembles it with the target system's IFOX00 and macros. Only
	// true is supported.
	Source bool
	// Parm is the PARM of the IEWL step; empty means derived from the
	// module's rent/reus/refr.
	Parm string
	// File is the module's one .asm source (with Source), relative to the
	// project root.
	File string
}

var moduleSMPKeys = []string{"syslib", "distlib", "source", "parm"}

// moduleSMP reads the smp table of a module. Called after the sources are
// resolved, since source = true needs exactly one assembler source.
func moduleSMP(u *Unit, v any) (*ModuleSMP, error) {
	where := "[module." + quoteKey(u.Name) + ".smp]"
	t, ok := v.(map[string]any)
	if !ok {
		return nil, configErr("mbt.toml: %s must be a table: { syslib = \"LPALIB\", distlib = \"AOST4\", source = true }", where)
	}
	allowed := map[string]bool{}
	for _, k := range moduleSMPKeys {
		allowed[k] = true
	}
	for _, k := range sortedKeys(t) {
		if !allowed[k] {
			return nil, configErr("mbt.toml: unknown key '%s' in %s (known: %s)", k, where, strings.Join(moduleSMPKeys, ", "))
		}
	}
	s := &ModuleSMP{}
	for _, f := range []struct {
		key string
		dst *string
	}{{"syslib", &s.Syslib}, {"distlib", &s.Distlib}} {
		x, ok := t[f.key].(string)
		if !ok || x == "" {
			return nil, configErr("mbt.toml: %s: '%s' is required, a DD name of the SMP procedures (syslib = \"LPALIB\", distlib = \"AOST4\")", where, f.key)
		}
		*f.dst = strings.ToUpper(x)
	}
	if p, ok := t["parm"]; ok {
		x, isStr := p.(string)
		if !isStr || x == "" || strings.ContainsAny(x, "'\"") {
			return nil, configErr("mbt.toml: %s: parm must be the link-edit options without quotes, e.g. \"MAP,LIST,NCAL,RENT,REUS,REFR\"", where)
		}
		s.Parm = strings.ToUpper(x)
	}
	src, ok := t["source"].(bool)
	if !ok || !src {
		return nil, configErr("mbt.toml: %s: source = true is required. The module is shipped as its assembler source and assembled by the install job; a load module linked from the LKLIB into a system library has not been measured (mbt#380)", where)
	}
	s.Source = true
	if len(u.Aliases) > 0 {
		return nil, configErr("mbt.toml: %s: a module linked into a system library cannot have aliases yet (the JCLIN would need ALIAS statements, which have not been measured)", where)
	}
	if len(u.Sources) != 1 || path.Ext(u.Sources[0]) != ".asm" {
		return nil, configErr("mbt.toml: %s: source = true needs exactly one assembler source (.asm) -- the install job assembles it with IFOX00; this module has %d source(s): %s", where, len(u.Sources), strings.Join(u.Sources, ", "))
	}
	s.File = u.Sources[0]
	return s, nil
}

// LinkParm is the PARM of the module's IEWL step: the given one, or MAP,
// LIST, NCAL and the module's declared attributes.
func (u *Unit) LinkParm() string {
	if u.SMP != nil && u.SMP.Parm != "" {
		return u.SMP.Parm
	}
	parts := []string{"MAP", "LIST", "NCAL"}
	for _, a := range u.Attrs {
		switch a {
		case "--rent", "--reus", "--refr":
			parts = append(parts, strings.ToUpper(a[2:]))
		}
	}
	if u.AC > 0 {
		parts = append(parts, "AC="+strconv.Itoa(u.AC))
	}
	return strings.Join(parts, ",")
}
