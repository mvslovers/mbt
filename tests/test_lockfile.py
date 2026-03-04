"""Tests for mbt/lockfile.py."""

import sys
import unittest
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.lockfile import Lockfile

FIXTURES = Path(__file__).parent / "fixtures"


class TestLockfileCreate(unittest.TestCase):

    def test_create_sets_fields(self):
        lf = Lockfile.create(
            {"mvslovers/crent370": "1.0.0"},
            mbt_version="1.0.0",
        )
        self.assertEqual(lf.dependencies, {"mvslovers/crent370": "1.0.0"})
        self.assertEqual(lf.mbt_version, "1.0.0")

    def test_create_sets_timestamp(self):
        lf = Lockfile.create({}, mbt_version="1.0.0")
        # Should be an ISO 8601 UTC timestamp
        self.assertRegex(lf.generated, r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z")


class TestLockfileRoundTrip(unittest.TestCase):

    def test_save_and_load(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / ".mbt" / "mvs.lock"
            lf = Lockfile.create(
                {
                    "mvslovers/crent370": "1.0.0",
                    "mvslovers/ufs370": "2.0.0",
                },
                mbt_version="1.0.0",
            )
            lf.save(path)

            loaded = Lockfile.load(path)
            self.assertIsNotNone(loaded)
            self.assertEqual(loaded.mbt_version, "1.0.0")
            self.assertEqual(loaded.dependencies["mvslovers/crent370"], "1.0.0")
            self.assertEqual(loaded.dependencies["mvslovers/ufs370"], "2.0.0")
            self.assertEqual(loaded.generated, lf.generated)

    def test_save_creates_parent_dirs(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "a" / "b" / "mvs.lock"
            lf = Lockfile.create({}, "1.0.0")
            lf.save(path)
            self.assertTrue(path.exists())

    def test_save_includes_header_comment(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "mvs.lock"
            lf = Lockfile.create({"mvslovers/crent370": "1.0.0"}, "1.0.0")
            lf.save(path)
            content = path.read_text(encoding="utf-8")
            self.assertIn("AUTO-GENERATED", content)

    def test_save_includes_sections(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "mvs.lock"
            lf = Lockfile.create({"mvslovers/crent370": "1.0.0"}, "1.0.0")
            lf.save(path)
            content = path.read_text(encoding="utf-8")
            self.assertIn("[metadata]", content)
            self.assertIn("[dependencies]", content)

    def test_save_sorted_deps(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "mvs.lock"
            lf = Lockfile.create(
                {"mvslovers/zzz370": "1.0.0", "mvslovers/aaa370": "2.0.0"},
                "1.0.0",
            )
            lf.save(path)
            content = path.read_text(encoding="utf-8")
            idx_aaa = content.index("aaa370")
            idx_zzz = content.index("zzz370")
            self.assertLess(idx_aaa, idx_zzz)


class TestLockfileLoad(unittest.TestCase):

    def test_load_fixture(self):
        lf = Lockfile.load(FIXTURES / "mvs.lock")
        self.assertIsNotNone(lf)
        self.assertEqual(lf.mbt_version, "1.0.0")
        self.assertEqual(lf.generated, "2026-03-04T10:30:00Z")
        self.assertIn("mvslovers/crent370", lf.dependencies)
        self.assertEqual(lf.dependencies["mvslovers/crent370"], "1.0.0")

    def test_load_missing_returns_none(self):
        result = Lockfile.load(Path("/nonexistent/path/mvs.lock"))
        self.assertIsNone(result)

    def test_load_empty_deps(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "mvs.lock"
            lf = Lockfile.create({}, "1.0.0")
            lf.save(path)
            loaded = Lockfile.load(path)
            self.assertEqual(loaded.dependencies, {})


if __name__ == "__main__":
    unittest.main()
