"""prerelease may only move the tags of the project that owns the repository (#85).

prerelease deletes its tag v<version> locally and on origin and creates it
again.  With two mbt projects in one repository at the same version, the
second deleted the first one's tag and moved it to a different commit, exit 0,
no warning.  The rule now: the manifest at the repository root owns the tags;
without one, the only manifest in the repository does; with several and none at
the root, prerelease stops.

Each test builds a throwaway repository with a local bare origin -- nothing
here touches a real remote.
"""

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

RELEASE = Path(__file__).parent.parent / "scripts" / "mvsrelease.py"


def _git(cwd, *args):
    return subprocess.run(["git"] + list(args), cwd=cwd,
                          capture_output=True, text=True, check=True).stdout.strip()


def _manifest(name, version="0.1.0"):
    return (f'[project]\nname = "{name}"\nversion = "{version}"\n'
            f'type = "application"\n')


class _Repo:
    def __init__(self, tmp, manifests):
        """manifests: {relative dir ('' = root): project name}"""
        self.origin = Path(tmp) / "origin.git"
        self.root = Path(tmp) / "shared"
        subprocess.run(["git", "init", "-q", "--bare", str(self.origin)], check=True)
        self.root.mkdir()
        _git(self.root, "init", "-q")
        _git(self.root, "symbolic-ref", "HEAD", "refs/heads/main")
        _git(self.root, "config", "user.email", "test@example.com")
        _git(self.root, "config", "user.name", "Test")
        _git(self.root, "remote", "add", "origin", str(self.origin))
        for d, name in manifests.items():
            p = self.root / d
            p.mkdir(parents=True, exist_ok=True)
            (p / "project.toml").write_text(_manifest(name))
            (p / "file.txt").write_text(name + "\n")
        _git(self.root, "add", "-A")
        _git(self.root, "commit", "-qm", "add projects")
        _git(self.root, "push", "-q", "origin", "main")

    def commit(self, msg):
        (self.root / "later.txt").write_text(msg + "\n")
        _git(self.root, "add", "-A")
        _git(self.root, "commit", "-qm", msg)
        _git(self.root, "push", "-q", "origin", "main")

    def prerelease(self, d):
        cwd = self.root / d
        return subprocess.run(
            [sys.executable, str(RELEASE), "--project", "project.toml",
             "--prerelease"],
            cwd=cwd, capture_output=True, text=True)

    def tag(self, name="v0.1.0"):
        r = subprocess.run(["git", "rev-parse", "-q", "--verify", name + "^{commit}"],
                           cwd=self.root, capture_output=True, text=True)
        return r.stdout.strip() or None

    def origin_tag(self, name="v0.1.0"):
        out = _git(self.root, "ls-remote", "--tags", "origin", f"refs/tags/{name}")
        return out.split()[0] if out else None


class TestTagOwner(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = self._tmp.name

    def tearDown(self):
        self._tmp.cleanup()

    def test_two_projects_no_root_manifest_refuses(self):
        """The issue's case: the second project must not move the first one's tag."""
        repo = _Repo(self.tmp, {"proja": "proja", "projb": "projb"})
        r = repo.prerelease("proja")
        self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("project.toml", r.stderr)
        self.assertIsNone(repo.tag())
        self.assertIsNone(repo.origin_tag())

    def test_root_manifest_owns_the_tags(self):
        repo = _Repo(self.tmp, {"": "main", "probe": "probe"})
        r = repo.prerelease("")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        first = repo.tag()
        self.assertEqual(repo.origin_tag(), first)
        repo.commit("later change")
        r = repo.prerelease("probe")
        self.assertNotEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(repo.tag(), first, "the owner's tag was moved")
        self.assertEqual(repo.origin_tag(), first, "the owner's tag was moved on origin")

    def test_single_manifest_below_root_is_the_owner(self):
        """The RAKF layout: one project, manifest below the root."""
        repo = _Repo(self.tmp, {"build": "rakf"})
        r = repo.prerelease("build")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        first = repo.tag()
        repo.commit("later change")
        r = repo.prerelease("build")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertNotEqual(repo.tag(), first, "prerelease should move its own tag")
        self.assertEqual(repo.origin_tag(), repo.tag())

    def test_single_manifest_at_root(self):
        """The usual layout (httpd, ufsd, ...): unchanged behaviour."""
        repo = _Repo(self.tmp, {"": "httpd"})
        self.assertEqual(repo.prerelease("").returncode, 0)
        first = repo.tag()
        repo.commit("later change")
        self.assertEqual(repo.prerelease("").returncode, 0)
        self.assertNotEqual(repo.tag(), first)


if __name__ == "__main__":
    unittest.main()
