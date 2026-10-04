"""mbtmoddata: no writable data in a module that promises to have none.

cc370 keeps a module's writable data in its CSECT; a rent = true module is one
copy for every task that LINKs it (cc370#100: CDUSE=3 on mvsdev).  Only an
explicit rent = true stops the build -- RENT by ld370's default and ac = 1 are
warnings, because whether they bite is the project's knowledge (#91).
"""

import contextlib
import io
import os
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


if __name__ == "__main__":
    unittest.main()
