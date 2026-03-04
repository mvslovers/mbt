"""Tests for mbt/version.py."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.version import Version, satisfies, to_vrm


class TestVersionParse(unittest.TestCase):

    def test_parse_release(self):
        v = Version.parse("1.0.0")
        self.assertEqual(v.major, 1)
        self.assertEqual(v.minor, 0)
        self.assertEqual(v.patch, 0)
        self.assertIsNone(v.pre)

    def test_parse_patch(self):
        v = Version.parse("3.3.1")
        self.assertEqual(v.major, 3)
        self.assertEqual(v.minor, 3)
        self.assertEqual(v.patch, 1)
        self.assertIsNone(v.pre)

    def test_parse_dev(self):
        v = Version.parse("1.0.0-dev")
        self.assertEqual(v.major, 1)
        self.assertEqual(v.pre, "dev")

    def test_parse_rc1(self):
        v = Version.parse("3.3.1-rc1")
        self.assertEqual(v.pre, "rc1")

    def test_parse_rc2(self):
        v = Version.parse("1.0.0-rc2")
        self.assertEqual(v.pre, "rc2")

    def test_parse_invalid_short(self):
        with self.assertRaises(ValueError):
            Version.parse("1.0")

    def test_parse_invalid_text(self):
        with self.assertRaises(ValueError):
            Version.parse("not-a-version")

    def test_parse_invalid_empty(self):
        with self.assertRaises(ValueError):
            Version.parse("")

    def test_parse_invalid_prerelease(self):
        with self.assertRaises(ValueError):
            Version.parse("1.0.0-beta")


class TestVersionStr(unittest.TestCase):

    def test_str_release(self):
        self.assertEqual(str(Version.parse("1.0.0")), "1.0.0")

    def test_str_patch(self):
        self.assertEqual(str(Version.parse("3.3.1")), "3.3.1")

    def test_str_dev(self):
        self.assertEqual(str(Version.parse("1.0.0-dev")), "1.0.0-dev")

    def test_str_rc1(self):
        self.assertEqual(str(Version.parse("3.3.1-rc1")), "3.3.1-rc1")


class TestVersionVRM(unittest.TestCase):

    def test_vrm_100(self):
        self.assertEqual(to_vrm("1.0.0"), "V1R0M0")

    def test_vrm_331(self):
        self.assertEqual(to_vrm("3.3.1"), "V3R3M1")

    def test_vrm_dev(self):
        self.assertEqual(to_vrm("1.0.0-dev"), "V1R0M0D")

    def test_vrm_rc1(self):
        self.assertEqual(to_vrm("3.3.1-rc1"), "V3R3M1R1")

    def test_vrm_rc2(self):
        self.assertEqual(to_vrm("1.0.0-rc2"), "V1R0M0R2")

    def test_vrm_instance_method(self):
        v = Version.parse("3.3.1")
        self.assertEqual(v.to_vrm(), "V3R3M1")


class TestVersionComparison(unittest.TestCase):

    def test_dev_lt_rc1(self):
        self.assertLess(Version.parse("1.0.0-dev"), Version.parse("1.0.0-rc1"))

    def test_rc1_lt_release(self):
        self.assertLess(Version.parse("1.0.0-rc1"), Version.parse("1.0.0"))

    def test_dev_lt_release(self):
        self.assertLess(Version.parse("1.0.0-dev"), Version.parse("1.0.0"))

    def test_rc1_lt_rc2(self):
        self.assertLess(Version.parse("1.0.0-rc1"), Version.parse("1.0.0-rc2"))

    def test_equal(self):
        self.assertEqual(Version.parse("1.0.0"), Version.parse("1.0.0"))

    def test_sorted_ordering(self):
        versions = [
            Version.parse("1.0.0"),
            Version.parse("1.0.0-dev"),
            Version.parse("1.0.0-rc1"),
        ]
        expected = [
            Version.parse("1.0.0-dev"),
            Version.parse("1.0.0-rc1"),
            Version.parse("1.0.0"),
        ]
        self.assertEqual(sorted(versions), expected)

    def test_major_minor_patch_ordering(self):
        self.assertLess(Version.parse("1.0.0"), Version.parse("2.0.0"))
        self.assertLess(Version.parse("1.0.0"), Version.parse("1.1.0"))
        self.assertLess(Version.parse("1.0.0"), Version.parse("1.0.1"))

    def test_ge(self):
        self.assertGreaterEqual(Version.parse("1.0.0"), Version.parse("1.0.0"))
        self.assertGreaterEqual(Version.parse("1.0.1"), Version.parse("1.0.0"))

    def test_le(self):
        self.assertLessEqual(Version.parse("1.0.0"), Version.parse("1.0.0"))
        self.assertLessEqual(Version.parse("1.0.0"), Version.parse("1.0.1"))

    def test_gt(self):
        self.assertGreater(Version.parse("1.0.1"), Version.parse("1.0.0"))


class TestSatisfies(unittest.TestCase):

    def test_gte_true(self):
        self.assertTrue(satisfies("1.5.0", ">=1.0.0"))

    def test_gte_equal(self):
        self.assertTrue(satisfies("1.0.0", ">=1.0.0"))

    def test_gte_false(self):
        self.assertFalse(satisfies("0.9.0", ">=1.0.0"))

    def test_lt_true(self):
        self.assertTrue(satisfies("1.5.0", "<2.0.0"))

    def test_lt_boundary_false(self):
        self.assertFalse(satisfies("2.0.0", "<2.0.0"))

    def test_lt_false(self):
        self.assertFalse(satisfies("2.1.0", "<2.0.0"))

    def test_eq_true(self):
        self.assertTrue(satisfies("1.0.0", "=1.0.0"))

    def test_eq_false(self):
        self.assertFalse(satisfies("1.0.1", "=1.0.0"))

    def test_compound_true(self):
        self.assertTrue(satisfies("1.5.0", ">=1.0.0,<2.0.0"))

    def test_compound_false_upper(self):
        self.assertFalse(satisfies("2.0.0", ">=1.0.0,<2.0.0"))

    def test_compound_false_lower(self):
        self.assertFalse(satisfies("0.9.0", ">=1.0.0,<2.0.0"))

    def test_dev_fails_gte_release(self):
        # 1.0.0-dev < 1.0.0, so >=1.0.0 must be False
        self.assertFalse(satisfies("1.0.0-dev", ">=1.0.0"))

    def test_gte_includes_higher_major(self):
        # >= is literal, no tilde/caret semantics
        self.assertTrue(satisfies("2.0.0", ">=1.0.0"))
        self.assertTrue(satisfies("3.0.0", ">=1.0.0"))

    def test_rc_satisfies_gte_rc(self):
        self.assertTrue(satisfies("1.0.0-rc1", ">=1.0.0-rc1"))

    def test_unknown_operator_raises(self):
        with self.assertRaises(ValueError):
            satisfies("1.0.0", "~1.0.0")


if __name__ == "__main__":
    unittest.main()
