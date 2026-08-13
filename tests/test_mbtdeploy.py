"""Offline tests for scripts/mbtdeploy.py -- the RECEIVE diagnosis (#78).

A failed RECEIVE used to print result.rc raw:

    [mbt] ERROR: deploy failed: RECEIVE job failed (RC=9999) for IBMUSER.MBT.XMIT.IN

9998/9999/-1 are _parse_retcode's sentinels, not return codes, and four
different outcomes collapsed onto three of them.  _receive_failure() turns
result.status into a named reason; it takes no client and touches no
filesystem, so it is testable without MVS.
"""

import os
import sys
import tempfile
import types
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbtdeploy
from mbt.mvsmf import JobResult

TARGET = "IBMUSER.HTTPD.V4R0M0D.LINKLIB"
SPOOLF = "build/receive.spool"

JCL_ERROR_SPOOL = (
    " 6.41.22 JOB 1099  IEF452I MBTDEPL  JOB NOT RUN - JCL ERROR\n"
    " STMT NO. MESSAGE\n"
    "         2 IEF621I EXPECTED CONTINUATION NOT RECEIVED\n"
)


def _result(status, rc, spool=""):
    return JobResult(jobid="JOB01099", jobname="MBTDEPL", rc=rc,
                     status=status, spool=spool)


class ReceiveFailureTest(unittest.TestCase):
    def _call(self, status, rc, spool="", timeout=360):
        return mbtdeploy._receive_failure(_result(status, rc, spool), TARGET,
                                          timeout, SPOOLF)

    # -- clean --

    def test_rc0_is_not_a_failure(self):
        self.assertIsNone(self._call("CC", 0))

    def test_rc4_is_still_accepted(self):
        self.assertIsNone(self._call("CC", 4))

    # -- each outcome names itself --

    def test_rc8_names_the_return_code(self):
        head, details = self._call("CC", 8)
        self.assertIn("RC=8", head)
        self.assertIn("MBTDEPL JOB01099", head)
        self.assertIn("was not written", head)
        self.assertTrue(any(SPOOLF in d for d in details))

    def test_abend_says_abend_not_a_number(self):
        head, _ = self._call("ABEND", 9999)
        self.assertIn("abended", head)
        self.assertNotIn("9999", head)

    def test_jcl_error_carries_the_diagnostic_line(self):
        head, details = self._call("JCL ERROR", 9998, JCL_ERROR_SPOOL)
        self.assertIn("rejected", head)
        self.assertNotIn("9998", head)
        self.assertTrue(any("IEF621I" in d for d in details), details)
        self.assertTrue(any("(STMT 2)" in d for d in details), details)

    def test_jcl_error_detected_from_the_spool_alone(self):
        # MVS/CE returns retcode null for every job, so the status can be
        # UNKNOWN while the spool says plainly what happened.
        head, details = self._call("UNKNOWN", -1, JCL_ERROR_SPOOL)
        self.assertIn("rejected", head)
        self.assertTrue(any("IEF621I" in d for d in details))

    def test_unknown_without_a_spool_leaves_the_outcome_open(self):
        # _parse_spool_rc yields UNKNOWN when the job ended but its spool named
        # no outcome -- or could not be fetched at all.  That includes a
        # RECEIVE that in fact succeeded, so claiming the target is gone would
        # be wrong, and would invite the rerun that then really deletes it.
        head, details = self._call("UNKNOWN", -1)
        self.assertIn("MBTDEPL JOB01099", head)
        self.assertIn("UNKNOWN", head)
        self.assertNotIn("RC=-1", head)
        self.assertNotIn("was not written", head + " ".join(details))
        self.assertIn("before rerunning", " ".join(details))
        self.assertTrue(any(SPOOLF in d for d in details))

    # -- the dangerous one --

    def test_timeout_warns_against_a_blind_rerun(self):
        # The rerun deletes the target before its own RECEIVE, so it would
        # destroy a dataset the first job may still be writing (#57).
        head, details = self._call("TIMEOUT", 9999, timeout=360)
        self.assertIn("360s", head)
        self.assertIn("outcome unknown", head)
        joined = " ".join(details)
        self.assertIn("may still be running", joined)
        self.assertIn(TARGET, joined)
        self.assertIn("before rerunning", joined)
        self.assertIn("MBT_DEPLOY_TIMEOUT", joined)

    def test_timeout_does_not_claim_the_target_was_not_written(self):
        # It may well have been -- claiming otherwise is what invites the
        # destructive retry.
        head, details = self._call("TIMEOUT", 9999)
        self.assertNotIn("was not written", head + " ".join(details))

    # -- message plumbing --

    def test_only_ended_and_diagnosed_jobs_claim_the_target_is_gone(self):
        # The claim is load-bearing: it is what tells the user the previous
        # LINKLIB contents went with it.  It must appear for the three
        # diagnosed failures and for nothing else.
        for status, rc in (("JCL ERROR", 9998), ("ABEND", 9999), ("CC", 8)):
            with self.subTest(status=status):
                head, _ = self._call(status, rc)
                self.assertIn("was not written", head)
        for status, rc in (("TIMEOUT", 9999), ("UNKNOWN", -1), ("ACTIVE", -1)):
            with self.subTest(status=status):
                head, details = self._call(status, rc)
                self.assertNotIn("was not written", head + " ".join(details))

    def test_no_spool_path_means_no_dangling_hint(self):
        out = mbtdeploy._receive_failure(_result("ABEND", 9999), TARGET, 300, "")
        self.assertEqual(out[1], [])

    def test_details_survive_the_exception(self):
        head, details = self._call("ABEND", 9999)
        e = mbtdeploy.ReceiveError(head, details)
        self.assertEqual(str(e), head)
        self.assertEqual(e.details, details)
        # an 'except MvsMFError' that predates ReceiveError still works
        from mbt.mvsmf import MvsMFError
        self.assertIsInstance(e, MvsMFError)


