"""Tests for mbt/project.py."""

import sys
import os
import unittest
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.project import ProjectConfig, ProjectError

FIXTURES = Path(__file__).parent / "fixtures"


class TestProjectLoadHello370(unittest.TestCase):

    def setUp(self):
        self.config = ProjectConfig.load(FIXTURES / "hello370.toml")

    def test_name(self):
        self.assertEqual(self.config.name, "hello370")

    def test_version(self):
        self.assertEqual(self.config.version, "1.0.0")

    def test_type(self):
        self.assertEqual(self.config.type, "application")

    def test_vrm(self):
        self.assertEqual(self.config.vrm, "V1R0M0")

    def test_build_datasets_present(self):
        self.assertIn("punch", self.config.build_datasets)
        self.assertIn("ncalib", self.config.build_datasets)
        self.assertIn("syslmod", self.config.build_datasets)

    def test_dataset_ncalib_attrs(self):
        ds = self.config.build_datasets["ncalib"]
        self.assertEqual(ds.suffix, "NCALIB")
        self.assertEqual(ds.dsorg, "PO")
        self.assertEqual(ds.recfm, "U")
        self.assertEqual(ds.lrecl, 0)
        self.assertEqual(ds.blksize, 32760)
        self.assertEqual(ds.space, ["TRK", 5, 2, 5])
        self.assertEqual(ds.unit, "SYSDA")
        self.assertIsNone(ds.volume)
        self.assertIsNone(ds.local_dir)

    def test_dataset_syslmod_attrs(self):
        ds = self.config.build_datasets["syslmod"]
        self.assertEqual(ds.suffix, "LOAD")
        self.assertEqual(ds.dsorg, "PO")
        self.assertEqual(ds.recfm, "U")
        self.assertEqual(ds.lrecl, 0)
        self.assertEqual(ds.blksize, 32760)

    def test_space_dirblks(self):
        ds = self.config.build_datasets["ncalib"]
        self.assertEqual(ds.space_dirblks(), 5)

    def test_dependencies(self):
        self.assertIn("mvslovers/crent370", self.config.dependencies)
        self.assertEqual(self.config.dependencies["mvslovers/crent370"], ">=1.0.0")

    def test_link_modules(self):
        self.assertEqual(len(self.config.link_modules), 1)
        mod = self.config.link_modules[0]
        self.assertEqual(mod.name, "HELLO")
        self.assertEqual(mod.entry, "@@CRT0")
        self.assertEqual(mod.options, ["RENT", "REUS"])
        self.assertEqual(mod.include, ["@@CRT1", "HELLO"])
        self.assertEqual(mod.setcode, "")

    def test_artifacts(self):
        self.assertFalse(self.config.artifact_headers)
        self.assertTrue(self.config.artifact_mvs)
        self.assertFalse(self.config.artifact_bundle)

    def test_release(self):
        self.assertEqual(self.config.release_github, "mvslovers/hello370")
        self.assertEqual(self.config.release_version_files, ["project.toml"])

    def test_default_c_dirs(self):
        self.assertEqual(self.config.c_dirs, ["src/"])

    def test_default_asm_dirs(self):
        self.assertEqual(self.config.asm_dirs, [])

    def test_default_max_rc(self):
        self.assertEqual(self.config.max_rc, 4)


