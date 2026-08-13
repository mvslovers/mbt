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
import tempfile
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


class NorentNoreusTest(unittest.TestCase):
    """norent / noreus module options -> MODULE_<key>_NORENT / _NOREUS flags."""

    def _emit(self, **extra):
        lines = []
        mod = {"name": "IRXANCHR", "entry": "IRXANCHR", "startup": False,
               "sources": [], **extra}
        mbtconfig._emit_module(lines, mod, "build", set(), set(), "MODULES")
        return "\n".join(lines)

    def test_default_emits_neither(self):
        out = self._emit()
        self.assertNotIn("_NORENT", out)
        self.assertNotIn("_NOREUS", out)

    def test_norent(self):
        self.assertIn("MODULE_IRXANCHR_NORENT := 1", self._emit(norent=True))

    def test_noreus(self):
        self.assertIn("MODULE_IRXANCHR_NOREUS := 1", self._emit(noreus=True))


class MvsFalseTest(unittest.TestCase):
    """`mvs = false` (the mirror of `host = false`): a test whose fixtures only
    resolve on the host has nothing to build for MVS -- generate() must drop it
    from TESTS entirely so `make test`/`test-mvs` never sees it, while a normal
    test in the same project.toml is emitted as usual."""

    def _generate(self, toml_text):
        with tempfile.TemporaryDirectory() as d:
            proj = Path(d, "project.toml")
            proj.write_text(toml_text)
            return mbtconfig.generate(str(proj), builddir=str(Path(d, "build")))

    def test_mvs_false_test_is_skipped(self):
        out = self._generate(
            '[[test]]\nname = "TSTCFG"\nmvs = false\nsources = []\n'
        )
        self.assertNotIn("TESTS += TSTCFG", out)
        self.assertNotIn("MODULE_TSTCFG_NAME", out)

    def test_other_tests_unaffected(self):
        out = self._generate(
            '[[test]]\nname = "TSTCFG"\nmvs = false\nsources = []\n\n'
            '[[test]]\nname = "TSTBUF"\nsources = []\n'
        )
        self.assertNotIn("TESTS += TSTCFG", out)
        self.assertIn("TESTS += TSTBUF", out)
        self.assertIn("MODULE_TSTBUF_NAME := TSTBUF", out)

    def test_mvs_true_or_absent_still_emitted(self):
        out = self._generate(
            '[[test]]\nname = "TSTA"\nmvs = true\nsources = []\n\n'
            '[[test]]\nname = "TSTB"\nsources = []\n'
        )
        self.assertIn("TESTS += TSTA", out)
        self.assertIn("TESTS += TSTB", out)


# -- Member name validation (issue #73) --------------------------------------
#
# A [[test]]/[[module]] name over 8 characters used to build and deploy without
# a word, then die on MVS as a JCL error (IEF642I EXCESSIVE PARAMETER LENGTH IN
# THE PGM FIELD) that discards the whole job -- so the matrix reported FAIL for
# every step and named no cause.  generate() now rejects it up front.

class MemberNameRuleTest(unittest.TestCase):
    """The rule itself: 1..8 of A-Z 0-9 @ # $, first character not a digit."""

    def _check(self, name, kind="test"):
        mbtconfig._check_member_name(name, kind)

    def test_accepts_plain_names(self):
        for name in ("A", "TSTBUF", "TSTSMOKE", "HTTPDMTT"):
            with self.subTest(name=name):
                self._check(name)

    def test_accepts_national_characters(self):
        # Real, shipping names -- rexx370's IRX#HELO and ind_file370's IND$FILE.
        for name in ("IRX#HELO", "IND$FILE", "@MAIN", "#A$B@C1"):
            with self.subTest(name=name):
                self._check(name)

    def test_rejects_nine_characters(self):
        # The httpd case from issue #73.
        with self.assertRaises(mbtconfig.ConfigError) as cm:
            self._check("TSTEXPIRE")
        msg = str(cm.exception)
        self.assertIn("TSTEXPIRE", msg)
        self.assertIn("9 characters", msg)
        self.assertIn("8 at most", msg)

    def test_rejects_lowercase_leading_digit_and_punctuation(self):
        for name in ("tstexp", "1TST", "TST-EXP", "TST EXP", "TST.EXP"):
            with self.subTest(name=name):
                with self.assertRaises(mbtconfig.ConfigError) as cm:
                    self._check(name)
                self.assertIn("valid MVS member name", str(cm.exception))

    def test_rejects_missing_or_non_string_name(self):
        # 12345678 is a valid TOML integer; len() on it would blow past the
        # ConfigError handler and leave the previous config.mk in place.
        for name in (None, "", 12345678, True, ["TSTBUF"]):
            with self.subTest(name=name):
                with self.assertRaises(mbtconfig.ConfigError):
                    self._check(name)

    def test_message_names_the_section(self):
        with self.assertRaises(mbtconfig.ConfigError) as cm:
            self._check("HTTPDMODULE", kind="module")
        self.assertIn("module name", str(cm.exception))


