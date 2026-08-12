"""Tests for mbttoolchain (issue #67: pin the toolchain a release is built with).

A release must link a *released* libc370, not whatever the default branch holds
at that minute -- libc370 stamps its VERSION into every load module, so an
unpinned release build announces e.g. 'LIBC370 1.0.2-DEV' at STC startup.  The
[toolchain] section declares the refs; these tests cover how a declared value
becomes a git ref and which values are rejected outright.
"""

import sys
import unittest
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbttoolchain
from mbt import EXIT_BUILD, EXIT_SUCCESS
from mbttoolchain import (
    DEFAULT_REF, ToolchainError, check_libc370, declared, git_ref,
    is_version, libc370_status, resolve,
)


class TestGitRef(unittest.TestCase):
    """A bare semver names a release; anything else is already a ref."""

    def test_bare_version_becomes_a_tag(self):
        self.assertEqual(git_ref("1.0.2"), "v1.0.2")

    def test_prerelease_version_becomes_a_tag(self):
        self.assertEqual(git_ref("1.0.2-dev"), "v1.0.2-dev")
        self.assertEqual(git_ref("1.1.0-rc1"), "v1.1.0-rc1")

    def test_build_metadata_becomes_a_tag(self):
        self.assertEqual(git_ref("1.0.2+20260812"), "v1.0.2+20260812")

    def test_tag_is_kept_verbatim(self):
        self.assertEqual(git_ref("v1.0.2"), "v1.0.2")

    def test_branch_is_kept_verbatim(self):
        self.assertEqual(git_ref("main"), "main")
        self.assertEqual(git_ref("feature/loadhi"), "feature/loadhi")

    def test_commit_sha_is_kept_verbatim(self):
        self.assertEqual(git_ref("58767b3"), "58767b3")
        self.assertEqual(
            git_ref("58767b3af6f970dfd520b0b8bd277f2ba82e97c3"),
            "58767b3af6f970dfd520b0b8bd277f2ba82e97c3",
        )

    def test_surrounding_whitespace_is_stripped(self):
        self.assertEqual(git_ref("  1.0.2  "), "v1.0.2")

    def test_partial_version_is_not_a_release(self):
        # '1.0' is not a semver, so it is not silently turned into a tag.
        self.assertEqual(git_ref("1.0"), "1.0")


class TestResolve(unittest.TestCase):
    """Every repository gets a ref; absent means 'main'."""

    def test_no_section_floats_on_main(self):
        self.assertEqual(
            resolve({}),
            {"cc370": DEFAULT_REF, "libc370": DEFAULT_REF},
        )

    def test_empty_section_floats_on_main(self):
        self.assertEqual(
            resolve({"toolchain": {}}),
            {"cc370": DEFAULT_REF, "libc370": DEFAULT_REF},
        )

    def test_partial_section_defaults_the_rest(self):
        # The expected migration state: libc370 pinned, cc370 still floating
        # because it has no releases to pin to.
        self.assertEqual(
            resolve({"toolchain": {"libc370": "1.0.2"}}),
            {"cc370": DEFAULT_REF, "libc370": "v1.0.2"},
        )

    def test_both_pinned(self):
        self.assertEqual(
            resolve({"toolchain": {"cc370": "2.0.0", "libc370": "1.0.2"}}),
            {"cc370": "v2.0.0", "libc370": "v1.0.2"},
        )

    def test_explicit_main_is_allowed(self):
        self.assertEqual(
            resolve({"toolchain": {"cc370": "main", "libc370": "1.0.2"}}),
            {"cc370": "main", "libc370": "v1.0.2"},
        )

    def test_other_sections_are_ignored(self):
        self.assertEqual(
            resolve({"project": {"name": "ufsd"}, "dependencies": {}}),
            {"cc370": DEFAULT_REF, "libc370": DEFAULT_REF},
        )

    # -- rejected input -------------------------------------------------
    def test_unknown_key_is_fatal(self):
        # Not a default-to-main: a typo must not produce an unpinned release.
        with self.assertRaises(ToolchainError) as cm:
            resolve({"toolchain": {"libc730": "1.0.2"}})
        self.assertIn("libc730", str(cm.exception))

    def test_unknown_key_alongside_a_valid_one_is_fatal(self):
        with self.assertRaises(ToolchainError):
            resolve({"toolchain": {"libc370": "1.0.2", "lua370": "5.4.0"}})

    def test_section_must_be_a_table(self):
        with self.assertRaises(ToolchainError):
            resolve({"toolchain": "1.0.2"})

    def test_empty_value_is_fatal(self):
        with self.assertRaises(ToolchainError):
            resolve({"toolchain": {"libc370": ""}})

    def test_blank_value_is_fatal(self):
        with self.assertRaises(ToolchainError):
            resolve({"toolchain": {"libc370": "   "}})

    def test_non_string_value_is_fatal(self):
        with self.assertRaises(ToolchainError):
            resolve({"toolchain": {"libc370": 1.02}})