class SpoolWriteTest(unittest.TestCase):
    """Keeping the spool must never be able to fail the deploy.

    OSError is not an MvsMFError, so neither call site catches it -- an
    unguarded write would exit 99 with a traceback on a RECEIVE that may well
    have succeeded, which is strictly worse than the message it replaced.
    """

    class _Client:
        def __init__(self, result):
            self.result = result

        def submit_jcl(self, jcl, timeout=120):
            return self.result

    def _run(self, result, spool_path):
        config = types.SimpleNamespace(
            jes_jobclass="A", jes_msgclass="X", deps_volume=None)
        return mbtdeploy._receive_xmit(
            self._Client(result), config, "IBMUSER.MBT.XMIT.IN", TARGET,
            spool_path=spool_path)

    def test_spool_is_written_on_a_clean_receive(self):
        # submit_jcl collects the spool whatever the outcome, so the file is
        # there after a success too -- no stale one survives from last time.
        with tempfile.TemporaryDirectory() as d:
            p = Path(d, "receive.spool")
            self._run(_result("CC", 0, "--- JESYSMSG ---\nIEF142I ..."), p)
            self.assertIn("IEF142I", p.read_text())

    def test_unwritable_path_warns_instead_of_crashing(self):
        with tempfile.TemporaryDirectory() as d:
            # a directory where the file should go -> OSError on write_text
            bad = Path(d, "receive.spool")
            bad.mkdir()
            rc = self._run(_result("CC", 0, "spool text"), bad)
            self.assertEqual(rc, 0)

    def test_unwritable_path_drops_the_hint_from_the_diagnosis(self):
        with tempfile.TemporaryDirectory() as d:
            bad = Path(d, "receive.spool")
            bad.mkdir()
            with self.assertRaises(mbtdeploy.ReceiveError) as cm:
                self._run(_result("ABEND", 9999, "spool text"), bad)
            self.assertIn("abended", str(cm.exception))
            self.assertEqual(
                [x for x in cm.exception.details if "receive.spool" in x], [],
                "pointed at a spool file that was never written")


class ReceiveTimeoutTest(unittest.TestCase):
    def setUp(self):
        self._saved = os.environ.pop("MBT_DEPLOY_TIMEOUT", None)

    def tearDown(self):
        os.environ.pop("MBT_DEPLOY_TIMEOUT", None)
        if self._saved is not None:
            os.environ["MBT_DEPLOY_TIMEOUT"] = self._saved

    def test_base_is_well_above_the_120s_default(self):
        # submit_jcl's flat 120 s is what made a still-running RECEIVE look
        # like a failed deploy.
        self.assertGreaterEqual(mbtdeploy._receive_timeout(0), 300)

    def test_scales_with_the_xmit(self):
        small = mbtdeploy._receive_timeout(100 * 1024)
        big = mbtdeploy._receive_timeout(2 * 1024 * 1024)   # ~rexx370
        self.assertGreater(big, small)

    def test_env_override_wins(self):
        os.environ["MBT_DEPLOY_TIMEOUT"] = "42"
        self.assertEqual(mbtdeploy._receive_timeout(10 * 1024 * 1024), 42)

    def test_empty_or_zero_env_falls_back(self):
        for value in ("", "0"):
            os.environ["MBT_DEPLOY_TIMEOUT"] = value
            self.assertGreaterEqual(mbtdeploy._receive_timeout(0), 300)


if __name__ == "__main__":
    unittest.main()
