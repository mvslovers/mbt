"""Tests for scripts/mvsrelease.py -- the guard against tagging the wrong repo.

Regression for the nested-repository bug. mvsrelease built every git command
with no working directory, so git discovered a repository by searching upward
from the cwd. `--project` resolved against the cwd separately. The two halves
could name different repositories:

  * an mbt project inside a larger repository read its *own* version, then
    deleted and recreated that version's tag on the *outer* repository, local
    and remote, and exited 0 with no warning;
  * `release` mode went further -- it committed the inner files into the outer
    history and pushed that branch to the outer origin.

Two properties are under test here:

  * the project directory must BE the repository root, or the run aborts
    (EXIT_CONFIG) before any git write;
  * every git call targets the *project's* repository, not whatever repository
    the cwd happens to sit in.

The tests use throwaway repositories with a local bare 'origin'. Nothing here
reaches a network.
"""

import sys
import subprocess
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

MVSRELEASE = Path(__file__).parent.parent / "scripts" / "mvsrelease.py"

EXIT_SUCCESS = 0
EXIT_CONFIG = 2

PROJECT_TOML = (
    '[project]\n'
    'name = "{name}"\n'
    'version = "{version}"\n'
    'type = "application"\n'
)


def _git(*args, cwd) -> str:
    """Run git in `cwd`, failing the test on a non-zero return code."""
    return subprocess.run(
        ["git", *args], cwd=str(cwd), check=True,
        capture_output=True, text=True,
    ).stdout.strip()


def _make_repo(root: Path, name: str) -> Path:
    """A throwaway repo on branch 'main' with one commit and a bare 'origin'."""
    bare = root / f"{name}.git"
    work = root / name
    work.mkdir()
    subprocess.run(["git", "init", "-q", "--bare", str(bare)],
                   check=True, capture_output=True)
    _git("init", "-q", cwd=work)
    _git("symbolic-ref", "HEAD", "refs/heads/main", cwd=work)
    _git("config", "user.email", "test@example.invalid", cwd=work)
    _git("config", "user.name", "mbt test", cwd=work)
    _git("config", "commit.gpgsign", "false", cwd=work)
    _git("remote", "add", "origin", str(bare), cwd=work)
    Path(work, "README.md").write_text("x\n")
    _git("add", "README.md", cwd=work)
    _git("commit", "-q", "-m", "initial", cwd=work)
    _git("push", "-q", "origin", "main", cwd=work)
    return work


def _write_project(directory: Path, name: str, version: str) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    Path(directory, "project.toml").write_text(
        PROJECT_TOML.format(name=name, version=version)
    )


def _run(*args, cwd) -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(MVSRELEASE), *args],
        cwd=str(cwd), capture_output=True, text=True,
    )


def _tags(work: Path) -> list[str]:
    out = _git("tag", "-l", cwd=work)
    return out.split() if out else []


def _remote_tags(work: Path) -> list[str]:
    out = _git("ls-remote", "--tags", "origin", cwd=work)
    return [line.split("refs/tags/")[-1]
            for line in out.splitlines() if line.strip()]


def _commit_count(work: Path) -> int:
    return int(_git("rev-list", "--count", "HEAD", cwd=work))


class NestedRepoGuardTest(unittest.TestCase):
    """A project below the repository root must abort, not write."""

    def test_prerelease_refuses_and_leaves_the_outer_tag_alone(self):
        with tempfile.TemporaryDirectory() as d:
            outer = _make_repo(Path(d), "outer")
            _git("tag", "v0.1.0", cwd=outer)
            _git("push", "-q", "origin", "v0.1.0", cwd=outer)
            before = _git("rev-parse", "v0.1.0", cwd=outer)

            inner = outer / "inner"
            _write_project(inner, "inner", "0.1.0")
            _git("add", "inner/project.toml", cwd=outer)
            _git("commit", "-q", "-m", "add inner", cwd=outer)

            r = _run("--project", "project.toml", "--prerelease", cwd=inner)

            self.assertEqual(r.returncode, EXIT_CONFIG, r.stderr)
            self.assertIn("not at the root of its git repository", r.stderr)
            self.assertEqual(_git("rev-parse", "v0.1.0", cwd=outer), before)
            self.assertEqual(_remote_tags(outer), ["v0.1.0"])

    def test_release_refuses_and_writes_no_commit(self):
        with tempfile.TemporaryDirectory() as d:
            outer = _make_repo(Path(d), "outer")
            inner = outer / "inner"
            _write_project(inner, "inner", "0.1.0-dev")
            _git("add", "inner/project.toml", cwd=outer)
            _git("commit", "-q", "-m", "add inner", cwd=outer)
            before = _commit_count(outer)

            r = _run("--project", "project.toml", "--version", "0.1.0",
                     cwd=inner)

            self.assertEqual(r.returncode, EXIT_CONFIG, r.stderr)
            self.assertIn("not at the root of its git repository", r.stderr)
            self.assertEqual(_commit_count(outer), before)
            self.assertEqual(_tags(outer), [])
            # The inner manifest must be untouched.
            self.assertIn('version = "0.1.0-dev"',
                          Path(inner, "project.toml").read_text())

    def test_outside_a_checkout_refuses(self):
        with tempfile.TemporaryDirectory() as d:
            loose = Path(d, "loose")
            _write_project(loose, "loose", "0.1.0")

            r = _run("--project", "project.toml", "--prerelease", cwd=loose)

            self.assertEqual(r.returncode, EXIT_CONFIG, r.stderr)
            # The guard must name the cause, not leak a raw git error from a
            # later step that happens to fail for the same reason.
            self.assertIn("is not inside a git repository", r.stderr)


