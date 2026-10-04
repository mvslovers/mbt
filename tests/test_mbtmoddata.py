"""mbtmoddata: no writable data in a module that promises to have none.

cc370 keeps a module's writable data in its CSECT; a rent = true module is one
copy for every task that LINKs it (cc370#100: CDUSE=3 on mvsdev).  Only an
explicit rent = true stops the build -- RENT by ld370's default and ac = 1 are
warnings, because whether they bite is the project's knowledge (#91).
"""

import contextlib
import io
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbtmoddata as M  # noqa: E402

STATIC = "static int counter;\nint f(void){ return ++counter; }\n"
CLEAN = "int f(int x){ int y = x; return y + 1; }\n"
CONSTS = "static const char tbl[] = \"abc\";\nint f(void){ return tbl[0]; }\n"
STKLEN = "unsigned __stklen = 64*1024;\nint main(void){ return 0; }\n"
LOCAL_STATIC = "int f(void){ static int n; return ++n; }\n"


class _Proj:
    def __init__(self, files: dict, project: dict):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        for name, text in files.items():
            p = self.root / name
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(text)
        self.project = project

    def check(self):
        cwd = os.getcwd()
        os.chdir(self.root)
        try:
            return M.check(self.project)
        finally:
            os.chdir(cwd)

    def close(self):
        self._tmp.cleanup()


def _mod(name, src, **kw):
    return {"name": name, "sources": [src], **kw}


class TestCheck(unittest.TestCase):

    def run_one(self, text, **attrs):
        p = _Proj({"src/a.c": text}, {"module": [_mod("MOD", "src/a.c", **attrs)]})
        try:
            return p.check()
        finally:
            p.close()

    def test_rent_true_with_static_is_an_error(self):
        errors, warnings = self.run_one(STATIC, rent=True)
        self.assertEqual(len(errors), 1)
        self.assertEqual(errors[0][0], "rent = true")

    def test_function_local_static_counts(self):
        errors, _ = self.run_one(LOCAL_STATIC, rent=True)
        self.assertEqual(len(errors), 1)

    def test_rent_true_clean(self):
        self.assertEqual(self.run_one(CLEAN, rent=True), ([], []))

    def test_const_is_not_writable(self):
        self.assertEqual(self.run_one(CONSTS, rent=True), ([], []))

    def test_stklen_is_exempt(self):
        self.assertEqual(self.run_one(STKLEN, rent=True), ([], []))

    def test_default_rent_is_a_warning(self):
        errors, warnings = self.run_one(STATIC)
        self.assertEqual(errors, [])
        self.assertEqual(warnings[0][0], "RENT by ld370's default")

    def test_rent_false_is_skipped(self):
        self.assertEqual(self.run_one(STATIC, rent=False), ([], []))

    def test_legacy_norent_is_skipped(self):
        self.assertEqual(self.run_one(STATIC, norent=True), ([], []))

    def test_ac1_is_a_warning_even_when_norent(self):
        errors, warnings = self.run_one(STATIC, ac=1, rent=False)
        self.assertEqual(errors, [])
        self.assertTrue(warnings[0][0].startswith("ac = 1"))

    def test_undeclared_test_is_skipped(self):
        p = _Proj({"t/t.c": STATIC}, {"test": [_mod("TST", "t/t.c")]})
        try:
            self.assertEqual(p.check(), ([], []))
        finally:
            p.close()

    def test_exclude_is_honoured(self):
        p = _Proj({"src/a.c": STATIC, "src/b.c": CLEAN},
                  {"module": [{"name": "MOD", "sources": ["src/*.c"],
                               "exclude": ["src/a.c"], "rent": True}]})
        try:
            self.assertEqual(p.check(), ([], []))
        finally:
            p.close()


ANON_CONST = ("static const struct { const char *n; int v; } tbl[] = "
              "{ {\"a\", 1}, {\"b\", 2} };\nint f(void){ return tbl[0].v; }\n")
ANON_DATA = "static struct { int a; int b; } state;\nint f(void){ return ++state.a; }\n"
HOST_ONLY = ("#ifndef __MVS__\nstatic int host_sim;\n#endif\n"
             "int f(void){ return 0; }\n")


