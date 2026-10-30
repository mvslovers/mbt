"""The legacy scripts that read a VERSION file next to the mbt checkout.

mbt takes its version from the release tag; the repository has no VERSION
file. scripts/legacy/mbtbootstrap.py and mvspackage.py resolve the file from
scripts/ (not the repository root), and mbtconfig.py only finds it through
MBT_ROOT, so each reader has a defined fallback. These tests load the real
modules from scripts/legacy/ without touching their paths.
"""

import importlib.util
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).parent.parent
sys.path.insert(0, str(ROOT / "scripts"))


def _load_legacy(name):
    """Load scripts/legacy/<name>.py under a private module name.

    scripts/ has its own mbtconfig.py, so the legacy one cannot be imported
    by its plain name.
    """
    path = ROOT / "scripts" / "legacy" / f"{name}.py"
    spec = importlib.util.spec_from_file_location(f"legacy_{name}", path)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, str(path.parent))
    try:
        spec.loader.exec_module(mod)
    finally:
        sys.path.remove(str(path.parent))
    return mod


def _env_without_mbt_root():
    env = {k: v for k, v in os.environ.items() if k != "MBT_ROOT"}
    return mock.patch.dict(os.environ, env, clear=True)


class NoVersionFileTest(unittest.TestCase):
    def test_repository_has_no_version_file(self):
        self.assertFalse((ROOT / "VERSION").exists())

    def test_mbtconfig_falls_back_to_unknown(self):
        mod = _load_legacy("mbtconfig")
        with _env_without_mbt_root():
            self.assertEqual(mod._mbt_version(), "unknown")

    def test_mbtbootstrap_falls_back_to_zero(self):
        self.assertEqual(_load_legacy("mbtbootstrap")._read_mbt_version(), "0.0.0")

    def test_mvspackage_falls_back_to_zero(self):
        self.assertEqual(_load_legacy("mvspackage")._read_mbt_version(), "0.0.0")


class MbtRootOverrideTest(unittest.TestCase):
    """MBT_ROOT is the only way mbtconfig finds a VERSION file."""

    @classmethod
    def setUpClass(cls):
        cls.mod = _load_legacy("mbtconfig")

    def test_reads_and_trims_the_file(self):
        with tempfile.TemporaryDirectory() as d:
            (Path(d) / "VERSION").write_text("2.2.0\n", encoding="utf-8")
            with mock.patch.dict(os.environ, {"MBT_ROOT": d}):
                self.assertEqual(self.mod._mbt_version(), "2.2.0")

    def test_missing_file_gives_unknown(self):
        with tempfile.TemporaryDirectory() as d:
            with mock.patch.dict(os.environ, {"MBT_ROOT": d}):
                self.assertEqual(self.mod._mbt_version(), "unknown")


if __name__ == "__main__":
    unittest.main()
