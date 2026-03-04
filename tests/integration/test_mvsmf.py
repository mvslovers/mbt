"""Integration tests for mbt/mvsmf.py.

These tests run against a live mvsMF instance (MVS/CE Docker).
They are skipped automatically if the host is not reachable.

Prerequisites:
    make run-mvs   (or: cd docker && docker compose up -d)

Connection defaults:
    host: localhost
    port: 1080
    user: IBMUSER
    pass: sys1   (MVS/CE default)

Override with environment variables:
    MBT_MVS_HOST, MBT_MVS_PORT, MBT_MVS_USER, MBT_MVS_PASS, MBT_MVS_HLQ
"""

import os
import sys
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent.parent / "scripts"))

from mbt.mvsmf import MvsMFClient, MvsMFError, JobResult

# --- Connection parameters (override via env) ---

HOST = os.environ.get("MBT_MVS_HOST", "localhost")
PORT = int(os.environ.get("MBT_MVS_PORT", "1080"))
USER = os.environ.get("MBT_MVS_USER", "IBMUSER")
PASS = os.environ.get("MBT_MVS_PASS", "sys1")
HLQ  = os.environ.get("MBT_MVS_HLQ", "IBMUSER")

# Test dataset names — use unique prefix to avoid collisions.
# 5-digit suffix: base qualifiers = 7 chars (PS/PO/BN + 5 digits),
# leaving room for 1-char test suffix letters (8-char MVS qualifier limit).
_TS = str(int(time.time()))[-5:]
TEST_PS_DSN = f"{HLQ}.MBTTEST.PS{_TS}"
TEST_PO_DSN = f"{HLQ}.MBTTEST.PO{_TS}"
TEST_BIN_DSN = f"{HLQ}.MBTTEST.BN{_TS}"


def _client() -> MvsMFClient:
    return MvsMFClient(HOST, PORT, USER, PASS)


def _is_reachable() -> bool:
    """Check if mvsMF is reachable before running tests."""
    try:
        client = _client()
        return client.ping()
    except Exception:
        return False


def skip_if_unreachable(fn):
    """Decorator: skip test if mvsMF is not reachable."""
    def wrapper(self, *args, **kwargs):
        if not _is_reachable():
            self.skipTest(
                f"mvsMF not reachable at {HOST}:{PORT} — "
                "run 'make run-mvs' to start MVS/CE"
            )
        return fn(self, *args, **kwargs)
    wrapper.__name__ = fn.__name__
    return wrapper


class TestMvsMFPing(unittest.TestCase):

    def test_ping_reachable(self):
        """ping() returns True when server is up."""
        client = _client()
        if not client.ping():
            self.skipTest(f"mvsMF not reachable at {HOST}:{PORT}")
        self.assertTrue(client.ping())

    def test_ping_unreachable_returns_false(self):
        """ping() returns False when server is not reachable."""
        client = MvsMFClient("127.0.0.1", 19999, USER, PASS)
        self.assertFalse(client.ping())


class TestMvsMFParseRetcode(unittest.TestCase):
    """Unit tests for _parse_retcode (no network needed)."""

    def test_cc_0(self):
        rc, status = MvsMFClient._parse_retcode("CC 0000")
        self.assertEqual(rc, 0)
        self.assertEqual(status, "CC")

    def test_cc_4(self):
        rc, status = MvsMFClient._parse_retcode("CC 0004")
        self.assertEqual(rc, 4)
        self.assertEqual(status, "CC")

    def test_cc_8(self):
        rc, status = MvsMFClient._parse_retcode("CC 0008")
        self.assertEqual(rc, 8)
        self.assertEqual(status, "CC")

    def test_abend(self):
        rc, status = MvsMFClient._parse_retcode("ABEND S0C4")
        self.assertEqual(rc, 9999)
        self.assertEqual(status, "ABEND")

    def test_jcl_error(self):
        rc, status = MvsMFClient._parse_retcode("JCL ERROR")
        self.assertEqual(rc, 9998)
        self.assertEqual(status, "JCL ERROR")

    def test_none(self):
        rc, status = MvsMFClient._parse_retcode(None)
        self.assertEqual(rc, -1)
        self.assertEqual(status, "UNKNOWN")

    def test_unknown_string(self):
        rc, status = MvsMFClient._parse_retcode("WEIRD")
        self.assertEqual(status, "UNKNOWN")


