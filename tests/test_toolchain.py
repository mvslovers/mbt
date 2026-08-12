"""Tests for mbttoolchain (issue #67: pin the toolchain a release is built with).

A release must link a *released* libc370, not whatever the default branch holds
at that minute -- libc370 stamps its VERSION into every load module, so an
unpinned release build announces e.g. 'LIBC370 1.0.2-DEV' at STC startup.  The
[toolchain] section declares the refs; these tests cover how a declared value
becomes a git ref and which values are rejected outright.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbttoolchain
from mbttoolchain import DEFAULT_REF, ToolchainError, git_ref, resolve


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


if __name__ == "__main__":
    unittest.main()
