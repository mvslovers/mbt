"""Tests for mbtdeps._sha_drift_action (issue #52: rolling prereleases)
and mbtdeps._lock_matches (issue #29: lock vs. a changed constraint).

The lock pins each dependency by SHA256.  A drifted SHA is a hard error for
stable releases (immutable) but is expected for rolling -dev/-rcN prereleases
(the tag moves), where it must be accepted with a warning and re-pinned.
"""

import io
import os
import sys
import json
import tarfile
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

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


class TestLockMatches(unittest.TestCase):
    """Is a lock entry still usable for the constraint in project.toml?"""

    def _lock(self, version):
        return {"version": version, "sha256": OLD}

    # -- keep the pin -------------------------------------------------
    def test_pin_inside_range_is_kept(self):
        self.assertTrue(
            mbtdeps._lock_matches(self._lock("1.0.6"), ">=1.0.0"))

    def test_prerelease_pin_under_prerelease_range_is_kept(self):
        self.assertTrue(
            mbtdeps._lock_matches(self._lock("1.0.7-dev"), ">=1.0.7-dev"))

    def test_exact_prerelease_pin_is_kept(self):
        self.assertTrue(
            mbtdeps._lock_matches(self._lock("4.0.0-dev"), "=4.0.0-dev"))

    # -- re-resolve ---------------------------------------------------
    def test_issue_reproduction(self):
        # issue #29: >=1.0.6 pinned 1.0.6, constraint raised to >=1.0.7-dev
        self.assertFalse(
            mbtdeps._lock_matches(self._lock("1.0.6"), ">=1.0.7-dev"))

    def test_pin_below_raised_floor(self):
        # issue #29 comment: '>=4.1.0 -> 4.0.0-dev' was accepted, rc 0
        self.assertFalse(
            mbtdeps._lock_matches(self._lock("4.0.0-dev"), ">=4.1.0"))

    def test_pin_above_new_ceiling(self):
        self.assertFalse(
            mbtdeps._lock_matches(self._lock("2.0.0"), ">=1.0.0,<2.0.0"))

    def test_prerelease_pin_under_stable_range(self):
        # 1.5.0-dev satisfies >=1.0.0 numerically, but the resolver would
        # never pick a prerelease for a constraint that names none.
        self.assertFalse(
            mbtdeps._lock_matches(self._lock("1.5.0-dev"), ">=1.0.0"))

    def test_no_entry(self):
        self.assertFalse(mbtdeps._lock_matches(None, ">=1.0.0"))

    def test_entry_without_version(self):
        self.assertFalse(mbtdeps._lock_matches({"sha256": OLD}, ">=1.0.0"))

    def test_unparsable_version(self):
        self.assertFalse(
            mbtdeps._lock_matches(self._lock("not-a-version"), ">=1.0.0"))


class TestMainReResolve(unittest.TestCase):
    """main(): a stale lock entry is re-resolved, the others are kept."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self._cwd = os.getcwd()
        os.chdir(self.root)
        self.cache = self.root / "cache"
        self.resolved = []

    def tearDown(self):
        os.chdir(self._cwd)
        self._tmp.cleanup()

    def _lib(self, repo, version, payload):
        d = self.cache / repo / version
        d.mkdir(parents=True, exist_ok=True)
        top = f"{repo}-{version}"
        tgz = d / f"{top}-lib.tar.gz"
        with tarfile.open(tgz, "w:gz") as tar:
            data = payload.encode()
            info = tarfile.TarInfo(f"{top}/lib/{repo}.a")
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
        return mbtdeps._sha256(tgz)

    def _run(self, deps, lock, available):
        Path("project.toml").write_text(
            "[dependencies]\n"
            + "".join(f'"{k}" = "{v}"\n' for k, v in deps.items()))
        Path("mbt.lock").write_text(json.dumps(lock))

        def resolve(owner, repo, constraint):
            self.resolved.append(f"{owner}/{repo}")
            return available[f"{owner}/{repo}"]

        def download(owner, repo, version, force=False, warn=None):
            return self.cache / repo / version

        err = io.StringIO()
        with patch.object(mbtdeps, "_resolve_one", resolve), \
                patch.object(mbtdeps, "download_dependency", download), \
                patch.object(sys, "argv", ["mbtdeps"]), \
                redirect_stdout(io.StringIO()), redirect_stderr(err):
            rc = mbtdeps.main()
        return rc, err.getvalue(), json.loads(Path("mbt.lock").read_text())

    def test_raised_floor_reresolves_only_that_entry(self):
        sha_a_old = self._lib("a", "1.0.6", "a-1.0.6")
        sha_a_new = self._lib("a", "1.0.7", "a-1.0.7")
        sha_b = self._lib("b", "2.0.0", "b-2.0.0")
        rc, err, lock = self._run(
            {"o/a": ">=1.0.7", "o/b": ">=2.0.0"},
            {"o/a": {"version": "1.0.6", "sha256": sha_a_old},
             "o/b": {"version": "2.0.0", "sha256": sha_b}},
            {"o/a": "1.0.7", "o/b": "9.9.9"})
        # stable 1.0.6 -> 1.0.7 is a new version, not a drifted SHA
        self.assertEqual(rc, 0, err)
        self.assertEqual(self.resolved, ["o/a"])
        self.assertIn("locked '1.0.6' no longer fits '>=1.0.7'", err)
        self.assertEqual(lock["o/a"],
                         {"version": "1.0.7", "sha256": sha_a_new})
        self.assertEqual(lock["o/b"],
                         {"version": "2.0.0", "sha256": sha_b})

    def test_satisfied_lock_still_detects_stable_drift(self):
        self._lib("a", "1.0.6", "a-1.0.6")
        rc, err, _ = self._run(
            {"o/a": ">=1.0.0"},
            {"o/a": {"version": "1.0.6", "sha256": OLD}},
            {"o/a": "1.0.9"})
        self.assertEqual(rc, mbtdeps.EXIT_DEPENDENCY)
        self.assertEqual(self.resolved, [])
        self.assertIn("lib SHA changed", err)


if __name__ == "__main__":
    unittest.main()