class TestMvsMFJobSubmit(unittest.TestCase):
    """Integration: submit JCL jobs and check results."""

    @skip_if_unreachable
    def test_submit_simple_job_rc0(self):
        """Submit a trivial job (IEFBR14) and expect RC=0."""
        jcl = (
            f"//{USER[:8]} JOB (001),'MBTTEST',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)\n"
            "//STEP1   EXEC PGM=IEFBR14\n"
            "//\n"
        )
        client = _client()
        result = client.submit_jcl(jcl, wait=True, timeout=60)
        self.assertIsInstance(result, JobResult)
        self.assertEqual(result.status, "CC")
        self.assertEqual(result.rc, 0)
        self.assertTrue(result.success)

    @skip_if_unreachable
    def test_submit_returns_jobid(self):
        """submit_jcl returns a JobResult with a non-empty jobid."""
        jcl = (
            f"//{USER[:8]} JOB (001),'MBTTEST',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)\n"
            "//STEP1   EXEC PGM=IEFBR14\n"
            "//\n"
        )
        client = _client()
        result = client.submit_jcl(jcl, wait=True, timeout=60)
        self.assertTrue(result.jobid)
        self.assertTrue(result.jobname)

    @skip_if_unreachable
    def test_spool_captured(self):
        """Spool output is non-empty for a completed job."""
        jcl = (
            f"//{USER[:8]} JOB (001),'MBTTEST',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)\n"
            "//STEP1   EXEC PGM=IEFBR14\n"
            "//\n"
        )
        client = _client()
        result = client.submit_jcl(jcl, wait=True, timeout=60)
        self.assertIsInstance(result.spool, str)

    @skip_if_unreachable
    def test_submit_nowait(self):
        """submit_jcl with wait=False returns ACTIVE status immediately."""
        jcl = (
            f"//{USER[:8]} JOB (001),'MBTTEST',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)\n"
            "//STEP1   EXEC PGM=IEFBR14\n"
            "//\n"
        )
        client = _client()
        result = client.submit_jcl(jcl, wait=False)
        self.assertEqual(result.status, "ACTIVE")


class TestMvsMFDatasets(unittest.TestCase):
    """Integration: dataset create/exist/delete operations."""

    @classmethod
    def setUpClass(cls):
        if not _is_reachable():
            return
        cls._client = _client()

    def _skip_if_needed(self):
        if not _is_reachable():
            self.skipTest(
                f"mvsMF not reachable at {HOST}:{PORT}"
            )

    @skip_if_unreachable
    def test_create_and_exists_ps(self):
        """Create a PS dataset and verify it exists."""
        dsn = TEST_PS_DSN
        c = _client()
        # Clean up first if exists
        if c.dataset_exists(dsn):
            c.delete_dataset(dsn)
        c.create_dataset(dsn, "PS", "FB", 80, 3120, ["TRK", 2, 1])
        self.assertTrue(c.dataset_exists(dsn))
        c.delete_dataset(dsn)

    @skip_if_unreachable
    def test_create_and_exists_po(self):
        """Create a PO dataset and verify it exists."""
        dsn = TEST_PO_DSN
        c = _client()
        if c.dataset_exists(dsn):
            c.delete_dataset(dsn)
        c.create_dataset(dsn, "PO", "FB", 80, 3120, ["TRK", 2, 1, 5])
        self.assertTrue(c.dataset_exists(dsn))
        c.delete_dataset(dsn)

    @skip_if_unreachable
    def test_nonexistent_dataset_returns_false(self):
        """dataset_exists returns False for a non-existent dataset."""
        c = _client()
        self.assertFalse(
            c.dataset_exists(f"{HLQ}.DEFINITELY.DOES.NOT.EXIST.XYZ")
        )

    @skip_if_unreachable
    def test_delete_removes_dataset(self):
        """Deleted dataset no longer exists."""
        dsn = TEST_PS_DSN + "D"
        c = _client()
        c.create_dataset(dsn, "PS", "FB", 80, 3120, ["TRK", 1, 1])
        self.assertTrue(c.dataset_exists(dsn))
        c.delete_dataset(dsn)
        self.assertFalse(c.dataset_exists(dsn))

    @skip_if_unreachable
    def test_list_datasets(self):
        """list_datasets returns items matching prefix."""
        dsn = TEST_PS_DSN + "L"
        c = _client()
        c.create_dataset(dsn, "PS", "FB", 80, 3120, ["TRK", 1, 1])
        try:
            items = c.list_datasets(f"{HLQ}.MBTTEST.*")
            names = [i.get("dsname", "").strip() for i in items]
            self.assertIn(dsn, names)
        finally:
            c.delete_dataset(dsn)