class TestEnvKey(unittest.TestCase):
    """The names release.yml writes into $GITHUB_ENV."""

    def test_env_keys(self):
        self.assertEqual(mbttoolchain.env_key("cc370"), "CC370_REF")
        self.assertEqual(mbttoolchain.env_key("libc370"), "LIBC370_REF")


class TestDeclared(unittest.TestCase):
    """The raw value, as written -- not the tag it names."""

    def test_returns_the_written_value(self):
        cfg = {"toolchain": {"libc370": "1.0.2"}}
        self.assertEqual(declared(cfg, "libc370"), "1.0.2")

    def test_absent_is_none(self):
        self.assertIsNone(declared({}, "libc370"))
        self.assertIsNone(declared({"toolchain": {}}, "libc370"))

    def test_blank_is_none(self):
        self.assertIsNone(declared({"toolchain": {"libc370": "  "}}, "libc370"))

    def test_non_string_is_none(self):
        self.assertIsNone(declared({"toolchain": {"libc370": 1.02}}, "libc370"))

    def test_is_version_distinguishes_refs(self):
        self.assertTrue(is_version("1.0.2"))
        self.assertTrue(is_version("1.0.2-dev"))
        self.assertFalse(is_version("main"))
        self.assertFalse(is_version("v1.0.2"))
        self.assertFalse(is_version("58767b3"))


