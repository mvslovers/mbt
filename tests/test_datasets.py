"""Tests for mbt/datasets.py."""

import sys
import os
import unittest
import tempfile
import shutil
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
import mbt.config as config_module

FIXTURES = Path(__file__).parent / "fixtures"

# Minimal project with build + install + dependency
HELLO_TOML = """\
[project]
name    = "hello370"
version = "1.0.0"
type    = "application"

[mvs.build.datasets.ncalib]
suffix    = "NCALIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 5, 2, 5]

[mvs.build.datasets.syslmod]
suffix    = "LOAD"
dsorg     = "PO"
recfm     = "U"
lrecl     = 0
blksize   = 32760
space     = ["TRK", 5, 2, 5]

[mvs.install]
naming = "fixed"

[mvs.install.datasets.ncalib]
name = "HELLO370.NCALIB"

[dependencies]
"mvslovers/crent370" = ">=1.0.0"

[[link.module]]
name    = "HELLO"
entry   = "HELLO"
options = ["RENT", "REUS"]
include = ["HELLO"]
"""

# Package cache simulating crent370 provides MACLIB + NCALIB
CRENT370_CACHE = {
    "mvslovers/crent370": {
        "mvs": {"provides": {"datasets": {
            "maclib": {
                "suffix": "MACLIB",
                "dsorg": "PO",
                "recfm": "FB",
                "lrecl": 80,
                "blksize": 3120,
                "space": ["TRK", 10, 5, 10],
            },
            "ncalib": {
                "suffix": "NCALIB",
                "dsorg": "PO",
                "recfm": "FB",
                "lrecl": 80,
                "blksize": 3120,
                "space": ["TRK", 10, 5, 10],
            },
        }}}
    }
}


