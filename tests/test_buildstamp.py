"""Tests for scripts/mbt/buildstamp.py -- the generated .mbt/buildstamp.h.

Regression for mbt#59: a commit hash injected as a `-DCOMMIT` cflag is baked
into an object make has no reason to recompile, so a build made after a
commit still printed the previous hash. The stamp is a generated header now,
which puts two properties under test here:

  * content-addressed -- an unchanged commit must NOT rewrite the file
    (a fresh mtime would recompile the including TU on every make);
  * resolved against the *project* checkout, degrading to "unknown" outside
    one instead of failing the build.
"""

import os
import sys
import subprocess
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt import buildstamp


def _git(*args, cwd):
    subprocess.run(["git", *args], cwd=cwd, check=True,
                   capture_output=True, text=True)


def _init_repo(path: Path):
    """A throwaway repo with one commit, independent of the user's git config."""
    _git("init", "-q", cwd=path)
    _git("config", "user.email", "test@example.invalid", cwd=path)
    _git("config", "user.name", "mbt test", cwd=path)
    _git("config", "commit.gpgsign", "false", cwd=path)
    Path(path, "tracked.txt").write_text("one\n")
    _git("add", "tracked.txt", cwd=path)
    _git("commit", "-q", "-m", "initial", cwd=path)


class RenderTest(unittest.TestCase):
    def test_macros_present(self):
        out = buildstamp.render("ufsd", "1.1.0-dev", "cd5be7f", False)
        self.assertIn('#define MBT_PROJECT      "ufsd"', out)
        self.assertIn('#define MBT_VERSION      "1.1.0-dev"', out)
        self.assertIn('#define MBT_COMMIT       "cd5be7f"', out)
        self.assertIn("#define MBT_COMMIT_DIRTY 0", out)

    def test_dirty_suffixes_the_commit(self):
        out = buildstamp.render("ufsd", "1.1.0-dev", "04f4fed", True)
        self.assertIn('#define MBT_COMMIT       "04f4fed-dirty"', out)
        self.assertIn("#define MBT_COMMIT_DIRTY 1", out)

    def test_include_guarded(self):
        out = buildstamp.render("p", "1.0.0", "abc1234", False)
        self.assertIn("#ifndef MBT_BUILDSTAMP_H", out)
        self.assertIn("#endif /* MBT_BUILDSTAMP_H */", out)

    def test_no_timestamp(self):
        # A build date would differ every run and recompile the including TU
        # forever -- the whole point of content-addressing it away.
        out = buildstamp.render("p", "1.0.0", "abc1234", False).lower()
        for word in ("__date__", "__time__", "build_date", "built"):
            self.assertNotIn(word, out)

    def test_quotes_escaped(self):
        out = buildstamp.render('we"ird', "1.0.0", "abc1234", False)
        self.assertIn(r'#define MBT_PROJECT      "we\"ird"', out)


class WriteIfChangedTest(unittest.TestCase):
    """The load-bearing property: no rewrite means no recompile."""

    def test_creates_missing_file(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d, "sub", "buildstamp.h")
            self.assertTrue(buildstamp.write_if_changed(p, "x\n"))
            self.assertEqual(p.read_text(), "x\n")

    def test_identical_content_leaves_mtime_alone(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d, "buildstamp.h")
            buildstamp.write_if_changed(p, "x\n")
            before = p.stat().st_mtime_ns
            self.assertFalse(buildstamp.write_if_changed(p, "x\n"))
            self.assertEqual(p.stat().st_mtime_ns, before)

    def test_changed_content_rewrites(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d, "buildstamp.h")
            buildstamp.write_if_changed(p, "x\n")
            self.assertTrue(buildstamp.write_if_changed(p, "y\n"))
            self.assertEqual(p.read_text(), "y\n")


