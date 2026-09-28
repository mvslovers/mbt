"""Tests for scripts/mbtcompiledb.py -- the clang compilation database.

#118: arguments[0] must be the program that runs the entry.  Every flag in
it is a clang flag, so that is clang; cc370 rejects them, and consumers that
execute the command (CLion/IntelliJ) lost every include path.

#119: the target must give clang cc370's data model -- ILP32, big-endian,
unsigned char, and size_t as 'long unsigned int'.  s390x (even with
-U__LP64__) kept 8-byte pointers and long; powerpc-unknown-linux-gnu got the
sizes right but made sizeof 'unsigned int', so clang flagged every libc
declaration of the sysroot as an incompatible redeclaration.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbtcompiledb


def _target():
    return next(f for f in mbtcompiledb.CLANGD_FLAGS
                if f.startswith("--target="))


class EntryTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        d = Path(self._tmp.name)
        (d / "src").mkdir()
        (d / "src" / "hello.c").write_text("int main(void) { return 0; }\n")
        (d / "project.toml").write_text(
            '[project]\nname = "hello"\nversion = "1.0.0"\n\n'
            '[[module]]\nname = "HELLO"\nsources = ["src/hello.c"]\n')
        self.dir = d

    def tearDown(self):
        self._tmp.cleanup()

    def _generate(self):
        # sources are globbed relative to the working directory, as under make
        argv = ["mbtcompiledb", "--project", "project.toml"]
        cwd = Path.cwd()
        os.chdir(self.dir)
        try:
            with mock.patch.object(sys, "argv", argv), \
                 mock.patch("builtins.print"):
                rc = mbtcompiledb.main()
        finally:
            os.chdir(cwd)
        self.assertEqual(rc, 0)
        return json.loads((self.dir / "compile_commands.json").read_text())

    def test_arguments0_is_clang(self):
        entries = self._generate()
        self.assertEqual(len(entries), 1)
        self.assertEqual(entries[0]["arguments"][0], "clang")

    def test_no_lp64_workaround_left(self):
        args = self._generate()[0]["arguments"]
        self.assertNotIn("-U__LP64__", args)
        self.assertNotIn("--target=s390x-ibm-linux", args)
        self.assertIn(_target(), args)


@unittest.skipUnless(shutil.which("clang"), "clang not available")
class TargetDataModelTest(unittest.TestCase):
    """What cc370 reports for itself (cc370 -E -dM, and GCC 3.4.6's i370
    target): 4-byte pointer and long, big-endian, unsigned char, sizeof of
    type 'long unsigned int'."""

    @classmethod
    def setUpClass(cls):
        r = subprocess.run(
            ["clang", "-xc", _target(), "-nostdinc", "-E", "-dM", "-"],
            input="", capture_output=True, text=True)
        cls.macros = {}
        for line in r.stdout.splitlines():
            parts = line.split(" ", 2)
            if len(parts) >= 2 and parts[0] == "#define":
                cls.macros[parts[1]] = parts[2] if len(parts) == 3 else ""

    def test_ilp32(self):
        self.assertEqual(self.macros.get("__SIZEOF_POINTER__"), "4")
        self.assertEqual(self.macros.get("__SIZEOF_LONG__"), "4")
        self.assertNotIn("__LP64__", self.macros)

    def test_big_endian(self):
        self.assertEqual(self.macros.get("__BYTE_ORDER__"),
                         "__ORDER_BIG_ENDIAN__")

    def test_unsigned_char(self):
        self.assertIn("__CHAR_UNSIGNED__", self.macros)

    def test_size_type_matches_cc370(self):
        # anything else and clang reports every sysroot libc declaration as
        # an incompatible redeclaration of a library builtin
        self.assertEqual(self.macros.get("__SIZE_TYPE__"), "long unsigned int")
        self.assertEqual(self.macros.get("__PTRDIFF_TYPE__"), "long int")


if __name__ == "__main__":
    unittest.main()
