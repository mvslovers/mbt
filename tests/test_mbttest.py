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

    def test_steplib_testlib_only_when_no_linklib(self):
        # linklib=None (nothing deployed yet) -> STEPLIB is TESTLIB alone,
        # no dangling concatenation DD.
        jcl, _ = mbttest._gen_runner(JC, ["TSTTOKN"], TESTLIB, None)
        self.assertIn(f"//STEPLIB  DD DSN={TESTLIB},DISP=SHR", jcl)
        self.assertNotIn(",DISP=SHR\n//         DD DSN=", jcl)

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


class PerLegParmTest(unittest.TestCase):
    def setUp(self):
        self.parms = {"TISTSO": {"batch": "0", "tso": "1"}}
        self.jcl, _ = mbttest._gen_runner(
            JC, ["TISTSO", "TSTTOKN"], TESTLIB, LINKLIB, None, self.parms)

    def test_batch_parm_on_exec(self):
        self.assertIn("//B01     EXEC PGM=TISTSO,COND=EVEN,REGION=", self.jcl)
        self.assertIn(",PARM='0'", self.jcl)

    def test_tso_arg_on_call(self):
        self.assertIn(f" CALL '{TESTLIB}(TISTSO)' '1'", self.jcl)

    def test_no_parm_test_unaffected(self):
        # TSTTOKN has no parm -> plain EXEC and plain CALL
        self.assertIn("//B02     EXEC PGM=TSTTOKN,COND=EVEN,REGION=", self.jcl)
        self.assertIn(f" CALL '{TESTLIB}(TSTTOKN)'\n", self.jcl)


class ResolveParmsTest(unittest.TestCase):
    def test_per_leg_overrides_and_common(self):
        proj = {"test": [
            {"name": "TISTSO", "parm_batch": "0", "parm_tso": "1"},
            {"name": "FOO", "parm": "X"},          # common -> both legs
            {"name": "BAR"},                        # none
        ]}
        out = mbttest._resolve_parms(proj, ["TISTSO", "FOO", "BAR"])
        self.assertEqual(out["TISTSO"], {"batch": "0", "tso": "1"})
        self.assertEqual(out["FOO"], {"batch": "X", "tso": "X"})
        self.assertNotIn("BAR", out)


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


# -- Job-level failure (issue #74) -------------------------------------------
#
# When the job fails as a whole -- JES rejects it for a JCL error, or the poll
# expires -- no step has an IEF142I/IEF450I/IEF272I line, every step parses as
# NO RC, and the matrix used to print a full column of FAIL for tests of which
# none was broken and none had run.  In httpd#170 that read like three broken
# tests plus contagion inside the job and was chased through job splits, step
# counts and MBT_TEST_TIMEOUT; the cause was a nine-character name.

# The real thing, from the JESMSGLG/JESYSMSG of a rejected runner (httpd#180).
JCL_ERROR_SPOOL = """--- JESMSGLG ---

                                                J E S 2   J O B   L O G
 6.41.22 JOB 1028  $HASP373 MBTTEST  STARTED - INIT  1 - CLASS A - SYS MVSC
 6.41.22 JOB 1028  IEF452I MBTTEST  JOB NOT RUN - JCL ERROR
 6.41.22 JOB 1028  $HASP395 MBTTEST  ENDED
--- JESYSMSG ---
 STMT NO. MESSAGE
        26 IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD
"""

RUNNER = "build/test-runner.jcl"
SPOOLF = "build/test-runner.spool"


def _rows(*legs):
    """rows for one test from (rc, status) pairs: {'T': {'batch':…, 'tso':…}}."""
    return {"TSTX": {"batch": legs[0], "tso": legs[1]}}


