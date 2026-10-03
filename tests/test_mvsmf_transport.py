"""Offline tests for MvsMFClient._request -- failures while reading (#108).

urlopen wraps connect-time failures in URLError, which _request turns into
MvsMFError.  A failure *after* the request went out -- the server is too slow
to answer, or drops the connection without one -- came through as a bare
TimeoutError / RemoteDisconnected, got past every 'except MvsMFError' in the
callers and ended the run with a traceback and exit 99, although the job on
MVS had finished (rexx370 make test-mvs, JOB01177).

These tests start a local socket server that misbehaves in exactly those two
ways and assert that the client raises MvsMFError naming method, path and,
for a timeout, the timeout used.  No MVS involved.
"""

import socket
import sys
import threading
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.mvsmf import MvsMFClient, MvsMFError


class _Server:
    """A one-shot TCP server on localhost.

    mode "slow": accept, read the request, answer nothing for `hold` seconds.
    mode "drop": accept, read the request, close without a response.
    """

    def __init__(self, mode: str, hold: float = 3.0):
        self.mode = mode
        self.hold = hold
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(1)
        self.port = self.sock.getsockname()[1]
        self.thread = threading.Thread(target=self._run, daemon=True)
        self.thread.start()

    def _run(self):
        try:
            conn, _ = self.sock.accept()
        except OSError:
            return
        with conn:
            conn.recv(65536)
            if self.mode == "slow":
                time.sleep(self.hold)
            # "drop": fall through and close at once

    def close(self):
        self.sock.close()


class TestReadFailures(unittest.TestCase):

    def _client(self, port):
        return MvsMFClient("127.0.0.1", port, "IBMUSER", "SYS1")

    def test_read_timeout_is_mvsmf_error(self):
        srv = _Server("slow", hold=3.0)
        try:
            with self.assertRaises(MvsMFError) as cm:
                self._client(srv.port)._request(
                    "GET", "/restjobs/jobs/MBTTEST/JOB01177", timeout=1)
            msg = str(cm.exception)
            self.assertIn("GET", msg)
            self.assertIn("/restjobs/jobs/MBTTEST/JOB01177", msg)
            self.assertIn("1s", msg)
        finally:
            srv.close()

    def test_connection_dropped_without_response_is_mvsmf_error(self):
        srv = _Server("drop")
        try:
            with self.assertRaises(MvsMFError) as cm:
                self._client(srv.port)._request(
                    "GET", "/restjobs/jobs/MBTTEST/JOB01177/files", timeout=5)
            msg = str(cm.exception)
            self.assertIn("GET", msg)
            self.assertIn("/restjobs/jobs/MBTTEST/JOB01177/files", msg)
        finally:
            srv.close()


if __name__ == "__main__":
    unittest.main()