class TestProjectValidation(unittest.TestCase):

    def _write(self, content: str) -> Path:
        self._tmp = tempfile.mkdtemp()
        p = Path(self._tmp) / "project.toml"
        p.write_text(content, encoding="utf-8")
        return p

    def tearDown(self):
        import shutil
        if hasattr(self, "_tmp"):
            shutil.rmtree(self._tmp, ignore_errors=True)

    def test_missing_name(self):
        toml = "[project]\nversion = \"1.0.0\"\ntype = \"library\"\n"
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("name", str(cm.exception))

    def test_missing_version(self):
        toml = "[project]\nname = \"foo\"\ntype = \"library\"\n"
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("version", str(cm.exception))

    def test_missing_type(self):
        toml = "[project]\nname = \"foo\"\nversion = \"1.0.0\"\n"
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("type", str(cm.exception))

    def test_invalid_type(self):
        toml = "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"plugin\"\n"
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("plugin", str(cm.exception))

    def test_invalid_version(self):
        toml = "[project]\nname=\"foo\"\nversion=\"not-semver\"\ntype=\"library\"\n"
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("version", str(cm.exception).lower())

    def test_po_space_too_short(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[mvs.build.datasets.ncalib]\n"
            "suffix=\"NCALIB\"\ndsorg=\"PO\"\nrecfm=\"FB\"\n"
            "lrecl=80\nblksize=3120\nspace=[\"TRK\",5,2]\n"
        )
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("ncalib", str(cm.exception))
        self.assertIn("4", str(cm.exception))

    def test_ps_space_too_long(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[mvs.build.datasets.sysout]\n"
            "suffix=\"SYSOUT\"\ndsorg=\"PS\"\nrecfm=\"FB\"\n"
            "lrecl=80\nblksize=3120\nspace=[\"TRK\",5,2,5]\n"
        )
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("sysout", str(cm.exception))
        self.assertIn("3", str(cm.exception))

    def test_link_disallowed_for_library(self):
        toml = (
            "[project]\nname=\"mylib\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[mvs.build.datasets.ncalib]\n"
            "suffix=\"NCALIB\"\ndsorg=\"PO\"\nrecfm=\"FB\"\n"
            "lrecl=80\nblksize=3120\nspace=[\"TRK\",5,2,5]\n"
            "[[link.module]]\n"
            "name=\"MYLIB\"\nentry=\"MYLIB\"\noptions=[\"RENT\"]\n"
            "include=[\"MYLIB\"]\n"
        )
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("library", str(cm.exception))

    def test_link_disallowed_for_runtime(self):
        toml = (
            "[project]\nname=\"rt\"\nversion=\"1.0.0\"\ntype=\"runtime\"\n"
            "[[link.module]]\n"
            "name=\"RT\"\nentry=\"RT\"\noptions=[]\ninclude=[]\n"
        )
        with self.assertRaises(ProjectError):
            ProjectConfig.load(self._write(toml))

    def test_install_references_nonexistent_build_ds(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[mvs.build.datasets.ncalib]\n"
            "suffix=\"NCALIB\"\ndsorg=\"PO\"\nrecfm=\"FB\"\n"
            "lrecl=80\nblksize=3120\nspace=[\"TRK\",5,2,5]\n"
            "[mvs.install]\nnaming=\"fixed\"\n"
            "[mvs.install.datasets.maclib]\nname=\"FOO.MACLIB\"\n"
        )
        with self.assertRaises(ProjectError) as cm:
            ProjectConfig.load(self._write(toml))
        self.assertIn("maclib", str(cm.exception))

    def test_file_not_found(self):
        with self.assertRaises(FileNotFoundError):
            ProjectConfig.load("/nonexistent/path/project.toml")


