"""The mbt 2 VERSION file and the three legacy scripts that read it.

VERSION belongs to mbt 2 (last release 2.2.0, no longer bumped). mbt 3 takes
its version from the release tag. A mbt 2 consumer that moves its submodule
pin still reads VERSION through MBT_VERSION and the package manifest, so
each reader needs a defined result with and without the file.
"""

import importlib.util
import os
import re
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


class CommittedVersionFileTest(unittest.TestCase):
    def test_is_a_plain_release_version(self):
        text = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
        self.assertRegex(text, r"^\d+\.\d+\.\d+$")

    def test_is_still_mbt_2(self):
        major = int((ROOT / "VERSION").read_text(encoding="utf-8").split(".")[0])
        self.assertEqual(major, 2)


class MbtconfigReaderTest(unittest.TestCase):
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


class RootRelativeReaderTest(unittest.TestCase):
    """mbtbootstrap and mvspackage resolve the root from their own __file__."""

    def _check(self, name):
        mod = _load_legacy(name)
        with tempfile.TemporaryDirectory() as d:
            fake = Path(d) / "scripts" / "mod.py"
            with mock.patch.object(mod, "__file__", str(fake)):
                self.assertEqual(mod._read_mbt_version(), "0.0.0")
                (Path(d) / "VERSION").write_text("2.2.0\n", encoding="utf-8")
                self.assertEqual(mod._read_mbt_version(), "2.2.0")

    def test_mbtbootstrap(self):
        self._check("mbtbootstrap")

    def test_mvspackage(self):
        self._check("mvspackage")


if __name__ == "__main__":
    unittest.main()