class TestDatasetResolverBase(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._proj = Path(self._tmp) / "project.toml"
        self._proj.write_text(HELLO_TOML, encoding="utf-8")
        self._orig_global = config_module.GLOBAL_CONFIG_PATH
        self._orig_local = config_module.LOCAL_ENV_PATH
        config_module.GLOBAL_CONFIG_PATH = Path(self._tmp) / "config.toml"
        config_module.LOCAL_ENV_PATH = Path(self._tmp) / ".env"

    def tearDown(self):
        config_module.GLOBAL_CONFIG_PATH = self._orig_global
        config_module.LOCAL_ENV_PATH = self._orig_local
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _resolver(self, env: dict = None) -> DatasetResolver:
        env = env or {"MBT_MVS_HLQ": "IBMUSER"}
        with patch.dict(os.environ, env, clear=True):
            cfg = MbtConfig(project_path=str(self._proj))
        return DatasetResolver(cfg)


class TestBuildDatasets(TestDatasetResolverBase):

    def test_ncalib_dsn(self):
        r = self._resolver()
        ds = r.build_datasets()
        self.assertIn("ncalib", ds)
        self.assertEqual(ds["ncalib"].dsn, "IBMUSER.HELLO370.V1R0M0.NCALIB")

    def test_syslmod_dsn(self):
        r = self._resolver()
        ds = r.build_datasets()
        self.assertEqual(ds["syslmod"].dsn, "IBMUSER.HELLO370.V1R0M0.LOAD")

    def test_project_name_uppercased(self):
        r = self._resolver()
        ds = r.build_datasets()
        for dsn in (d.dsn for d in ds.values()):
            self.assertIn("HELLO370", dsn)

    def test_ci_mode_uses_build_id(self):
        r = self._resolver({"MBT_MVS_HLQ": "IBMUSER", "MBT_BUILD_ID": "42"})
        ds = r.build_datasets()
        self.assertEqual(ds["ncalib"].dsn, "IBMUSER.HELLO370.B42.NCALIB")
        self.assertEqual(ds["syslmod"].dsn, "IBMUSER.HELLO370.B42.LOAD")

    def test_different_hlq(self):
        r = self._resolver({"MBT_MVS_HLQ": "CIUSER"})
        ds = r.build_datasets()
        self.assertTrue(ds["ncalib"].dsn.startswith("CIUSER."))

    def test_caching(self):
        r = self._resolver()
        ds1 = r.build_datasets()
        ds2 = r.build_datasets()
        self.assertIs(ds1, ds2)


class TestInstallDatasets(TestDatasetResolverBase):

    def test_fixed_naming(self):
        r = self._resolver()
        ds = r.install_datasets()
        self.assertIn("ncalib", ds)
        # fixed: {HLQ}.{name} = IBMUSER.HELLO370.NCALIB
        self.assertEqual(ds["ncalib"].dsn, "IBMUSER.HELLO370.NCALIB")

    def test_vrm_naming(self):
        toml = HELLO_TOML.replace('naming = "fixed"', 'naming = "vrm"')
        self._proj.write_text(toml, encoding="utf-8")
        r = self._resolver()
        ds = r.install_datasets()
        self.assertEqual(ds["ncalib"].dsn, "IBMUSER.HELLO370.V1R0M0.NCALIB")

    def test_dcb_inherited_from_build(self):
        r = self._resolver()
        inst = r.install_datasets()
        build = r.build_datasets()
        self.assertEqual(inst["ncalib"].recfm, build["ncalib"].recfm)
        self.assertEqual(inst["ncalib"].lrecl, build["ncalib"].lrecl)


class TestDependencyDatasets(TestDatasetResolverBase):

    def test_dep_dsn_format(self):
        r = self._resolver()
        lockfile_deps = {"mvslovers/crent370": "1.0.0"}
        dep_ds = r.dependency_datasets(lockfile_deps, CRENT370_CACHE)
        self.assertIn("mvslovers/crent370", dep_ds)
        dsns = {ds.suffix: ds.dsn for ds in dep_ds["mvslovers/crent370"]}
        # Default deps_hlq = IBMUSER.DEPS
        self.assertEqual(dsns["MACLIB"], "IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB")
        self.assertEqual(dsns["NCALIB"], "IBMUSER.DEPS.CRENT370.V1R0M0.NCALIB")

    def test_dep_custom_deps_hlq(self):
        r = self._resolver({
            "MBT_MVS_HLQ": "IBMUSER",
            "MBT_MVS_DEPS_HLQ": "SHARED.DEPS",
        })
        lockfile_deps = {"mvslovers/crent370": "1.0.0"}
        dep_ds = r.dependency_datasets(lockfile_deps, CRENT370_CACHE)
        dsns = {ds.suffix: ds.dsn for ds in dep_ds["mvslovers/crent370"]}
        self.assertTrue(dsns["MACLIB"].startswith("SHARED.DEPS."))

    def test_empty_lockfile(self):
        r = self._resolver()
        dep_ds = r.dependency_datasets({}, {})
        self.assertEqual(dep_ds, {})


class TestSyslibOrder(TestDatasetResolverBase):

    def test_syslib_maclibs_no_project_maclib(self):
        # hello370 has no maclib dataset: dep MACLIB + SYS1.MACLIB + SYS1.AMODGEN
        r = self._resolver()
        lockfile_deps = {"mvslovers/crent370": "1.0.0"}
        maclibs = r.syslib_maclibs(lockfile_deps, CRENT370_CACHE)
        # crent370 first, then system defaults
        self.assertIn("CRENT370", maclibs[0])
        self.assertIn("MACLIB", maclibs[0])
        self.assertIn("SYS1.MACLIB", maclibs)
        self.assertIn("SYS1.AMODGEN", maclibs)

    def test_syslib_maclibs_with_project_maclib(self):
        # Add a maclib to the project
        toml = HELLO_TOML + (
            "\n[mvs.build.datasets.maclib]\n"
            "suffix=\"MACLIB\"\ndsorg=\"PO\"\nrecfm=\"FB\"\n"
            "lrecl=80\nblksize=3120\nspace=[\"TRK\",5,2,5]\n"
        )
        self._proj.write_text(toml, encoding="utf-8")
        r = self._resolver()
        lockfile_deps = {"mvslovers/crent370": "1.0.0"}
        maclibs = r.syslib_maclibs(lockfile_deps, CRENT370_CACHE)
        # Project maclib first, then dep maclib
        self.assertGreaterEqual(len(maclibs), 2)
        self.assertIn("HELLO370", maclibs[0])
        self.assertIn("CRENT370", maclibs[1])

    def test_syslib_maclibs_always_includes_defaults(self):
        r = self._resolver()
        maclibs = r.syslib_maclibs({}, {})
        self.assertIn("SYS1.MACLIB", maclibs)
        self.assertIn("SYS1.AMODGEN", maclibs)

    def test_syslib_maclibs_defaults_after_deps(self):
        r = self._resolver()
        lockfile_deps = {"mvslovers/crent370": "1.0.0"}
        maclibs = r.syslib_maclibs(lockfile_deps, CRENT370_CACHE)
        sys1_idx = maclibs.index("SYS1.MACLIB")
        crent_idx = next(i for i, m in enumerate(maclibs) if "CRENT370" in m)
        self.assertGreater(sys1_idx, crent_idx)

    def test_syslib_maclibs_project_extras_appended(self):
        toml_with_system = HELLO_TOML + '\n[system]\nmaclibs = ["SYS2.MACLIB"]\n'
        self._proj.write_text(toml_with_system, encoding="utf-8")
        r = self._resolver()
        maclibs = r.syslib_maclibs({}, {})
        self.assertIn("SYS1.MACLIB", maclibs)
        self.assertIn("SYS1.AMODGEN", maclibs)
        self.assertIn("SYS2.MACLIB", maclibs)
        # extras come after defaults
        self.assertGreater(maclibs.index("SYS2.MACLIB"),
                           maclibs.index("SYS1.AMODGEN"))

    def test_syslib_ncalibs_order(self):
        r = self._resolver()
        lockfile_deps = {"mvslovers/crent370": "1.0.0"}
        ncalibs = r.syslib_ncalibs(lockfile_deps, CRENT370_CACHE)
        # Project ncalib first, then dep ncalib
        self.assertGreaterEqual(len(ncalibs), 2)
        self.assertIn("HELLO370", ncalibs[0])
        self.assertIn("CRENT370", ncalibs[1])


if __name__ == "__main__":
    unittest.main()