class JobFailureTest(unittest.TestCase):
    def _call(self, status, spool, rows, timeout=300):
        return mbttest._job_failure(status, spool, rows, "MBTTEST", "JOB01028",
                                    timeout, RUNNER, SPOOLF)

    # -- fires only when nothing ran --

    def test_jcl_error_reports_the_reason(self):
        out = self._call("JCL ERROR", JCL_ERROR_SPOOL,
                         _rows((None, "NO RC"), (None, "NO RC")))
        self.assertIsNotNone(out)
        head, details = out
        self.assertIn("rejected", head)
        self.assertIn("JOB01028", head)
        # the line that actually names the cause must be in the output
        self.assertTrue(any("IEF642I" in d for d in details), details)
        self.assertTrue(any("EXCESSIVE PARAMETER LENGTH" in d for d in details))
        self.assertTrue(any(RUNNER in d for d in details))

    def test_jcl_error_detected_from_spool_when_status_disagrees(self):
        # _parse_spool_rc searches the whole spool for COND CODE before it
        # looks for IEF452I, so the status can say something else entirely.
        out = self._call("CC", JCL_ERROR_SPOOL,
                         _rows((None, "NO RC"), (None, "NO RC")))
        self.assertIsNotNone(out)
        self.assertIn("rejected", out[0])

    def test_jcl_error_detected_from_status_when_spool_is_empty(self):
        out = self._call("JCL ERROR", "",
                         _rows((None, "NO RC"), (None, "NO RC")))
        self.assertIsNotNone(out)
        self.assertIn("rejected", out[0])

    def test_timeout_says_timeout_not_failed_tests(self):
        out = self._call("TIMEOUT", "", _rows((None, "NO RC"), (None, "NO RC")),
                         timeout=420)
        self.assertIsNotNone(out)
        head, details = out
        self.assertIn("420s", head)
        self.assertTrue(any("MBT_TEST_TIMEOUT" in d for d in details))

    def test_unexplained_empty_spool_still_names_the_job(self):
        out = self._call("UNKNOWN", "", _rows((None, "NO RC"), (None, "NO RC")))
        self.assertIsNotNone(out)
        head, details = out
        self.assertIn("no return code for any step", head)
        self.assertTrue(any(SPOOLF in d for d in details))

    # -- stays quiet whenever the steps did produce verdicts --

    def test_all_steps_abended_is_test_level_not_job_level(self):
        # Every step ABENDed -> IEF450I verdicts exist; that IS test-level
        # information and the matrix must still print.
        out = self._call("ABEND", "IEF450I ...",
                         _rows((9999, "ABEND S806"), (9999, "ABEND S806")))
        self.assertIsNone(out)

    def test_partial_no_rc_still_prints_the_matrix(self):
        out = self._call("CC", "IEF142I ...",
                         _rows((0, "CC"), (None, "NO RC")))
        self.assertIsNone(out)

    def test_not_executed_is_a_verdict(self):
        out = self._call("CC", "IEF272I ...",
                         _rows((None, "NOT EXECUTED"), (None, "NOT EXECUTED")))
        self.assertIsNone(out)

    def test_empty_rows_never_reports(self):
        # all() over nothing is True -- guard against a future refactor
        # turning "no tests" into a spurious job-level failure.
        self.assertIsNone(self._call("CC", "", {}))


class HealthySpoolTest(unittest.TestCase):
    """The negative control: a real 30-step runner spool from a green
    `make test-mvs` (httpd) must not trip the job-level guard.

    The fixture is that spool reduced to its JES/IEF system messages -- the
    tests' own WTO output is what the runner ignores anyway, and it is not
    this repo's to carry.
    """

    SPOOL = Path(__file__).parent / "fixtures" / "spool-healthy.txt"

    def setUp(self):
        self.spool = self.SPOOL.read_text()

    def test_every_step_has_a_verdict(self):
        rows = {}
        for i in range(1, 16):
            rows[f"T{i:02d}"] = {
                "batch": mbttest._parse_step_rc(self.spool, "MBTTEST", f"B{i:02d}"),
            }
        statuses = {st for legs in rows.values() for (_rc, st) in legs.values()}
        self.assertEqual(statuses, {"CC"}, "real spool did not parse as executed")

    def test_guard_stays_quiet(self):
        rows = {
            f"T{i:02d}": {
                "batch": mbttest._parse_step_rc(self.spool, "MBTTEST", f"B{i:02d}"),
                "tso": mbttest._parse_step_rc(self.spool, "MBTTEST", f"T{i:02d}"),
            }
            for i in range(1, 16)
        }
        self.assertIsNone(
            mbttest._job_failure("CC", self.spool, rows, "MBTTEST", "JOB01032",
                                 300, RUNNER, SPOOLF))

    def test_no_false_jcl_error_from_a_healthy_spool(self):
        self.assertIsNone(mbttest._JCL_ERROR_RE.search(self.spool))
        self.assertEqual(mbttest._jcl_diagnostics(self.spool), [])


class JclDiagnosticsTest(unittest.TestCase):
    def test_deduplicates_and_caps(self):
        spool = "\n".join([f"IEF6{i:02d}I MESSAGE {i}" for i in range(10, 20)]
                          + ["IEF610I MESSAGE 10"] * 3)
        out = mbttest._jcl_diagnostics(spool)
        self.assertEqual(len(out), mbttest._MAX_DIAG)
        self.assertEqual(len(set(out)), len(out))

    def test_survives_leading_carriage_control(self):
        spool = " 6.41.22 JOB 1028  IEF642I EXCESSIVE PARAMETER LENGTH\n"
        self.assertEqual(mbttest._jcl_diagnostics(spool),
                         ["IEF642I EXCESSIVE PARAMETER LENGTH"])

    def test_statement_number_is_kept(self):
        # The interpreter's "STMT NO. MESSAGE" table -- the number points into
        # the generated runner, so it is worth carrying over.
        spool = (" STMT NO. MESSAGE\n"
                 "        26 IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD\n")
        self.assertEqual(
            mbttest._jcl_diagnostics(spool),
            ["IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD    (STMT 26)"])

    def test_same_message_at_two_statements_kept_apart(self):
        spool = ("        26 IEF642I EXCESSIVE PARAMETER LENGTH\n"
                 "        31 IEF642I EXCESSIVE PARAMETER LENGTH\n")
        self.assertEqual(len(mbttest._jcl_diagnostics(spool)), 2)


class AssertionCountTest(unittest.TestCase):
    def test_pass_fail_counting(self):
        spool = "  PASS: a\n  PASS: b\n  FAIL: c\n=== 2/3 passed ===\n"
        self.assertEqual(len(mbttest._PASS.findall(spool)), 2)
        self.assertEqual(len(mbttest._FAIL.findall(spool)), 1)


if __name__ == "__main__":
    unittest.main()
