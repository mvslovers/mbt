"""Tests for scripts/mbtconfig.py -- the config.mk generator.

Regression for module names containing MVS national characters (e.g. '#').
A '#' in a module name used to leak into the generated Make variable names
(MODULES += IRX#HELO, MODULE_IRX#HELO_ENTRY := ...), where '#' starts a Make
comment, so `make` died with "missing separator". The generator now uses a
make-safe key (# -> _) for variable names and carries the real member name
in MODULE_<key>_NAME.
"""

import sys
import shutil
import subprocess
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbtconfig


class VarKeyTest(unittest.TestCase):
    def test_hash_and_dollar_mapped_to_underscore(self):
        self.assertEqual(mbtconfig._var_key("IRX#HELO"), "IRX_HELO")
        self.assertEqual(mbtconfig._var_key("FOO$BAR"), "FOO_BAR")

    def test_plain_name_unchanged(self):
        self.assertEqual(mbtconfig._var_key("UFSD"), "UFSD")
        self.assertEqual(mbtconfig._var_key("IRXINIT"), "IRXINIT")


class EmitHashModuleTest(unittest.TestCase):
    def _emit(self, name):
        lines = []
        mod = {"name": name, "entry": "@@CRT0", "startup": "crt0", "sources": []}
        mbtconfig._emit_module(lines, mod, "build", set(), set(), "MODULES")
        return lines

    def test_key_sanitized_real_name_preserved(self):
        out = "\n".join(self._emit("IRX#HELO"))
        self.assertIn("MODULES += IRX_HELO", out)
        # the real member name is carried (escaped) for the output member/file
        self.assertIn("MODULE_IRX_HELO_NAME := IRX\\#HELO", out)
        self.assertIn("MODULE_IRX_HELO_ENTRY :=", out)
        self.assertIn("MODULE_IRX_HELO_ALIAS := irx_helo", out)

    def test_no_hash_in_any_make_identifier(self):
        # '#' must never appear left of ':=' (a variable name) nor in the
        # '<PREFIX> += <key>' list line -- those are Make identifiers.
        for line in self._emit("IRX#HELO"):
            lhs = line.split(":=", 1)[0] if ":=" in line else line
            self.assertNotIn("#", lhs, f"'#' leaked into a Make identifier: {line!r}")


class MakeParsesGeneratedConfigTest(unittest.TestCase):
    """End-to-end: a config.mk for a '#'-named module must parse under make."""

    @unittest.skipUnless(shutil.which("make"), "make not available")
    def test_make_includes_config_without_error(self):
        lines = []
        mod = {"name": "IRX#HELO", "entry": "@@CRT0", "startup": "crt0", "sources": []}
        mbtconfig._emit_module(lines, mod, "build", set(), set(), "MODULES")
        import tempfile, os
        with tempfile.TemporaryDirectory() as d:
            Path(d, "config.mk").write_text("\n".join(lines) + "\n")
            Path(d, "Makefile").write_text("include config.mk\nall:\n\t@true\n")
            r = subprocess.run(["make", "-n", "all"], cwd=d,
                               capture_output=True, text=True)
            self.assertEqual(r.returncode, 0,
                             f"make failed to parse generated config.mk:\n{r.stderr}")


if __name__ == "__main__":
    unittest.main()
