"""Tests for mbt/dependencies.py."""

import sys
import json
import tarfile
import tempfile
import unittest
import io
from pathlib import Path
from unittest.mock import patch, MagicMock

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.dependencies import (
    resolve_dependencies,
    download_dependency,
    extract_headers,
    load_package_toml,
    DependencyError,
    _resolve_one,
    CACHE_DIR,
)
from mbt.lockfile import Lockfile


# --- Helpers ---

def _make_release(tag: str, assets: list[dict] | None = None,
                  draft: bool = False,
                  prerelease: bool = False) -> dict:
    """Build a fake GitHub release dict."""
    return {
        "tag_name": tag,
        "draft": draft,
        "prerelease": prerelease,
        "assets": assets or [],
    }


def _mock_urlopen(releases: list[dict]):
    """Return a context manager mock that yields a fake response."""
    body = json.dumps(releases).encode("utf-8")

    class FakeResp:
        def read(self):
            return body
        def __enter__(self):
            return self
        def __exit__(self, *a):
            pass

    return FakeResp()


# --- resolve_dependencies ---

class TestResolveDependenciesWithLockfile(unittest.TestCase):

    def test_uses_lockfile_when_no_update(self):
        lockfile = Lockfile(
            generated="2026-03-04T10:00:00Z",
            mbt_version="1.0.0",
            dependencies={"mvslovers/crent370": "1.2.3"},
        )
        result = resolve_dependencies(
            {"mvslovers/crent370": ">=1.0.0"},
            lockfile=lockfile,
            update=False,
        )
        self.assertEqual(result, {"mvslovers/crent370": "1.2.3"})

    def test_ignores_lockfile_when_update(self):
        lockfile = Lockfile(
            generated="2026-03-04T10:00:00Z",
            mbt_version="1.0.0",
            dependencies={"mvslovers/crent370": "1.0.0"},
        )
        releases = [_make_release("v1.5.0"), _make_release("v1.0.0")]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = resolve_dependencies(
                {"mvslovers/crent370": ">=1.0.0"},
                lockfile=lockfile,
                update=True,
            )
        self.assertEqual(result["mvslovers/crent370"], "1.5.0")

    def test_no_lockfile_resolves_fresh(self):
        releases = [_make_release("v2.0.0"), _make_release("v1.0.0")]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = resolve_dependencies(
                {"mvslovers/crent370": ">=1.0.0"},
                lockfile=None,
            )
        self.assertEqual(result["mvslovers/crent370"], "2.0.0")

    def test_empty_declared_returns_empty(self):
        result = resolve_dependencies({}, lockfile=None)
        self.assertEqual(result, {})


# --- _resolve_one ---

class TestResolveOne(unittest.TestCase):

    def test_selects_highest_matching(self):
        releases = [
            _make_release("v2.0.0"),
            _make_release("v1.5.0"),
            _make_release("v1.0.0"),
        ]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", ">=1.0.0")
        self.assertEqual(result, "2.0.0")

    def test_respects_upper_bound(self):
        releases = [
            _make_release("v2.0.0"),
            _make_release("v1.9.0"),
            _make_release("v1.0.0"),
        ]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", ">=1.0.0,<2.0.0")
        self.assertEqual(result, "1.9.0")

    def test_exact_version(self):
        releases = [
            _make_release("v2.0.0"),
            _make_release("v1.0.0"),
        ]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", "=1.0.0")
        self.assertEqual(result, "1.0.0")

    def test_skips_draft_releases(self):
        releases = [
            _make_release("v2.0.0", draft=True),
            _make_release("v1.0.0"),
        ]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", ">=1.0.0")
        self.assertEqual(result, "1.0.0")

    def test_skips_prerelease_releases(self):
        releases = [
            _make_release("v2.0.0-beta", prerelease=True),
            _make_release("v1.0.0"),
        ]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", ">=1.0.0")
        self.assertEqual(result, "1.0.0")

    def test_skips_invalid_tag_names(self):
        releases = [
            _make_release("not-a-version"),
            _make_release("v1.0.0"),
        ]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", ">=1.0.0")
        self.assertEqual(result, "1.0.0")

    def test_no_matching_raises(self):
        releases = [_make_release("v1.0.0")]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            with self.assertRaises(DependencyError):
                _resolve_one("mvslovers", "crent370", ">=2.0.0")

    def test_empty_releases_raises(self):
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen([])
            with self.assertRaises(DependencyError):
                _resolve_one("mvslovers", "crent370", ">=1.0.0")

    def test_strips_v_prefix(self):
        releases = [_make_release("v1.2.3")]
        with patch("urllib.request.urlopen") as mock_open:
            mock_open.return_value = _mock_urlopen(releases)
            result = _resolve_one("mvslovers", "crent370", "=1.2.3")
        self.assertEqual(result, "1.2.3")


# --- extract_headers ---