class TestProjectSourceDirs(unittest.TestCase):

    def _write(self, content: str) -> Path:
        self._tmp = tempfile.mkdtemp()
        p = Path(self._tmp) / "project.toml"
        p.write_text(content, encoding="utf-8")
        return p

    def tearDown(self):
        import shutil
        if hasattr(self, "_tmp"):
            shutil.rmtree(self._tmp, ignore_errors=True)

    def test_custom_c_dirs(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[build.sources]\nc_dirs=[\"src/\",\"extra/\"]\n"
        )
        c = ProjectConfig.load(self._write(toml))
        self.assertEqual(c.c_dirs, ["src/", "extra/"])
        self.assertEqual(c.asm_dirs, [])

    def test_custom_asm_dirs_only(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[build.sources]\nasm_dirs=[\"asm/\"]\n"
        )
        c = ProjectConfig.load(self._write(toml))
        self.assertEqual(c.c_dirs, ["src/"])  # default preserved
        self.assertEqual(c.asm_dirs, ["asm/"])

    def test_custom_max_rc(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[mvs.asm]\nmax_rc=8\n"
        )
        c = ProjectConfig.load(self._write(toml))
        self.assertEqual(c.max_rc, 8)

    def test_system_maclibs_default_empty(self):
        toml = "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
        c = ProjectConfig.load(self._write(toml))
        self.assertEqual(c.system_maclibs, [])

    def test_system_maclibs_parsed(self):
        toml = (
            "[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"library\"\n"
            "[system]\nmaclibs = [\"SYS2.MACLIB\", \"MYLIB.MACLIB\"]\n"
        )
        c = ProjectConfig.load(self._write(toml))
        self.assertEqual(c.system_maclibs, ["SYS2.MACLIB", "MYLIB.MACLIB"])


class TestProjectValidTypes(unittest.TestCase):

    def _make(self, ptype: str) -> ProjectConfig:
        toml = (
            f"[project]\nname=\"foo\"\nversion=\"1.0.0\"\ntype=\"{ptype}\"\n"
        )
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".toml", delete=False, encoding="utf-8"
        ) as f:
            f.write(toml)
            f.flush()
            return ProjectConfig.load(f.name)

    def test_runtime(self):
        c = self._make("runtime")
        self.assertEqual(c.type, "runtime")

    def test_library(self):
        c = self._make("library")
        self.assertEqual(c.type, "library")

    def test_module(self):
        c = self._make("module")
        self.assertEqual(c.type, "module")

    def test_application(self):
        c = self._make("application")
        self.assertEqual(c.type, "application")


class TestLinkModuleSetcode(unittest.TestCase):

    def _write(self, content: str) -> Path:
        self._tmp = tempfile.mkdtemp()
        p = Path(self._tmp) / "project.toml"
        p.write_text(content, encoding="utf-8")
        return p

    def tearDown(self):
        import shutil
        if hasattr(self, "_tmp"):
            shutil.rmtree(self._tmp, ignore_errors=True)

    def test_setcode_parsed(self):
        toml = (
            "[project]\nname=\"myapp\"\nversion=\"1.0.0\"\ntype=\"application\"\n"
            "[mvs.build.datasets.ncalib]\n"
            "suffix=\"NCALIB\"\ndsorg=\"PO\"\nrecfm=\"U\"\n"
            "lrecl=0\nblksize=32760\nspace=[\"TRK\",5,2,5]\n"
            "[mvs.build.datasets.syslmod]\n"
            "suffix=\"LOAD\"\ndsorg=\"PO\"\nrecfm=\"U\"\n"
            "lrecl=0\nblksize=32760\nspace=[\"TRK\",5,2,5]\n"
            "[dependencies]\n\"mvslovers/crent370\" = \">=1.0.0\"\n"
            "[link.module]\n"
            "name=\"MYAPP\"\noptions=[\"RENT\"]\n"
            "setcode=\"AC(1)\"\n"
        )
        c = ProjectConfig.load(self._write(toml))
        self.assertEqual(len(c.link_modules), 1)
        mod = c.link_modules[0]
        self.assertEqual(mod.setcode, "AC(1)")

    def test_setcode_default_empty(self):
        toml = (
            "[project]\nname=\"myapp\"\nversion=\"1.0.0\"\ntype=\"application\"\n"
            "[mvs.build.datasets.ncalib]\n"
            "suffix=\"NCALIB\"\ndsorg=\"PO\"\nrecfm=\"U\"\n"
            "lrecl=0\nblksize=32760\nspace=[\"TRK\",5,2,5]\n"
            "[mvs.build.datasets.syslmod]\n"
            "suffix=\"LOAD\"\ndsorg=\"PO\"\nrecfm=\"U\"\n"
            "lrecl=0\nblksize=32760\nspace=[\"TRK\",5,2,5]\n"
            "[dependencies]\n\"mvslovers/crent370\" = \">=1.0.0\"\n"
            "[link.module]\n"
            "name=\"MYAPP\"\noptions=[\"RENT\"]\n"
        )
        c = ProjectConfig.load(self._write(toml))
        mod = c.link_modules[0]
        self.assertEqual(mod.setcode, "")


if __name__ == "__main__":
    unittest.main()