class GenerateValidatesNamesTest(unittest.TestCase):
    """generate() raises before emitting anything -- for modules and tests
    alike, and before the `mvs = false` skip."""

    def _generate(self, toml_text):
        with tempfile.TemporaryDirectory() as d:
            proj = Path(d, "project.toml")
            proj.write_text(toml_text)
            return mbtconfig.generate(str(proj), builddir=str(Path(d, "build")))

    def test_long_test_name_raises(self):
        with self.assertRaises(mbtconfig.ConfigError):
            self._generate('[[test]]\nname = "TSTEXPIRE"\nsources = []\n')

    def test_long_module_name_raises(self):
        with self.assertRaises(mbtconfig.ConfigError):
            self._generate('[[module]]\nname = "HTTPDPROXY"\nsources = []\n')

    def test_host_only_test_is_validated_too(self):
        # `mvs = false` drops the test from TESTS, but the name is still the
        # --only key and the host binary, so the rule holds there as well.
        with self.assertRaises(mbtconfig.ConfigError):
            self._generate(
                '[[test]]\nname = "TSTEXPIRE"\nmvs = false\nsources = []\n'
            )

    def test_lib_name_is_not_a_member_name(self):
        # [lib] name is a host archive (build/liblua370.a), not a PDS member.
        out = self._generate(
            '[lib]\nname = "liblua370"\nsources = []\n'
        )
        self.assertIn("LIB_NAME := liblua370", out)

    def test_valid_project_still_generates(self):
        out = self._generate(
            '[[module]]\nname = "IRX#HELO"\nsources = []\n\n'
            '[[module]]\nname = "IND$FILE"\nsources = []\n\n'
            '[[test]]\nname = "TSTSMOKE"\nsources = []\n'
        )
        self.assertIn("MODULES += IRX_HELO", out)
        self.assertIn("MODULES += IND_FILE", out)
        self.assertIn("TESTS += TSTSMOKE", out)


class MakeSurfacesNameErrorTest(unittest.TestCase):
    """The plumbing, not the rule: mk/mbt.mk greps '[mbt]' lines away and
    discards mbtconfig's exit status, and pulls config.mk in with `-include`.
    A bad name must still stop make, with the message visible."""

    # The relevant three lines of mk/mbt.mk, verbatim in behaviour.
    MAKEFILE = (
        "$(shell mkdir -p .mbt build)\n"
        "$(shell python3 {script} --project project.toml --builddir build "
        "--output file 2>&1 | grep -v '^\\[mbt\\]' >&2)\n"
        "-include .mbt/config.mk\n"
        "all:\n\t@echo BUILT\n"
    )

    def _run_make(self, d):
        Path(d, "Makefile").write_text(
            self.MAKEFILE.format(script=Path(mbtconfig.__file__).resolve())
        )
        return subprocess.run(["make", "-n", "all"], cwd=d,
                              capture_output=True, text=True)

    def test_cli_exits_with_config_error_code(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "project.toml").write_text(
                '[[test]]\nname = "TSTEXPIRE"\nsources = []\n')
            r = subprocess.run(
                [sys.executable, str(Path(mbtconfig.__file__).resolve()),
                 "--project", "project.toml", "--output", "file"],
                cwd=d, capture_output=True, text=True)
            self.assertEqual(r.returncode, 2, r.stderr)
            self.assertIn("TSTEXPIRE", r.stderr)
            self.assertIn("[mbt] ERROR:", r.stderr)

    @unittest.skipUnless(shutil.which("make"), "make not available")
    def test_make_stops_and_shows_the_name(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "project.toml").write_text(
                '[[test]]\nname = "TSTEXPIRE"\nsources = []\n')
            r = self._run_make(d)
            self.assertNotEqual(r.returncode, 0, "make did not stop")
            out = r.stdout + r.stderr
            self.assertIn("TSTEXPIRE", out)
            self.assertIn("8 at most", out)
            self.assertNotIn("BUILT", out)

    @unittest.skipUnless(shutil.which("make"), "make not available")
    def test_stale_config_mk_is_replaced(self):
        # Without overwriting it, `-include` would keep the previous good
        # config.mk and the build would carry on against a stale module list.
        with tempfile.TemporaryDirectory() as d:
            Path(d, "project.toml").write_text(
                '[[test]]\nname = "TSTBUF"\nsources = []\n')
            self.assertEqual(self._run_make(d).returncode, 0)
            self.assertIn("TESTS += TSTBUF", Path(d, ".mbt/config.mk").read_text())

            Path(d, "project.toml").write_text(
                '[[test]]\nname = "TSTEXPIRE"\nsources = []\n')
            r = self._run_make(d)
            self.assertNotEqual(r.returncode, 0, "make used the stale config.mk")
            self.assertIn("TSTEXPIRE", r.stdout + r.stderr)

    @unittest.skipUnless(shutil.which("make"), "make not available")
    def test_parenthesis_in_name_does_not_break_the_message(self):
        # make matches parentheses balanced inside $(error ...); an unescaped
        # one would turn the message into a "missing separator" syntax error.
        with tempfile.TemporaryDirectory() as d:
            Path(d, "project.toml").write_text(
                '[[test]]\nname = "TST(1)"\nsources = []\n')
            r = self._run_make(d)
            out = r.stdout + r.stderr
            self.assertNotEqual(r.returncode, 0)
            self.assertIn("TST(1)", out)
            self.assertNotIn("missing separator", out)

    @unittest.skipUnless(shutil.which("make"), "make not available")
    def test_national_characters_still_build(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "project.toml").write_text(
                '[[module]]\nname = "IRX#HELO"\nsources = []\n\n'
                '[[module]]\nname = "IND$FILE"\nsources = []\n\n'
                '[[test]]\nname = "TSTSMOKE"\nsources = []\n')
            r = self._run_make(d)
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
            self.assertIn("BUILT", r.stdout)


if __name__ == "__main__":
    unittest.main()