class TestLibc370Status(unittest.TestCase):
    """The comparison the build gates on and `make doctor` reports.

    `sysroot` is passed as a directory that holds a synthetic libc.a, so the
    real toolchain installed on the machine never influences the result.
    """

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.sysroot = Path(self._tmp.name)
        (self.sysroot / "lib").mkdir()

    def _install(self, version: str) -> Path:
        (self.sysroot / "lib" / "libc.a").write_bytes(
            f"LIBC370 {version} (abc1234)".encode("cp037")
        )
        return self.sysroot

    def _cfg(self, value=None):
        return {"toolchain": {"libc370": value}} if value else {}

    # -- satisfied ------------------------------------------------------
    def test_newer_satisfies_the_minimum(self):
        status, _ = libc370_status(self._cfg("1.0.2"), self._install("1.0.3-dev"))
        self.assertEqual(status, "ok")

    def test_equal_satisfies_the_minimum(self):
        status, _ = libc370_status(self._cfg("1.0.2"), self._install("1.0.2"))
        self.assertEqual(status, "ok")

    def test_equal_satisfies_exact(self):
        status, _ = libc370_status(
            self._cfg("1.0.2"), self._install("1.0.2"), exact=True
        )
        self.assertEqual(status, "ok")

    # -- violated -------------------------------------------------------
    def test_older_fails_the_minimum(self):
        status, msg = libc370_status(self._cfg("1.0.2"), self._install("1.0.1"))
        self.assertEqual(status, "fail")
        self.assertIn("1.0.1", msg)
        self.assertIn("1.0.2", msg)

    def test_prerelease_of_the_wanted_version_fails(self):
        # The case that motivated this: a sysroot on 1.0.2-dev while 1.0.2
        # is released. Semver puts the prerelease below it, so >= must fail.
        status, _ = libc370_status(self._cfg("1.0.2"), self._install("1.0.2-dev"))
        self.assertEqual(status, "fail")

    def test_older_fails_exact_too(self):
        # A sysroot below the pin cannot have built what CI will build.
        status, _ = libc370_status(
            self._cfg("1.0.2"), self._install("1.0.1"), exact=True
        )
        self.assertEqual(status, "fail")

    # -- drift (release only) -------------------------------------------
    def test_newer_only_drifts_on_exact(self):
        # The normal path: development tracks libc370 main, the release pins
        # the current stable. Worth a warning, not a blocked release (#71).
        status, msg = libc370_status(
            self._cfg("1.0.2"), self._install("1.0.3-dev"), exact=True
        )
        self.assertEqual(status, "drift")
        self.assertIn("1.0.2", msg)
        self.assertIn("1.0.3-dev", msg)

    def test_newer_is_plain_ok_for_a_build(self):
        status, _ = libc370_status(self._cfg("1.0.2"), self._install("1.0.3-dev"))
        self.assertEqual(status, "ok")

    # -- nothing to check -----------------------------------------------
    def test_no_declaration_skips(self):
        status, msg = libc370_status(self._cfg(), self._install("1.0.3-dev"))
        self.assertEqual(status, "skip")
        self.assertIn("1.0.3-dev", msg)   # still reports what is installed

    def test_branch_declaration_skips(self):
        status, _ = libc370_status(self._cfg("main"), self._install("1.0.1"))
        self.assertEqual(status, "skip")

    def test_sha_declaration_skips(self):
        status, _ = libc370_status(self._cfg("58767b3"), self._install("1.0.1"))
        self.assertEqual(status, "skip")

    def test_no_sysroot_skips(self):
        status, _ = libc370_status(self._cfg("1.0.2"), None)
        self.assertEqual(status, "skip")

    # -- unreadable stamp -----------------------------------------------
    def test_unreadable_stamp_is_unknown_not_fail(self):
        # A future change to libc370's stamp format must not fail every build.
        (self.sysroot / "lib" / "libc.a").write_bytes(b"\x00" * 64)
        status, msg = libc370_status(self._cfg("1.0.2"), self.sysroot)
        self.assertEqual(status, "unknown")
        self.assertIn("1.0.2", msg)

    def test_missing_archive_is_unknown_not_fail(self):
        status, _ = libc370_status(self._cfg("1.0.2"), self.sysroot)
        self.assertEqual(status, "unknown")


class TestCheckLibc370ExitCodes(unittest.TestCase):
    """Only a real violation may stop a build."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.sysroot = Path(self._tmp.name)
        (self.sysroot / "lib").mkdir()

    def _install(self, version: str):
        (self.sysroot / "lib" / "libc.a").write_bytes(
            f"LIBC370 {version} (abc1234)".encode("cp037")
        )
        return self.sysroot

    def test_satisfied_is_success(self):
        cfg = {"toolchain": {"libc370": "1.0.2"}}
        self.assertEqual(check_libc370(cfg, self._install("1.0.2")), EXIT_SUCCESS)

    def test_violation_is_build_failure(self):
        cfg = {"toolchain": {"libc370": "1.0.2"}}
        self.assertEqual(check_libc370(cfg, self._install("1.0.1")), EXIT_BUILD)

    def test_unreadable_stamp_is_success(self):
        (self.sysroot / "lib" / "libc.a").write_bytes(b"\x00" * 64)
        cfg = {"toolchain": {"libc370": "1.0.2"}}
        self.assertEqual(check_libc370(cfg, self.sysroot), EXIT_SUCCESS)

    def test_no_declaration_is_success(self):
        self.assertEqual(check_libc370({}, self._install("1.0.1")), EXIT_SUCCESS)

    def test_release_drift_is_success(self):
        # `make release` must not be blocked by a sysroot newer than the pin.
        cfg = {"toolchain": {"libc370": "1.0.2"}}
        self.assertEqual(
            check_libc370(cfg, self._install("1.0.3-dev"), exact=True),
            EXIT_SUCCESS,
        )

    def test_release_below_the_pin_is_still_a_failure(self):
        cfg = {"toolchain": {"libc370": "1.0.2"}}
        self.assertEqual(
            check_libc370(cfg, self._install("1.0.1"), exact=True), EXIT_BUILD
        )


if __name__ == "__main__":
    unittest.main()
