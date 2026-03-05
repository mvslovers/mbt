"""Configuration manager.

Merges all config sources in priority order:
1. Environment variables (MBT_*)
2. Project-local .env file
3. Global ~/.mbt/config.toml
4. Built-in defaults

All other modules receive their config values through this
module. No other module should read env vars or config files
directly.
"""

import os
import tomllib
from pathlib import Path
from dataclasses import dataclass

from .project import ProjectConfig


# Default values for all config keys
_DEFAULTS = {
    "mvs.host":      "localhost",
    "mvs.port":      "1080",
    "mvs.user":      "IBMUSER",
    "mvs.pass":      "",
    "mvs.hlq":        "IBMUSER",
    "mvs.deps_hlq":   "",          # computed: {hlq}.DEPS when empty
    "mvs.deps_volume": "",          # volume for RECEIVE; empty = public
    "jes.jobclass":  "A",
    "jes.msgclass":  "H",
    "build.id":      "",          # empty = not CI
}

# Mapping: dotted config key -> environment variable name
_ENV_MAP = {
    "mvs.host":         "MBT_MVS_HOST",
    "mvs.port":         "MBT_MVS_PORT",
    "mvs.user":         "MBT_MVS_USER",
    "mvs.pass":         "MBT_MVS_PASS",
    "mvs.hlq":          "MBT_MVS_HLQ",
    "mvs.deps_hlq":     "MBT_MVS_DEPS_HLQ",
    "mvs.deps_volume":  "MBT_MVS_DEPS_VOLUME",
    "jes.jobclass":  "MBT_JES_JOBCLASS",
    "jes.msgclass":  "MBT_JES_MSGCLASS",
    "build.id":      "MBT_BUILD_ID",
}

GLOBAL_CONFIG_PATH = Path.home() / ".mbt" / "config.toml"
LOCAL_ENV_PATH = Path(".env")


@dataclass
class ConfigSource:
    """Tracks where a config value came from."""
    value: str
    source: str  # "env", ".env", "~/.mbt/config.toml", "default"


class MbtConfig:
    """Central configuration manager.

    Usage:
        config = MbtConfig()
        config = MbtConfig(project_path="other/project.toml")

    Access:
        config.hlq          -> "IBMUSER"
        config.mvs_host     -> "localhost"
        config.project.name -> "httpd"
    """

    def __init__(self, project_path: str = "project.toml"):
        self.project = ProjectConfig.load(project_path)
        self._env: dict = dict(os.environ)
        self._global: dict = self._load_global()
        self._dotenv: dict = self._load_dotenv()

    def _load_global(self) -> dict:
        """Load ~/.mbt/config.toml if present."""
        if not GLOBAL_CONFIG_PATH.exists():
            return {}
        try:
            with open(GLOBAL_CONFIG_PATH, "rb") as f:
                return tomllib.load(f)
        except Exception:
            return {}

    def _load_dotenv(self) -> dict:
        """Parse local .env file if present.

        Simple KEY=VALUE per line, # comments, no quotes handling,
        no variable expansion.
        """
        if not LOCAL_ENV_PATH.exists():
            return {}
        result = {}
        try:
            text = LOCAL_ENV_PATH.read_text(encoding="utf-8")
            for line in text.splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if "=" not in line:
                    continue
                key, _, value = line.partition("=")
                result[key.strip()] = value.strip()
        except Exception:
            pass
        return result

    def _resolve_global(self, key: str) -> str | None:
        """Navigate nested TOML dict using dotted key path."""
        parts = key.split(".")
        current = self._global
        for part in parts:
            if not isinstance(current, dict) or part not in current:
                return None
            current = current[part]
        if current is None:
            return None
        return str(current)

    def get(self, key: str) -> str:
        """Resolve config value by dotted key.

        Priority: env -> .env -> global config -> default

        Args:
            key: Dotted config key, e.g. "mvs.host"

        Returns:
            Resolved value as string

        Raises:
            KeyError: If key is unknown and has no default
        """
        if key not in _DEFAULTS and key not in _ENV_MAP:
            raise KeyError(f"Unknown config key: {key!r}")

        # 1. Environment variable
        if key in _ENV_MAP:
            env_name = _ENV_MAP[key]
            if env_name in self._env:
                return self._env[env_name]
            # 2. .env file (same env var names)
            if env_name in self._dotenv:
                return self._dotenv[env_name]

        # 3. Global config (~/.mbt/config.toml)
        global_val = self._resolve_global(key)
        if global_val is not None:
            return global_val

        # 4. Built-in default
        return _DEFAULTS.get(key, "")

    def get_sourced(self, key: str) -> ConfigSource:
        """Like get(), but also returns the source.

        Used by mbt doctor for diagnostics.
        """
        if key in _ENV_MAP:
            env_name = _ENV_MAP[key]
            if env_name in self._env:
                return ConfigSource(self._env[env_name], "env")
            if env_name in self._dotenv:
                return ConfigSource(self._dotenv[env_name], ".env")

        global_val = self._resolve_global(key)
        if global_val is not None:
            return ConfigSource(global_val, "~/.mbt/config.toml")

        default = _DEFAULTS.get(key, "")
        return ConfigSource(default, "default")

    # --- Convenience properties ---

    @property
    def hlq(self) -> str:
        return self.get("mvs.hlq")

    @property
    def deps_hlq(self) -> str:
        """Dependency HLQ. Defaults to {hlq}.DEPS.

        Set mvs.deps_hlq = "." in config to use bare dep names
        ({dep_name}.{vrm}.{suffix} with no HLQ prefix).
        """
        val = self.get("mvs.deps_hlq")
        if val == ".":
            return ""      # bare: {dep_name}.{vrm}.{suffix}
        return val if val else f"{self.hlq}.DEPS"

    @property
    def deps_volume(self) -> str:
        """Volume serial for dependency RECEIVE.

        When set, RECEIVE uses VOLUME('{deps_volume}') + DATASET(...)
        to place received datasets on a user/work volume instead of
        the public volume (PUB001). Required on MVS/CE where PUB001
        has limited free space.
        """
        return self.get("mvs.deps_volume")

    @property
    def mvs_host(self) -> str:
        return self.get("mvs.host")

    @property
    def mvs_port(self) -> int:
        return int(self.get("mvs.port"))

    @property
    def mvs_user(self) -> str:
        return self.get("mvs.user")

    @property
    def mvs_pass(self) -> str:
        return self.get("mvs.pass")

    @property
    def jes_jobclass(self) -> str:
        return self.get("jes.jobclass")

    @property
    def jes_msgclass(self) -> str:
        return self.get("jes.msgclass")

    @property
    def build_id(self) -> str | None:
        """Build ID for CI. None if not in CI."""
        val = self.get("build.id")
        return val if val else None

    @property
    def is_ci(self) -> bool:
        return self.build_id is not None

