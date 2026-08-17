"""Offline tests for MvsMFClient spool collection -- the readback (#87).

A failed readback used to become an empty string, which is exactly what a job
with no output produces.  mbttest then found no step RC and reported "no test
ran" for a job whose every step had ended RC 0000; that mistranslation cost a
full investigation (mvsmf#282) before the fault was found to be server-side.

These tests drive _collect_spool against a stubbed transport -- no MVS, no
sockets -- and assert the one thing that was missing: that "empty" and
"unreadable" come back as different results.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.mvsmf import MvsMFClient, MvsMFError, JobResult, _one_line

JOB = ("MBTTEST", "JOB01179")

# What mvsMF answers when it is abending under the job (mvsmf#217 + libc370#108).
HTTP_500 = "HTTP 500 Internal Server Error for GET {path}"


class _StubClient(MvsMFClient):
    """MvsMFClient with the two HTTP entry points replaced.

    files: the /files listing, or an MvsMFError instance to raise instead.
    records: {file id: text or MvsMFError instance}.
    """

    def __init__(self, files, records=None):
        super().__init__("localhost", 1080, "IBMUSER", "sys1")
        self._files = files
        self._records = records or {}
        self.requested = []

    def _json_request(self, method, path, body=None):
        self.requested.append(path)
        if isinstance(self._files, Exception):
            raise self._files
        return self._files

    def _request(self, method, path, body=None, **kw):
        self.requested.append(path)
        fid = path.rsplit("/files/", 1)[1].split("/")[0]
        out = self._records.get(fid, "")
        if isinstance(out, Exception):
            raise out
        return out.encode("utf-8")


def _listing(*dds):
    return {"items": [{"id": str(i), "ddname": dd}
                      for i, dd in enumerate(dds, 1)]}


class CollectSpoolTest(unittest.TestCase):
    def test_clean_readback_reports_no_errors(self):
        c = _StubClient(_listing("JESMSGLG", "JESYSMSG"),
                        {"1": "$HASP373", "2": "IEF142I ... COND CODE 0000"})
        text, errors = c._collect_spool(*JOB)
        self.assertEqual(errors, [])
        self.assertIn("--- JESMSGLG ---", text)
        self.assertIn("COND CODE 0000", text)

    def test_a_job_with_no_output_is_empty_without_errors(self):
        # The state the failure mode was confused with: nothing on the spool,
        # nothing wrong with the server.
        text, errors = _StubClient(_listing())._collect_spool(*JOB)
        self.assertEqual((text, errors), ("", []))

    def test_failed_listing_is_empty_WITH_an_error(self):
        err = MvsMFError(HTTP_500.format(
            path="/restjobs/jobs/MBTTEST/JOB01179/files"))
        text, errors = _StubClient(err)._collect_spool(*JOB)
        self.assertEqual(text, "")
        self.assertEqual(len(errors), 1)
        # the message has to name what was asked for and what came back
        self.assertIn("500", errors[0])
        self.assertIn("/files", errors[0])

    def test_listing_failure_carries_no_ddname_prefix(self):
        # Nothing is known about which DDs there were, and mbttest reads the
        # "{ddname}: " prefix to tell which part of the spool went missing.
        _text, errors = _StubClient(MvsMFError(
            HTTP_500.format(path="/restjobs/jobs/MBTTEST/JOB01179/files")
        ))._collect_spool(*JOB)
        self.assertTrue(errors[0].startswith("HTTP "), errors[0])

    def test_failed_records_call_is_reported_and_names_the_dd(self):
        err = MvsMFError(HTTP_500.format(
            path="/restjobs/jobs/MBTTEST/JOB01179/files/2/records"))
        c = _StubClient(_listing("JESMSGLG", "JESYSMSG"),
                        {"1": "$HASP373", "2": err})
        text, errors = c._collect_spool(*JOB)
        self.assertEqual(len(errors), 1)
        self.assertTrue(errors[0].startswith("JESYSMSG:"), errors[0])
        # what did come back is kept -- a partial spool still beats none
        self.assertIn("$HASP373", text)
        # and the hole stays visible in the saved text as well
        self.assertIn("FAILED TO RETRIEVE", text)

    def test_every_records_call_failing_reports_every_dd(self):
        errs = {str(i): MvsMFError("HTTP 500") for i in (1, 2, 3)}
        c = _StubClient(_listing("JESMSGLG", "JESYSMSG", "SYSPRINT"), errs)
        _text, errors = c._collect_spool(*JOB)
        self.assertEqual(len(errors), 3)

    def test_jes_only_does_not_report_the_dds_it_skipped(self):
        # A skipped DD was never requested, so it cannot have failed.
        c = _StubClient(_listing("JESMSGLG", "SYSPRINT"),
                        {"1": "$HASP373", "2": MvsMFError("HTTP 500")})
        text, errors = c._collect_spool(*JOB, jes_only=True)
        self.assertEqual(errors, [])
        self.assertIn("$HASP373", text)


class PublicWrapperTest(unittest.TestCase):
    """collect_spool() stays a str -- scripts/legacy/mvsasm.py calls it."""

    def test_returns_text_only(self):
        c = _StubClient(_listing("JESMSGLG"), {"1": "$HASP373"})
        out = c.collect_spool(*JOB)
        self.assertIsInstance(out, str)
        self.assertIn("$HASP373", out)

    def test_returns_text_only_when_the_readback_failed(self):
        out = _StubClient(MvsMFError("HTTP 500")).collect_spool(*JOB)
        self.assertEqual(out, "")


class JobResultTest(unittest.TestCase):
    def test_spool_errors_defaults_to_a_fresh_list(self):
        a = JobResult("J1", "N", 0, "CC", "")
        b = JobResult("J2", "N", 0, "CC", "")
        self.assertEqual(a.spool_errors, [])
        a.spool_errors.append("x")
        self.assertEqual(b.spool_errors, [], "default list is shared")

    def test_construction_without_errors_still_works(self):
        # tests/test_mbtdeploy.py and scripts/legacy/ build JobResult with the
        # original five fields; the new one must stay optional.
        r = JobResult(jobid="J", jobname="N", rc=0, status="CC", spool="s")
        self.assertEqual(r.spool_errors, [])


class OneLineTest(unittest.TestCase):
    """An abending mvsMF answers with a multi-line HTML body; a diagnosis
    prints one line per failed request."""

    def test_collapses_newlines(self):
        out = _one_line(MvsMFError("HTTP 500 for GET /x:\n<html>\n<body>\n"))
        self.assertNotIn("\n", out)
        self.assertIn("HTTP 500", out)

    def test_caps_length_keeping_the_front(self):
        out = _one_line(MvsMFError("HTTP 500 for GET /x: " + "y" * 500))
        self.assertLessEqual(len(out), 200)
        self.assertTrue(out.startswith("HTTP 500 for GET /x:"))
        self.assertTrue(out.endswith("..."))


if __name__ == "__main__":
    unittest.main()
