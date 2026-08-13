"""Tests for scripts/mbt/spool.py -- reading a job's spool output.

The IEF6nnI extraction is what turns "the job failed" into "the job failed
because of this"; it is shared by the test runner (#74) and the deploy's
RECEIVE (#78), so it lives in the package rather than in one executor.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.spool import JCL_ERROR_RE, MAX_DIAG, jcl_diagnostics


class JclErrorTest(unittest.TestCase):
    def test_matches_with_carriage_control_and_jes_prefix(self):
        line = " 6.41.22 JOB 1028  IEF452I MBTTEST  JOB NOT RUN - JCL ERROR"
        self.assertIsNotNone(JCL_ERROR_RE.search(line))

    def test_jobname_is_not_anchored(self):
        # submit_jcl falls back to "UNKNOWN" when the response omits the
        # jobname, so the pattern must not depend on knowing it.
        self.assertIsNotNone(
            JCL_ERROR_RE.search("IEF452I MBTDEPL  JOB NOT RUN - JCL ERROR"))

    def test_healthy_line_does_not_match(self):
        self.assertIsNone(JCL_ERROR_RE.search(
            "IEF142I MBTTEST B01 - STEP WAS EXECUTED - COND CODE 0000"))


class JclDiagnosticsTest(unittest.TestCase):
    def test_deduplicates_and_caps(self):
        spool = "\n".join([f"IEF6{i:02d}I MESSAGE {i}" for i in range(10, 20)]
                          + ["IEF610I MESSAGE 10"] * 3)
        out = jcl_diagnostics(spool)
        self.assertEqual(len(out), MAX_DIAG)
        self.assertEqual(len(set(out)), len(out))

    def test_survives_leading_carriage_control(self):
        spool = " 6.41.22 JOB 1028  IEF642I EXCESSIVE PARAMETER LENGTH\n"
        self.assertEqual(jcl_diagnostics(spool),
                         ["IEF642I EXCESSIVE PARAMETER LENGTH"])

    def test_statement_number_is_kept(self):
        # The interpreter's "STMT NO. MESSAGE" table -- the number points into
        # the generated JCL, so it is worth carrying over.
        spool = (" STMT NO. MESSAGE\n"
                 "        26 IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD\n")
        self.assertEqual(
            jcl_diagnostics(spool),
            ["IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD    (STMT 26)"])

    def test_same_message_at_two_statements_kept_apart(self):
        spool = ("        26 IEF642I EXCESSIVE PARAMETER LENGTH\n"
                 "        31 IEF642I EXCESSIVE PARAMETER LENGTH\n")
        self.assertEqual(len(jcl_diagnostics(spool)), 2)

    def test_nothing_in_a_clean_spool(self):
        self.assertEqual(jcl_diagnostics(""), [])
        self.assertEqual(
            jcl_diagnostics("IEF142I MBTTEST B01 - STEP WAS EXECUTED"), [])


if __name__ == "__main__":
    unittest.main()