class ResolveCommitTest(unittest.TestCase):
    def test_outside_a_checkout_degrades_to_unknown(self):
        with tempfile.TemporaryDirectory() as d:
            # A tmpdir can still sit inside a repo on some systems; -c
            # protects nothing there, so make it a hard ceiling instead.
            Path(d, ".git").mkdir()      # an invalid repo: rev-parse fails
            commit, dirty = buildstamp.resolve_commit(cwd=d)
            self.assertEqual(commit, "unknown")
            self.assertFalse(dirty)

    def test_clean_checkout(self):
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            commit, dirty = buildstamp.resolve_commit(cwd=d)
            self.assertRegex(commit, r"^[0-9a-f]{7,}$")
            self.assertFalse(dirty)

    def test_tracked_modification_is_dirty(self):
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            Path(d, "tracked.txt").write_text("two\n")
            _, dirty = buildstamp.resolve_commit(cwd=d)
            self.assertTrue(dirty)

    def test_untracked_file_is_not_dirty(self):
        # --untracked-files=no: stray notes and generated files are not a
        # provenance difference (matches ufsd's pre-existing -DCOMMIT rule).
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            Path(d, "scratch.log").write_text("noise\n")
            _, dirty = buildstamp.resolve_commit(cwd=d)
            self.assertFalse(dirty)

    def test_resolves_the_project_checkout_not_the_cwd(self):
        # mbt is a submodule of the project; make runs mbtconfig from the
        # project root, so the stamp must come from there -- not from
        # whatever repo the interpreter happens to be started in.
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            head = subprocess.run(
                ["git", "rev-parse", "--short", "HEAD"],
                cwd=d, capture_output=True, text=True,
            ).stdout.strip()
            commit, _ = buildstamp.resolve_commit(cwd=d)
            self.assertEqual(commit, head)


class GenerateTest(unittest.TestCase):
    def test_second_run_is_a_no_op(self):
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            p = Path(d, ".mbt", "buildstamp.h")
            self.assertTrue(buildstamp.generate("p", "1.0.0", p, cwd=d))
            before = p.stat().st_mtime_ns
            self.assertFalse(buildstamp.generate("p", "1.0.0", p, cwd=d))
            self.assertEqual(p.stat().st_mtime_ns, before)

    def test_new_commit_rewrites(self):
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            p = Path(d, ".mbt", "buildstamp.h")
            buildstamp.generate("p", "1.0.0", p, cwd=d)
            first = p.read_text()
            Path(d, "tracked.txt").write_text("two\n")
            _git("commit", "-q", "-am", "second", cwd=d)
            self.assertTrue(buildstamp.generate("p", "1.0.0", p, cwd=d))
            self.assertNotEqual(p.read_text(), first)

    def test_version_bump_rewrites(self):
        with tempfile.TemporaryDirectory() as d:
            _init_repo(Path(d))
            p = Path(d, ".mbt", "buildstamp.h")
            buildstamp.generate("p", "1.0.0", p, cwd=d)
            self.assertTrue(buildstamp.generate("p", "1.1.0", p, cwd=d))
            self.assertIn('"1.1.0"', p.read_text())


class MbtconfigWritesStampTest(unittest.TestCase):
    """mbtconfig.py is the single generator -- mk/mbt.mk calls nothing else."""

    def test_main_writes_the_header(self):
        script = Path(__file__).resolve().parent.parent / "scripts" / "mbtconfig.py"
        with tempfile.TemporaryDirectory() as d:
            Path(d, "project.toml").write_text(
                '[project]\nname = "demo"\nversion = "2.3.4"\n'
            )
            r = subprocess.run(
                [sys.executable, str(script), "--output", "file"],
                cwd=d, capture_output=True, text=True,
            )
            self.assertEqual(r.returncode, 0, r.stderr)
            stamp = Path(d, ".mbt", "buildstamp.h").read_text()
            self.assertIn('#define MBT_PROJECT      "demo"', stamp)
            self.assertIn('#define MBT_VERSION      "2.3.4"', stamp)
            self.assertIn("#define MBT_COMMIT ", stamp)


if __name__ == "__main__":
    unittest.main()
