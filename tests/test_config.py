"""Tests for mbt/config.py."""

import sys
import os
import unittest
import tempfile
import shutil
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.config import MbtConfig, _DEFAULTS, _ENV_MAP
import mbt.config as config_module

FIXTURES = Path(__file__).parent / "fixtures"

MINIMAL_TOML = """\
[project]
name    = "test"
version = "1.0.0"
type    = "library"
"""


class TestMbtConfigDefaults(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._proj = Path(self._tmp) / "project.toml"
        self._proj.write_text(MINIMAL_TOML, encoding="utf-8")
        # Override GLOBAL_CONFIG_PATH to a non-existent path
        self._orig_global = config_module.GLOBAL_CONFIG_PATH
        config_module.GLOBAL_CONFIG_PATH = Path(self._tmp) / "config.toml"

    def tearDown(self):
        config_module.GLOBAL_CONFIG_PATH = self._orig_global
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _cfg(self, env: dict = None) -> MbtConfig:
        env = env or {}
        with patch.dict(os.environ, env, clear=True):
            return MbtConfig(project_path=str(self._proj))

    def test_default_host(self):
        self.assertEqual(self._cfg().mvs_host, "localhost")

    def test_default_port(self):
        self.assertEqual(self._cfg().mvs_port, 1080)

    def test_default_user(self):
        self.assertEqual(self._cfg().mvs_user, "IBMUSER")

    def test_default_hlq(self):
        self.assertEqual(self._cfg().hlq, "IBMUSER")

    def test_default_jobclass(self):
        self.assertEqual(self._cfg().jes_jobclass, "A")

    def test_default_msgclass(self):
        self.assertEqual(self._cfg().jes_msgclass, "H")

    def test_default_not_ci(self):
        cfg = self._cfg()
        self.assertFalse(cfg.is_ci)
        self.assertIsNone(cfg.build_id)

    def test_deps_hlq_default_formula(self):
        cfg = self._cfg({"MBT_MVS_HLQ": "MYUSER"})
        self.assertEqual(cfg.deps_hlq, "MYUSER.DEPS")


class TestMbtConfigEnvOverride(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._proj = Path(self._tmp) / "project.toml"
        self._proj.write_text(MINIMAL_TOML, encoding="utf-8")
        self._orig_global = config_module.GLOBAL_CONFIG_PATH
        config_module.GLOBAL_CONFIG_PATH = Path(self._tmp) / "config.toml"

    def tearDown(self):
        config_module.GLOBAL_CONFIG_PATH = self._orig_global
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _cfg(self, env: dict) -> MbtConfig:
        with patch.dict(os.environ, env, clear=True):
            return MbtConfig(project_path=str(self._proj))

    def test_env_host(self):
        cfg = self._cfg({"MBT_MVS_HOST": "myhost"})
        self.assertEqual(cfg.mvs_host, "myhost")

    def test_env_hlq(self):
        cfg = self._cfg({"MBT_MVS_HLQ": "CIUSER"})
        self.assertEqual(cfg.hlq, "CIUSER")

    def test_env_port(self):
        cfg = self._cfg({"MBT_MVS_PORT": "9999"})
        self.assertEqual(cfg.mvs_port, 9999)

    def test_ci_build_id(self):
        cfg = self._cfg({"MBT_BUILD_ID": "42"})
        self.assertTrue(cfg.is_ci)
        self.assertEqual(cfg.build_id, "42")

    def test_env_deps_hlq_explicit(self):
        cfg = self._cfg({
            "MBT_MVS_HLQ": "MYUSER",
            "MBT_MVS_DEPS_HLQ": "SHARED.DEPS",
        })
        self.assertEqual(cfg.deps_hlq, "SHARED.DEPS")


class TestMbtConfigDotenv(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._proj = Path(self._tmp) / "project.toml"
        self._proj.write_text(MINIMAL_TOML, encoding="utf-8")
        self._orig_global = config_module.GLOBAL_CONFIG_PATH
        self._orig_local = config_module.LOCAL_ENV_PATH
        config_module.GLOBAL_CONFIG_PATH = Path(self._tmp) / "config.toml"
        self._env_file = Path(self._tmp) / ".env"
        config_module.LOCAL_ENV_PATH = self._env_file

    def tearDown(self):
        config_module.GLOBAL_CONFIG_PATH = self._orig_global
        config_module.LOCAL_ENV_PATH = self._orig_local
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _cfg(self, env: dict = None) -> MbtConfig:
        env = env or {}
        with patch.dict(os.environ, env, clear=True):
            return MbtConfig(project_path=str(self._proj))

    def test_dotenv_host(self):
        self._env_file.write_text("MBT_MVS_HOST=dotenvhost\n", encoding="utf-8")
        cfg = self._cfg()
        self.assertEqual(cfg.mvs_host, "dotenvhost")

    def test_dotenv_with_comments(self):
        self._env_file.write_text(
            "# comment\nMBT_MVS_HOST=dotenvhost\n# another\n",
            encoding="utf-8",
        )
        cfg = self._cfg()
        self.assertEqual(cfg.mvs_host, "dotenvhost")

    def test_dotenv_multiple(self):
        self._env_file.write_text(
            "MBT_MVS_HOST=dotenvhost\nMBT_MVS_PORT=9999\n",
            encoding="utf-8",
        )
        cfg = self._cfg()
        self.assertEqual(cfg.mvs_host, "dotenvhost")
        self.assertEqual(cfg.mvs_port, 9999)

    def test_env_beats_dotenv(self):
        self._env_file.write_text("MBT_MVS_HOST=dotenvhost\n", encoding="utf-8")
        cfg = self._cfg({"MBT_MVS_HOST": "envhost"})
        self.assertEqual(cfg.mvs_host, "envhost")


class TestMbtConfigGlobalToml(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._proj = Path(self._tmp) / "project.toml"
        self._proj.write_text(MINIMAL_TOML, encoding="utf-8")
        self._orig_global = config_module.GLOBAL_CONFIG_PATH
        self._orig_local = config_module.LOCAL_ENV_PATH
        self._global_cfg = Path(self._tmp) / "config.toml"
        config_module.GLOBAL_CONFIG_PATH = self._global_cfg
        config_module.LOCAL_ENV_PATH = Path(self._tmp) / ".env"

    def tearDown(self):
        config_module.GLOBAL_CONFIG_PATH = self._orig_global
        config_module.LOCAL_ENV_PATH = self._orig_local
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _cfg(self, env: dict = None) -> MbtConfig:
        env = env or {}
        with patch.dict(os.environ, env, clear=True):
            return MbtConfig(project_path=str(self._proj))

    def test_global_host(self):
        self._global_cfg.write_text('[mvs]\nhost = "globalhost"\n', encoding="utf-8")
        cfg = self._cfg()
        self.assertEqual(cfg.mvs_host, "globalhost")

    def test_global_jobclass(self):
        self._global_cfg.write_text('[jes]\njobclass = "B"\n', encoding="utf-8")
        cfg = self._cfg()
        self.assertEqual(cfg.jes_jobclass, "B")

    def test_env_beats_global(self):
        self._global_cfg.write_text('[mvs]\nhost = "globalhost"\n', encoding="utf-8")
        cfg = self._cfg({"MBT_MVS_HOST": "envhost"})
        self.assertEqual(cfg.mvs_host, "envhost")

    def test_system_maclibs(self):
        self._global_cfg.write_text(
            '[system.maclibs]\nSYS1 = "SYS1.MACLIB"\nAMOD = "SYS1.AMODGEN"\n',
            encoding="utf-8",
        )
        cfg = self._cfg()
        self.assertEqual(cfg.system_maclibs, ["SYS1.MACLIB", "SYS1.AMODGEN"])

    def test_system_maclibs_empty_when_absent(self):
        cfg = self._cfg()
        self.assertEqual(cfg.system_maclibs, [])


class TestMbtConfigSourced(unittest.TestCase):

    def setUp(self):
        self._tmp = tempfile.mkdtemp()
        self._proj = Path(self._tmp) / "project.toml"
        self._proj.write_text(MINIMAL_TOML, encoding="utf-8")
        self._orig_global = config_module.GLOBAL_CONFIG_PATH
        self._orig_local = config_module.LOCAL_ENV_PATH
        config_module.GLOBAL_CONFIG_PATH = Path(self._tmp) / "config.toml"
        self._env_file = Path(self._tmp) / ".env"
        config_module.LOCAL_ENV_PATH = self._env_file

    def tearDown(self):
        config_module.GLOBAL_CONFIG_PATH = self._orig_global
        config_module.LOCAL_ENV_PATH = self._orig_local
        shutil.rmtree(self._tmp, ignore_errors=True)

    def _cfg(self, env: dict = None) -> MbtConfig:
        env = env or {}
        with patch.dict(os.environ, env, clear=True):
            return MbtConfig(project_path=str(self._proj))

    def test_sourced_env(self):
        cfg = self._cfg({"MBT_MVS_HOST": "envhost"})
        cs = cfg.get_sourced("mvs.host")
        self.assertEqual(cs.value, "envhost")
        self.assertEqual(cs.source, "env")

    def test_sourced_dotenv(self):
        self._env_file.write_text("MBT_MVS_HOST=dotenvhost\n", encoding="utf-8")
        cfg = self._cfg()
        cs = cfg.get_sourced("mvs.host")
        self.assertEqual(cs.value, "dotenvhost")
        self.assertEqual(cs.source, ".env")

    def test_sourced_global(self):
        (Path(self._tmp) / "config.toml").write_text(
            '[mvs]\nhost = "globalhost"\n', encoding="utf-8"
        )
        cfg = self._cfg()
        cs = cfg.get_sourced("mvs.host")
        self.assertEqual(cs.value, "globalhost")
        self.assertEqual(cs.source, "~/.mbt/config.toml")

    def test_sourced_default(self):
        cfg = self._cfg()
        cs = cfg.get_sourced("jes.jobclass")
        self.assertEqual(cs.value, "A")
        self.assertEqual(cs.source, "default")

    def test_unknown_key_raises(self):
        cfg = self._cfg()
        with self.assertRaises(KeyError):
            cfg.get("nonexistent.key")


if __name__ == "__main__":
    unittest.main()