class ProjectRepoTargetTest(unittest.TestCase):
    """Every git call must target the project's repo, whatever the cwd is."""

    def test_prerelease_at_the_repo_root_tags_and_pushes(self):
        with tempfile.TemporaryDirectory() as d:
            repo = _make_repo(Path(d), "proj")
            _write_project(repo, "proj", "0.1.0")
            _git("add", "project.toml", cwd=repo)
            _git("commit", "-q", "-m", "add project", cwd=repo)

            r = _run("--project", "project.toml", "--prerelease", cwd=repo)

            self.assertEqual(r.returncode, EXIT_SUCCESS, r.stderr)
            self.assertEqual(_tags(repo), ["v0.1.0"])
            self.assertEqual(_remote_tags(repo), ["v0.1.0"])

    def test_prerelease_tags_the_project_repo_not_the_cwd_repo(self):
        with tempfile.TemporaryDirectory() as d:
            proj = _make_repo(Path(d), "proj")
            other = _make_repo(Path(d), "other")
            _write_project(proj, "proj", "0.1.0")
            _git("add", "project.toml", cwd=proj)
            _git("commit", "-q", "-m", "add project", cwd=proj)
            # 'other' is dirty: today's cwd-based clean check would abort here.
            Path(other, "README.md").write_text("dirty\n")

            r = _run("--project", str(Path(proj, "project.toml")),
                     "--prerelease", cwd=other)

            self.assertEqual(r.returncode, EXIT_SUCCESS, r.stderr)
            self.assertEqual(_tags(proj), ["v0.1.0"])
            self.assertEqual(_tags(other), [])
            self.assertEqual(_remote_tags(other), [])

    def test_release_bumps_the_project_repo_not_the_cwd_repo(self):
        with tempfile.TemporaryDirectory() as d:
            proj = _make_repo(Path(d), "proj")
            other = _make_repo(Path(d), "other")
            _write_project(proj, "proj", "0.1.0-dev")
            _git("add", "project.toml", cwd=proj)
            _git("commit", "-q", "-m", "add project", cwd=proj)
            before_other = _commit_count(other)

            r = _run("--project", str(Path(proj, "project.toml")),
                     "--version", "0.1.0", cwd=other)

            self.assertEqual(r.returncode, EXIT_SUCCESS, r.stderr)
            self.assertIn('version = "0.1.1-dev"',
                          Path(proj, "project.toml").read_text())
            self.assertEqual(_tags(proj), ["v0.1.0"])
            self.assertEqual(_commit_count(other), before_other)
            self.assertEqual(_tags(other), [])


class CleanTreeTest(unittest.TestCase):
    def test_dirty_project_repo_aborts(self):
        with tempfile.TemporaryDirectory() as d:
            repo = _make_repo(Path(d), "proj")
            _write_project(repo, "proj", "0.1.0")
            _git("add", "project.toml", cwd=repo)
            _git("commit", "-q", "-m", "add project", cwd=repo)
            Path(repo, "README.md").write_text("dirty\n")

            r = _run("--project", "project.toml", "--prerelease", cwd=repo)

            self.assertEqual(r.returncode, EXIT_CONFIG, r.stderr)
            self.assertIn("not clean", r.stderr)
            self.assertEqual(_tags(repo), [])


if __name__ == "__main__":
    unittest.main()