class TestExtractHeaders(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._orig_cwd = Path.cwd()
        import os
        os.chdir(self._tmp)

    def tearDown(self):
        import os
        import shutil
        os.chdir(self._orig_cwd)
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _make_headers_tarball(self, cache_dir: Path,
                              dep_name: str,
                              dep_version: str,
                              header_files: list[str]) -> None:
        """Create a fake headers tarball in cache_dir."""
        cache_dir.mkdir(parents=True, exist_ok=True)
        tarball = cache_dir / f"{dep_name}-{dep_version}-headers.tar.gz"
        buf = io.BytesIO()
        prefix = f"{dep_name}-{dep_version}"
        with tarfile.open(fileobj=buf, mode="w:gz") as tf:
            for fname in header_files:
                content = f"/* {fname} */\n".encode()
                info = tarfile.TarInfo(name=f"{prefix}/include/{fname}")
                info.size = len(content)
                tf.addfile(info, io.BytesIO(content))
        tarball.write_bytes(buf.getvalue())

    def test_extracts_to_contrib(self):
        cache_dir = Path(self._tmp) / "cache"
        self._make_headers_tarball(cache_dir, "crent370", "1.0.0",
                                   ["crent.h", "stdlib.h"])
        with patch("mbt.dependencies.CACHE_DIR", Path(self._tmp) / "cache"):
            inc = extract_headers(cache_dir, "crent370", "1.0.0")
        self.assertTrue(inc.exists())
        self.assertTrue((inc / "crent.h").exists())

    def test_returns_include_path(self):
        cache_dir = Path(self._tmp) / "cache"
        self._make_headers_tarball(cache_dir, "crent370", "1.0.0",
                                   ["foo.h"])
        inc = extract_headers(cache_dir, "crent370", "1.0.0")
        self.assertTrue(str(inc).endswith("include"))

    def test_missing_tarball_raises(self):
        cache_dir = Path(self._tmp) / "empty_cache"
        cache_dir.mkdir()
        with self.assertRaises(DependencyError):
            extract_headers(cache_dir, "crent370", "1.0.0")

    def test_contrib_dir_named_correctly(self):
        cache_dir = Path(self._tmp) / "cache"
        self._make_headers_tarball(cache_dir, "ufs370", "2.0.0", ["ufs.h"])
        inc = extract_headers(cache_dir, "ufs370", "2.0.0")
        # Should be in contrib/ufs370-2.0.0/include/
        self.assertIn("ufs370-2.0.0", str(inc))


# --- load_package_toml ---

class TestLoadPackageToml(unittest.TestCase):

    def test_returns_empty_when_not_found(self):
        with patch("mbt.dependencies.CACHE_DIR", Path("/nonexistent/cache")):
            result = load_package_toml("mvslovers", "crent370", "1.0.0")
        self.assertEqual(result, {})

    def test_loads_valid_toml(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg_dir = Path(tmp) / "mvslovers" / "crent370" / "1.0.0"
            pkg_dir.mkdir(parents=True)
            pkg_toml = pkg_dir / "package.toml"
            pkg_toml.write_text(
                '[mvs]\nprovides = true\n', encoding="utf-8"
            )
            with patch("mbt.dependencies.CACHE_DIR", Path(tmp)):
                result = load_package_toml("mvslovers", "crent370", "1.0.0")
        self.assertIn("mvs", result)

    def test_loads_provides_datasets(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg_dir = Path(tmp) / "mvslovers" / "crent370" / "1.0.0"
            pkg_dir.mkdir(parents=True)
            pkg_toml = pkg_dir / "package.toml"
            pkg_toml.write_text(
                '[mvs.provides.datasets.ncalib]\n'
                'suffix = "NCALIB"\ndsorg = "PO"\n'
                'recfm = "FB"\nlrecl = 80\nblksize = 3120\n'
                'space = ["TRK", 10, 5, 10]\n',
                encoding="utf-8"
            )
            with patch("mbt.dependencies.CACHE_DIR", Path(tmp)):
                result = load_package_toml("mvslovers", "crent370", "1.0.0")
        ds = result["mvs"]["provides"]["datasets"]["ncalib"]
        self.assertEqual(ds["suffix"], "NCALIB")


# --- download_dependency (cache hit) ---

class TestDownloadDependencyCache(unittest.TestCase):

    def test_cache_hit_skips_download(self):
        with tempfile.TemporaryDirectory() as tmp:
            cache_dir = Path(tmp) / "mvslovers" / "crent370" / "1.0.0"
            cache_dir.mkdir(parents=True)
            # Create a dummy file so cache is "populated"
            (cache_dir / "package.toml").write_text("[meta]\n", encoding="utf-8")
            with patch("mbt.dependencies.CACHE_DIR", Path(tmp)):
                with patch("urllib.request.urlopen") as mock_open:
                    result = download_dependency(
                        "mvslovers", "crent370", "1.0.0"
                    )
                    mock_open.assert_not_called()
            self.assertEqual(result, cache_dir)


if __name__ == "__main__":
    unittest.main()
