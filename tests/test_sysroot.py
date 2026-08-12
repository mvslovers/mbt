"""Tests for mbt.sysroot (issue #69: which libc370 is actually installed).

The sysroot carries no version marker, so the version is read from the build
stamp baked into libc.a -- EBCDIC-encoded, because it is a target string
constant. These tests cover the decoding and the tolerance the scrape needs:
libc370 changed the stamp's spelling once already ("uppercase the build stamp
and drop the 'v' before the version"), so both forms must still parse.
"""

import sys
import unittest
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.sysroot import installed_libc370, stamp_version


def ebcdic(text: str) -> bytes:
    return text.encode("cp037")


class TestStampVersion(unittest.TestCase):
    """Reading the version out of raw archive bytes."""

    def test_current_stamp_format(self):
        self.assertEqual(
            stamp_version(ebcdic("LIBC370 1.0.2-dev (5c0deeb)")), "1.0.2-dev"
        )

    def test_stable_version(self):
        self.assertEqual(
            stamp_version(ebcdic("LIBC370 1.0.2 (58767b3)")), "1.0.2"
        )

    def test_previous_stamp_format_still_parses(self):
        # Before libc370 5c0deeb the stamp was lowercase with a leading 'v'.
        self.assertEqual(
            stamp_version(ebcdic("libc370 v1.0.1 (abc1234)")), "1.0.1"
        )

    def test_dirty_tree_suffix(self):
        self.assertEqual(
            stamp_version(ebcdic("LIBC370 1.0.3-dev (f14ad04-dirty)")),
            "1.0.3-dev",
        )

    def test_found_amid_surrounding_object_code(self):
        blob = b"\x00\xff" * 500 + ebcdic("LIBC370 1.0.2 (58767b3)") + b"\x00" * 40
        self.assertEqual(stamp_version(blob), "1.0.2")

    def test_ascii_stamp_also_found(self):
        # Fallback encoding, in case libc370 ever emits a host-encoded stamp.
        self.assertEqual(stamp_version(b"LIBC370 1.2.3 (deadbee)"), "1.2.3")

    def test_no_stamp_is_none(self):
        self.assertIsNone(stamp_version(b"\x00\x01\x02" * 100))

    def test_unrelated_text_is_none(self):
        self.assertIsNone(stamp_version(ebcdic("HELLO 1.0.0 (abc)")))

    def test_version_without_patch_is_not_a_stamp(self):
        self.assertIsNone(stamp_version(ebcdic("LIBC370 1.0 (abc1234)")))


class TestInstalledLibc370(unittest.TestCase):
    """Reading it from a sysroot on disk."""

    def test_reads_from_sysroot(self):
        with tempfile.TemporaryDirectory() as d:
            lib = Path(d) / "lib"
            lib.mkdir()
            (lib / "libc.a").write_bytes(ebcdic("LIBC370 1.0.2 (58767b3)"))
            self.assertEqual(installed_libc370(d), "1.0.2")

    def test_missing_archive_is_none(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertIsNone(installed_libc370(d))

    def test_missing_sysroot_is_none(self):
        self.assertIsNone(installed_libc370("/nonexistent/sysroot"))


if __name__ == "__main__":
    unittest.main()
