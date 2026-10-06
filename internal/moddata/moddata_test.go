package moddata

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The scanner cases come from mbt v2's tests/test_mbtmoddata.py.
func count(src string) int { return len(scanText("a.c", src)) }

func TestScanner(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      int
	}{
		{"static", "static int counter;\nint f(void){ return ++counter; }\n", 1},
		{"clean", "int f(int x){ int y = x; return y + 1; }\n", 0},
		{"const", "static const char tbl[] = \"abc\";\nint f(void){ return tbl[0]; }\n", 0},
		{"stklen exempt", "unsigned __stklen = 64*1024;\nint main(void){ return 0; }\n", 0},
		{"function-local static", "int f(void){ static int n; return ++n; }\n", 1},
		{"anonymous const struct table", "static const struct { const char *n; int v; } tbl[] = { {\"a\", 1}, {\"b\", 2} };\nint f(void){ return tbl[0].v; }\n", 0},
		{"anonymous struct instance", "static struct { int a; int b; } state;\nint f(void){ return ++state.a; }\n", 1},
		{"struct parameter does not leak", "static int f(struct s *p) { return 0; }\nstatic int g(int x);\nint h(void){ return 1; }\n", 0},
		{"attributed struct definition", "struct route { int m; } __attribute__((aligned(4)));\nvoid f(void) __attribute__((noreturn));\n", 0},
		{"prototype with function-pointer parameter", "void hashMapFree(void *h, void (*freeData)(void *));\nint RxReFn( char *name, void ( *func)(int), int opt );\n", 0},
		{"function-pointer variables", "static int (*fp)(int);\nstatic void (*tbl[4])(void);\n", 2},
		{"initializer ending in ')'", "static char *days[] = { (\"Mon\"), (\"Tue\") };\nstatic int n = f(1);\n", 2},
		{"attributed instance", "static int buf[8] __attribute__((aligned(8)));\n", 1},
		{"comment and string do not count", "/* static int x; */\nchar *s = \"static int y;\";\n", 1},
		// a text proxy: a pointer to const reads as const (v2 behaves the same)
		{"pointer to const", "const char *s = \"x\";\n", 0},
	} {
		if got := count(c.src); got != c.want {
			t.Errorf("%s: %d findings, want %d: %+v", c.name, got, c.want, scanText("a.c", c.src))
		}
	}
}

func TestLineNumbers(t *testing.T) {
	f := scanText("a.c", "int f(void)\n{\n  return 0;\n}\n\nstatic int late;\n")
	if len(f) != 1 || f[0].Line != 6 {
		t.Errorf("findings %+v, want one on line 6", f)
	}
}

// mapLines keeps the source and project headers in order of appearance, each
// line at its own number, and drops system headers and the pseudo-files.
func TestMapLines(t *testing.T) {
	out := "# 1 \"src/a.c\"\n# 1 \"<built-in>\"\n# 1 \"<command line>\"\n# 1 \"src/a.c\"\n" +
		"# 1 \"include/p.h\" 1\nstatic int hdr;\n# 1 \"/sys/stdio.h\" 1 3\nint sys_x;\n" +
		"# 3 \"src/a.c\" 2\nstatic int src;\n"
	got := mapLines(out, "src/a.c")
	if len(got) != 2 || got[0].file != "include/p.h" || got[1].file != "src/a.c" {
		t.Fatalf("files %+v", got)
	}
	// v2 counts the line after the final newline too
	if got[1].text != "\n\nstatic int src;\n" {
		t.Errorf("src/a.c text %q (line 3 expected)", got[1].text)
	}
	if mapLines(out, "src/other.c") != nil {
		t.Error("a source that does not appear must give nil")
	}
}

func TestCheckWithPreprocessor(t *testing.T) {
	if _, err := exec.LookPath("cc370"); err != nil {
		t.Skip("no cc370 on PATH")
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, "include"), 0o755)
	os.WriteFile(filepath.Join(root, "include/p.h"), []byte("static int from_header;\n"), 0o644)
	os.WriteFile(filepath.Join(root, "src/a.c"), []byte("#include \"p.h\"\n#ifndef __MVS__\nstatic int host_sim;\n#endif\nint f(void){ return from_header; }\n"), 0o644)
	yes := true
	in := Input{Root: root, CC: "cc370", CFlags: []string{"-I", "include"},
		Units: []Unit{{Name: "MOD", Rent: &yes, Sources: []string{"src/a.c"}}}}
	errs, _ := Check(in)
	// the host branch is preprocessed away; the header's static is found
	if len(errs) != 1 || errs[0].File != "include/p.h" {
		t.Errorf("errors %+v", errs)
	}
	in.CFlags = nil // raw scan: the host branch counts, the header is not seen
	errs, _ = Check(in)
	if len(errs) != 1 || errs[0].File != "src/a.c" {
		t.Errorf("raw errors %+v", errs)
	}
}

func TestRules(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.c"), []byte("static int s;\n"), 0o644)
	yes, no := true, false
	for _, c := range []struct {
		u           Unit
		errs, warns int
	}{
		{Unit{Name: "R", Rent: &yes}, 1, 0},
		{Unit{Name: "D"}, 0, 1},            // RENT by ld370's default
		{Unit{Name: "N", Rent: &no}, 0, 0}, // declared not reentrant
		{Unit{Name: "A", Rent: &no, AC1: true}, 0, 1},
		{Unit{Name: "T", Test: true}, 0, 0}, // an undeclared test is skipped
	} {
		c.u.Sources = []string{"a.c"}
		errs, warns := Check(Input{Root: root, Units: []Unit{c.u}})
		if len(errs) != c.errs || len(warns) != c.warns {
			t.Errorf("%s: errors %d warnings %d, want %d %d", c.u.Name, len(errs), len(warns), c.errs, c.warns)
		}
	}
}
