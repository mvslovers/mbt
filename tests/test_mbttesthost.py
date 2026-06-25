"""Offline tests for scripts/mbttesthost.py (host include flags + tallying).

The end-to-end build+run path is validated against a live host compiler
separately; these cover the pure pieces.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbttesthost


class HostCflagsTest(unittest.TestCase):
    def test_reuses_build_cflags_and_adds_mbt_include(self):
        cfg = {"build": {"cflags": ["-std=gnu99", "-I", "include"]},
               "host": {"cflags": ["-Wall"]}}
        mbt_inc = Path(mbttesthost.__file__).resolve().parent.parent / "include"
        flags = mbttesthost._host_cflags(cfg, mbt_inc)
        self.assertEqual(flags[:3], ["-std=gnu99", "-I", "include"])
        self.assertIn("-Wall", flags)            # [host].cflags appended
        if mbt_inc.is_dir():
            self.assertIn(str(mbt_inc), flags)   # mbt/include for mbtcheck.h


class TallyTest(unittest.TestCase):
    def test_pass_fail_counting(self):
        spool = "  PASS: a\n  PASS: b\n  FAIL: c\n=== 2/3 passed ===\n"
        self.assertEqual(len(mbttesthost._PASS.findall(spool)), 2)
        self.assertEqual(len(mbttesthost._FAIL.findall(spool)), 1)


if __name__ == "__main__":
    unittest.main()