class TestParser(unittest.TestCase):

    def run_one(self, text, cflags=None, **attrs):
        p = _Proj({"src/a.c": text}, {"module": [_mod("MOD", "src/a.c", rent=True)]})
        try:
            cwd = os.getcwd()
            os.chdir(p.root)
            try:
                return M.check(p.project, cflags)
            finally:
                os.chdir(cwd)
        finally:
            p.close()

    def test_anonymous_const_struct_table_is_not_writable(self):
        """The tail after the struct body inherits 'static const' (rexx370)."""
        self.assertEqual(self.run_one(ANON_CONST), ([], []))

    def test_anonymous_struct_instance_is_writable(self):
        errors, _ = self.run_one(ANON_DATA)
        self.assertEqual(len(errors), 1)

    def test_function_with_struct_parameter_does_not_leak_its_head(self):
        """A function head ending in ')' must not prefix the next statement."""
        src = ("static int f(struct s *p) { return 0; }\n"
               "static int g(int x);\nint h(void){ return 1; }\n")
        self.assertEqual(self.run_one(src), ([], []))

    def test_attributed_struct_definition_is_not_data(self):
        """`} __attribute__((aligned(n)));` closes a type, not an instance
        (mvsMF router.h)."""
        src = ("struct route { int m; } __attribute__((aligned(4)));\n"
               "void f(void) __attribute__((noreturn));\n")
        self.assertEqual(self.run_one(src), ([], []))

    def test_prototype_with_function_pointer_parameter_is_not_data(self):
        """brexx370 hashmap.h / rexx.h."""
        src = ("void hashMapFree(void *h, void (*freeData)(void *));\n"
               "int RxReFn( char *name, void ( *func)(int), int opt );\n")
        self.assertEqual(self.run_one(src), ([], []))

    def test_function_pointer_variables_are_writable(self):
        src = "static int (*fp)(int);\nstatic void (*tbl[4])(void);\n"
        errors, _ = self.run_one(src)
        self.assertEqual(len(errors), 2)

    def test_initializer_ending_in_a_parenthesis_is_data(self):
        """brexx370 date.c: the table was taken for a function head."""
        src = 'static char *days[] = { ("Mon"), ("Tue") };\nstatic int n = f(1);\n'
        errors, _ = self.run_one(src)
        self.assertEqual(len(errors), 2)

    def test_attributed_instance_is_still_writable(self):
        src = "static int buf[8] __attribute__((aligned(8)));\n"
        errors, _ = self.run_one(src)
        self.assertEqual(len(errors), 1)

    def test_raw_scan_sees_the_host_branch(self):
        """Without cflags the text is scanned as is -- the old behaviour."""
        errors, _ = self.run_one(HOST_ONLY)
        self.assertEqual(len(errors), 1)

    @unittest.skipUnless(shutil.which("cc370"), "needs cc370 on PATH")
    def test_preprocessed_scan_skips_the_host_branch(self):
        """With cflags, cc370 -E drops the !__MVS__ branch (rexx370)."""
        self.assertEqual(self.run_one(HOST_ONLY, cflags=["-O1"]), ([], []))

    @unittest.skipUnless(shutil.which("cc370"), "needs cc370 on PATH")
    def test_preprocessed_scan_keeps_line_numbers(self):
        errors, _ = self.run_one("#ifndef __MVS__\nint x;\n#endif\n\n"
                                 "static int y;\nint f(void){ return ++y; }\n",
                                 cflags=["-O1"])
        self.assertEqual([(e[2], e[3]) for e in errors], [("src/a.c", 5)])


class TestHeaders(unittest.TestCase):
    """Data defined in a header the source includes is in the module too --
    mbt's own mbtcheck.h has `static int mbt_run` which 2.1.1 missed."""

    def _check(self, files, project, cflags):
        p = _Proj(files, project)
        try:
            cwd = os.getcwd()
            os.chdir(p.root)
            try:
                return M.check(p.project, cflags)
            finally:
                os.chdir(cwd)
        finally:
            p.close()

    @unittest.skipUnless(shutil.which("cc370"), "needs cc370 on PATH")
    def test_static_in_a_project_header_is_found(self):
        errors, _ = self._check(
            {"include/state.h": "static int counter;\n",
             "src/a.c": "#include \"state.h\"\nint f(void){ return ++counter; }\n"},
            {"module": [_mod("MOD", "src/a.c", rent=True)]}, ["-I", "include"])
        self.assertEqual([(e[2], e[3]) for e in errors], [("include/state.h", 1)])

    @unittest.skipUnless(shutil.which("cc370"), "needs cc370 on PATH")
    def test_mbtcheck_counters_fail_a_rent_test(self):
        """ftpd's TSTPRM was declared rent = true and built green on 2.1.1."""
        mbt_include = str(Path(__file__).parent.parent / "include")
        errors, _ = self._check(
            {"test/t.c": "#include <mbtcheck.h>\nint main(void){ return 0; }\n"},
            {"test": [_mod("TST", "test/t.c", rent=True)]}, ["-I", mbt_include])
        names = {e[4].split("=")[0].split()[-1] for e in errors}
        self.assertTrue({"mbt_run", "mbt_passed", "mbt_failed"} <= names, names)

    @unittest.skipUnless(shutil.which("cc370"), "needs cc370 on PATH")
    def test_a_header_included_twice_is_reported_once(self):
        errors, _ = self._check(
            {"include/state.h": "static int counter;\n",
             "src/a.c": "#include \"state.h\"\nint f(void){ return ++counter; }\n",
             "src/b.c": "#include \"state.h\"\nint g(void){ return counter; }\n"},
            {"module": [{"name": "MOD", "sources": ["src/*.c"], "rent": True}]},
            ["-I", "include"])
        self.assertEqual(len(errors), 1)

    @unittest.skipUnless(shutil.which("cc370"), "needs cc370 on PATH")
    def test_system_headers_are_not_scanned(self):
        self.assertEqual(self._check(
            {"src/a.c": "#include <stdio.h>\n#include <ctype.h>\nint f(void){ return 0; }\n"},
            {"module": [_mod("MOD", "src/a.c", rent=True)]}, ["-O1"]), ([], []))

    def test_internal_is_reported_without_other_findings(self):
        """httpd went silent on 2.1.1 once its own modules were clean."""
        errors, warnings = self._check(
            {"src/a.c": CLEAN, "src/lib.c": STATIC},
            {"module": [_mod("MOD", "src/a.c", rent=True)],
             "internal": {"sources": ["src/lib.c"]}}, None)
        self.assertEqual(errors, [])
        self.assertEqual(len(warnings), 1)
        self.assertTrue(warnings[0][0].startswith("[internal]"))


if __name__ == "__main__":
    unittest.main()
