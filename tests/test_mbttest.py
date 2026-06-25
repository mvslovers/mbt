"""Offline tests for scripts/mbttest.py -- the MVS test runner.

Covers the pure pieces (no MVS contact): runner-JCL generation and per-step
RC parsing. The end-to-end deploy+submit path is validated against a live
system separately.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbttest

JC = "//MBTTEST  JOB (A),'T'"
TESTLIB = "IBMUSER.REXX370.V1R0M0D.TESTLIB"
LINKLIB = "IBMUSER.REXX370.V1R0M0D.LINKLIB"


class GenRunnerTest(unittest.TestCase):
    def setUp(self):
        self.jcl, self.smap = mbttest._gen_runner(
            JC, ["TSTTOKN", "TSTFIND"], TESTLIB, LINKLIB)

    def test_batch_and_tso_step_per_test(self):
        # 2 tests -> 2 batch (B01,B02) + 2 tso (T01,T02)
        self.assertEqual(
            {("TSTTOKN", "batch"), ("TSTFIND", "batch"),
             ("TSTTOKN", "tso"), ("TSTFIND", "tso")},
            set(self.smap.values()))
        self.assertEqual(self.smap["B01"], ("TSTTOKN", "batch"))
        self.assertEqual(self.smap["T01"], ("TSTTOKN", "tso"))

    def test_batch_step_form(self):
        self.assertIn("//B01     EXEC PGM=TSTTOKN,COND=EVEN,REGION=", self.jcl)

    def test_tso_step_uses_ikjeft01_call(self):
        self.assertIn("//T01     EXEC PGM=IKJEFT01", self.jcl)
        self.assertIn(f" CALL '{TESTLIB}(TSTTOKN)'", self.jcl)

    def test_steplib_concatenates_testlib_then_linklib(self):
        self.assertIn(f"//STEPLIB  DD DSN={TESTLIB},DISP=SHR", self.jcl)
        self.assertIn(f"//         DD DSN={LINKLIB},DISP=SHR", self.jcl)

    def test_region_is_concrete_not_zero(self):
        # MVS 3.8j needs a concrete REGION (0M -> 512K default -> S878)
        self.assertNotIn("REGION=0M", self.jcl)
        self.assertIn(f"REGION={mbttest.RUNNER_REGION}", self.jcl)


class FixtureRunnerTest(unittest.TestCase):
    def setUp(self):
        self.fix = {
            "TSTLOAD": {
                "pds": "IBMUSER.REXX370.FIX.TSTLOAD",
                "dds": ["SYSEXEC", "ALTDD"],
                "members": [("HELLO", "/* c */\nsay 'hi'\n"), ("EMPTY", "")],
            }
        }
        self.jcl, self.smap = mbttest._gen_runner(
            JC, ["TSTLOAD", "TSTTOKN"], TESTLIB, LINKLIB, self.fix)

    def test_iebgener_load_step_per_member(self):
        self.assertIn("EXEC PGM=IEBGENER", self.jcl)
        self.assertIn("//SYSUT2   DD DSN=IBMUSER.REXX370.FIX.TSTLOAD(HELLO),DISP=SHR", self.jcl)
        self.assertIn("//SYSUT2   DD DSN=IBMUSER.REXX370.FIX.TSTLOAD(EMPTY),DISP=SHR", self.jcl)

    def test_dlm_lets_rexx_comment_pass(self):
        # the '/* c */' content must survive (DLM moves the terminator off '/*')
        self.assertIn(f"//SYSUT1   DD *,DLM={mbttest._FIX_DLM}", self.jcl)
        self.assertIn("/* c */", self.jcl)

    def test_both_dds_added_to_fixture_test_steps(self):
        self.assertIn("//SYSEXEC  DD DSN=IBMUSER.REXX370.FIX.TSTLOAD,DISP=SHR", self.jcl)
        self.assertIn("//ALTDD    DD DSN=IBMUSER.REXX370.FIX.TSTLOAD,DISP=SHR", self.jcl)

    def test_non_fixture_test_gets_no_fixture_dd(self):
        # TSTTOKN has no fixture -> no FIX.TSTLOAD DD leaks into its step
        toks = self.jcl.split("//B02")[1].split("//T01")[0]  # TSTTOKN batch step
        self.assertNotIn("FIX.TSTLOAD", toks)


class ParseStepRcTest(unittest.TestCase):
    def test_cond_code(self):
        s = "IEF142I MBTTEST B01 - STEP WAS EXECUTED - COND CODE 0000"
        self.assertEqual(mbttest._parse_step_rc(s, "MBTTEST", "B01"), (0, "CC"))

    def test_cond_code_nonzero(self):
        s = "IEF142I MBTTEST T01 - STEP WAS EXECUTED - COND CODE 0012"
        self.assertEqual(mbttest._parse_step_rc(s, "MBTTEST", "T01"), (12, "CC"))

    def test_abend(self):
        s = "IEF450I MBTTEST B01 - ABEND S806 U0000 - TIME=15.54.28"
        rc, st = mbttest._parse_step_rc(s, "MBTTEST", "B01")
        self.assertEqual(rc, 9999)
        self.assertIn("S806", st)

    def test_missing(self):
        self.assertEqual(mbttest._parse_step_rc("", "MBTTEST", "B01"),
                         (None, "NO RC"))


class AssertionCountTest(unittest.TestCase):
    def test_pass_fail_counting(self):
        spool = "  PASS: a\n  PASS: b\n  FAIL: c\n=== 2/3 passed ===\n"
        self.assertEqual(len(mbttest._PASS.findall(spool)), 2)
        self.assertEqual(len(mbttest._FAIL.findall(spool)), 1)


if __name__ == "__main__":
    unittest.main()
