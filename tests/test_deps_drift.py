"""Tests for mbtdeps._sha_drift_action (issue #52: rolling prereleases).

The lock pins each dependency by SHA256.  A drifted SHA is a hard error for
stable releases (immutable) but is expected for rolling -dev/-rcN prereleases
(the tag moves), where it must be accepted with a warning and re-pinned.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

import mbtdeps


OLD = "a" * 64
NEW = "b" * 64


class TestShaDriftAction(unittest.TestCase):

    # -- no drift to act on -------------------------------------------
    def test_no_lock_is_ok(self):
        self.assertEqual(
            mbtdeps._sha_drift_action(None, NEW, is_pre=False, update=False),
            "ok")

    def test_matching_sha_is_ok(self):
        locked = {"version": "1.0.0", "sha256": OLD}
        self.assertEqual(
            mbtdeps._sha_drift_action(locked, OLD, is_pre=False, update=False),
            "ok")

    def test_update_mode_ignores_drift(self):
        # --update deliberately re-resolves + re-pins, so drift is never fatal.
        locked = {"version": "1.0.0", "sha256": OLD}
        self.assertEqual(
            mbtdeps._sha_drift_action(locked, NEW, is_pre=False, update=True),
            "ok")

    def test_lock_without_sha_is_ok(self):
        # A lock entry that never recorded a SHA cannot drift.
        locked = {"version": "1.0.0"}
        self.assertEqual(
            mbtdeps._sha_drift_action(locked, NEW, is_pre=True, update=False),
            "ok")

    # -- stable drift -> hard error -----------------------------------
    def test_stable_drift_errors(self):
        locked = {"version": "1.0.0", "sha256": OLD}
        self.assertEqual(
            mbtdeps._sha_drift_action(locked, NEW, is_pre=False, update=False),
            "error")

    # -- rolling prerelease drift -> warn + accept --------------------
    def test_prerelease_drift_warns(self):
        locked = {"version": "1.0.0-dev", "sha256": OLD}
        self.assertEqual(
            mbtdeps._sha_drift_action(locked, NEW, is_pre=True, update=False),
            "warn")

    def test_rc_prerelease_drift_warns(self):
        # -rcN counts as a prerelease too (can be re-pushed while stabilizing).
        locked = {"version": "2.0.0-rc1", "sha256": OLD}
        self.assertEqual(
            mbtdeps._sha_drift_action(locked, NEW, is_pre=True, update=False),
            "warn")


if __name__ == "__main__":
    unittest.main()