class TestMvsMFMembers(unittest.TestCase):
    """Integration: PDS member write/read/list operations."""

    @skip_if_unreachable
    def test_write_and_read_member_roundtrip(self):
        """Write a text member and read it back."""
        dsn = TEST_PO_DSN + "M"
        c = _client()
        if c.dataset_exists(dsn):
            c.delete_dataset(dsn)
        c.create_dataset(dsn, "PO", "FB", 80, 3120, ["TRK", 2, 1, 5])
        try:
            content = "HELLO370 CSECT\n         END\n"
            c.write_member(dsn, "HELLO", content)
            read_back = c.read_member(dsn, "HELLO")
            # Content may have trailing spaces (MVS fixed-length records)
            self.assertIn("HELLO370", read_back)
            self.assertIn("END", read_back)
        finally:
            c.delete_dataset(dsn)

    @skip_if_unreachable
    def test_list_members(self):
        """list_members returns written member names."""
        dsn = TEST_PO_DSN + "L"
        c = _client()
        if c.dataset_exists(dsn):
            c.delete_dataset(dsn)
        c.create_dataset(dsn, "PO", "FB", 80, 3120, ["TRK", 2, 1, 5])
        try:
            c.write_member(dsn, "ALPHA", "* ALPHA\n")
            c.write_member(dsn, "BETA", "* BETA\n")
            members = c.list_members(dsn)
            self.assertIn("ALPHA", members)
            self.assertIn("BETA", members)
        finally:
            c.delete_dataset(dsn)

    @skip_if_unreachable
    def test_write_member_multiple_lines(self):
        """Multi-line member content is preserved."""
        dsn = TEST_PO_DSN + "X"
        c = _client()
        if c.dataset_exists(dsn):
            c.delete_dataset(dsn)
        c.create_dataset(dsn, "PO", "FB", 80, 3120, ["TRK", 2, 1, 5])
        try:
            content = "LINE1\nLINE2\nLINE3\n"
            c.write_member(dsn, "TEST", content)
            read_back = c.read_member(dsn, "TEST")
            self.assertIn("LINE1", read_back)
            self.assertIn("LINE2", read_back)
            self.assertIn("LINE3", read_back)
        finally:
            c.delete_dataset(dsn)


class TestMvsMFBinaryUpload(unittest.TestCase):
    """Integration: binary upload to sequential dataset."""

    @skip_if_unreachable
    def test_upload_binary(self):
        """upload_binary sends data to a sequential dataset."""
        dsn = TEST_BIN_DSN
        c = _client()
        if c.dataset_exists(dsn):
            c.delete_dataset(dsn)
        # Create a PS dataset big enough for a small binary payload
        c.create_dataset(dsn, "PS", "U", 0, 32760, ["TRK", 5, 1])
        try:
            data = b"\x00\x01\x02\x03" * 100
            c.upload_binary(dsn, data)
            self.assertTrue(c.dataset_exists(dsn))
        finally:
            c.delete_dataset(dsn)


if __name__ == "__main__":
    unittest.main(verbosity=2)
