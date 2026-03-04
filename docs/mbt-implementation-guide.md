# mbt Implementation Guide

**Companion to:** MVS Build & Package Specification v1.0.0
**Purpose:** Step-by-step implementation blueprint for Claude Code
**Language:** Python 3.12+ (stdlib only), GNU Make, Bash
**Date:** 2026-03-04

---

## How to Use This Document

This guide defines **how** and **in what order** to implement the mbt
build system specified in `mvs-build-spec-v1.0.0.md`. Read the spec
first for the **what**, then follow this guide for the **how**.

The implementation is organized into sequential milestones. Each
milestone builds on the previous one and has clear acceptance criteria.
Do not skip ahead — later modules depend on earlier ones.

---

## 1. Repository Setup

### 1.1 Directory Structure (Create First)

```
mbt/
├── bin/
│   └── mbt
├── scripts/
│   ├── mbtconfig.py
│   ├── mbtbootstrap.py
│   ├── mbtdoctor.py
│   ├── mbtgraph.py
│   ├── mbtdatasets.py
│   ├── mvsasm.py
│   ├── mvslink.py
│   ├── mvsinstall.py
│   ├── mvspackage.py
│   ├── mvsrelease.py
│   └── mbt/
│       ├── __init__.py
│       ├── config.py
│       ├── project.py
│       ├── lockfile.py
│       ├── version.py
│       ├── datasets.py
│       ├── dependencies.py
│       ├── output.py
│       ├── mvsmf.py
│       └── jcl.py
├── mk/
│   ├── core.mk
│   ├── targets.mk
│   ├── rules.mk
│   └── defaults.mk
├── templates/
│   ├── project.toml
│   └── jcl/
│       ├── alloc.jcl.tpl
│       ├── asm.jcl.tpl
│       ├── ncallink.jcl.tpl
│       ├── link.jcl.tpl
│       ├── copy.jcl.tpl
│       ├── delete.jcl.tpl
│       └── receive.jcl.tpl
├── docker/
│   ├── docker-compose.yml
│   └── Makefile
├── .github/
│   └── workflows/
│       ├── build.yml
│       └── release.yml
├── examples/
│   └── hello370/                    # Reference project (smoke test)
│       ├── project.toml
│       ├── src/hello.c
│       └── Makefile
├── Makefile
├── README.md
└── VERSION
```

### 1.2 bin/mbt (CLI Wrapper)

```bash
#!/bin/sh
# mbt CLI entrypoint
# Usage: mbt <command> [args...]
# Example: mbt doctor, mbt bootstrap --update

set -e

MBT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export MBT_ROOT

COMMAND="${1:?Usage: mbt <command> [args...]}"
shift

SCRIPT="$MBT_ROOT/scripts/mbt${COMMAND}.py"

if [ ! -f "$SCRIPT" ]; then
    echo "[mbt] ERROR: Unknown command '$COMMAND'" >&2
    echo "[mbt] Available: doctor, config, bootstrap, graph, datasets" >&2
    exit 2
fi

exec python3 "$SCRIPT" "$@"
```

Make executable: `chmod +x bin/mbt`

---

## 2. Implementation Order & Dependencies

```
                    ┌───────────┐
                    │  version  │  ← no dependencies, implement first
                    └─────┬─────┘
                          │
                    ┌─────▼─────┐
                    │  project  │  ← depends on: version
                    └─────┬─────┘
                          │
                    ┌─────▼─────┐
                    │  config   │  ← depends on: project
                    └─────┬─────┘
                          │
              ┌───────────┼───────────┐
              │           │           │
        ┌─────▼─────┐ ┌──▼───┐ ┌─────▼─────┐
        │  datasets  │ │output│ │  lockfile  │
        └─────┬─────┘ └──┬───┘ └─────┬─────┘
              │           │           │
              └───────────┼───────────┘
                          │
                ┌─────────▼─────────┐
                │  mbtconfig (CLI)  │  ← Milestone 1 complete
                │  mbtdoctor (CLI)  │
                └─────────┬─────────┘
                          │
                    ┌─────▼─────┐
                    │   mvsmf   │  ← HTTP client
                    └─────┬─────┘
                          │
                    ┌─────▼─────┐
                    │    jcl    │  ← template engine
                    └─────┬─────┘
                          │
                ┌─────────▼─────────┐
                │  dependencies     │  ← GitHub API
                │  mbtbootstrap     │
                └─────────┬─────────┘  ← Milestone 2 complete
                          │
              ┌───────────┼───────────┐
              │           │           │
        ┌─────▼──┐  ┌────▼────┐ ┌────▼──────┐
        │ mvsasm │  │ mvslink │ │mvsinstall │
        └────────┘  └─────────┘ └───────────┘
                                               ← Milestone 3 complete
              ┌───────────┼───────────┐
              │           │           │
        ┌─────▼─────┐ ┌──▼────┐ ┌────▼─────┐
        │mvspackage │ │ graph │ │ datasets │
        └───────────┘ └───────┘ └──────────┘
                                               ← Milestone 4 complete
              ┌───────────┐
              │mvsrelease │
              │  CI/CD    │
              │  Make     │
              └───────────┘                    ← Milestone 5 complete
```

---

## 3. Milestone 1: Core Library + Config CLI

### 3.1 mbt/version.py

Purpose: Semver parsing and MVS VRM conversion.

```python
"""Semantic versioning and MVS VRM format conversion.

This module handles parsing of semver version strings and
converting them to the MVS VRM (Version/Release/Modification)
naming format used in dataset qualifiers.
"""

import re
from dataclasses import dataclass

# Semver pattern: MAJOR.MINOR.PATCH[-prerelease]
# Prerelease: "dev" or "rc" followed by digits
_SEMVER_RE = re.compile(
    r"^(\d+)\.(\d+)\.(\d+)(?:-(dev|rc\d+))?$"
)


@dataclass(frozen=True)
class Version:
    """Parsed semantic version."""
    major: int
    minor: int
    patch: int
    pre: str | None = None  # None, "dev", "rc1", "rc2", ...

    @classmethod
    def parse(cls, version_str: str) -> "Version":
        """Parse a semver string.

        Args:
            version_str: Version string like "1.0.0" or "3.3.1-dev"

        Returns:
            Version instance

        Raises:
            ValueError: If version_str is not valid semver
        """
        ...

    def to_vrm(self) -> str:
        """Convert to MVS VRM format.

        Returns:
            VRM string, e.g. "V1R0M0", "V3R3M1D", "V3R3M1R1"

        Mapping:
            1.0.0     → V1R0M0
            3.3.1     → V3R3M1
            1.0.0-dev → V1R0M0D
            3.3.1-rc1 → V3R3M1R1
        """
        ...

    def __str__(self) -> str:
        """Return original semver string."""
        ...

    def _as_tuple(self) -> tuple[int, int, int, int]:
        """For comparison, including prerelease rank.

        Prerelease ranking (per semver):
            dev     → -2   (lowest)
            rc<N>   → -1   (higher than dev)
            release →  0   (highest)

        This ensures: 1.0.0-dev < 1.0.0-rc1 < 1.0.0
        """
        rank = 0
        if self.pre == "dev":
            rank = -2
        elif self.pre is not None and self.pre.startswith("rc"):
            rank = -1
        return (self.major, self.minor, self.patch, rank)

    def __lt__(self, other: "Version") -> bool: ...
    def __le__(self, other: "Version") -> bool: ...
    def __gt__(self, other: "Version") -> bool: ...
    def __ge__(self, other: "Version") -> bool: ...


def satisfies(version_str: str, constraint: str) -> bool:
    """Check if a version satisfies a constraint expression.

    Supported operators: >=, <, =
    Multiple constraints joined by comma (AND logic).

    Args:
        version_str: Version to check, e.g. "1.2.0"
        constraint: Constraint expression, e.g. ">=1.0.0,<2.0.0"

    Returns:
        True if version satisfies all constraints

    Examples:
        satisfies("1.5.0", ">=1.0.0")        → True
        satisfies("2.0.0", ">=1.0.0,<2.0.0") → False
        satisfies("1.0.0", "=1.0.0")         → True
        satisfies("1.0.0-dev", ">=1.0.0")    → False (dev < release)
    """
    ...


def to_vrm(version_str: str) -> str:
    """Convenience: parse and convert to VRM in one call."""
    return Version.parse(version_str).to_vrm()
```

### 3.2 mbt/project.py

Purpose: Parse and validate project.toml.

```python
"""project.toml parser and validator.

Loads the single project configuration file and provides
typed access to all sections. Validates required fields,
project types, dataset definitions, and space arrays.
"""

import tomllib
from dataclasses import dataclass, field
from pathlib import Path

from .version import Version


class ProjectError(Exception):
    """Raised when project.toml is invalid."""
    pass


@dataclass
class DatasetDef:
    """Parsed dataset definition from [mvs.build.datasets.*]."""
    key: str           # TOML key, e.g. "ncalib"
    suffix: str        # e.g. "NCALIB"
    dsorg: str         # "PO" or "PS"
    recfm: str         # "FB", "VB", "U"
    lrecl: int
    blksize: int
    space: list        # ["TRK", pri, sec, dir] or ["TRK", pri, sec]
    unit: str = "SYSDA"
    volume: str | None = None
    local_dir: str | None = None  # local dir to upload during bootstrap

    def space_unit(self) -> str:
        return self.space[0]

    def space_primary(self) -> int:
        return self.space[1]

    def space_secondary(self) -> int:
        return self.space[2]

    def space_dirblks(self) -> int | None:
        """Returns dirblks for PO, None for PS."""
        return self.space[3] if len(self.space) == 4 else None


@dataclass
class LinkModule:
    """Parsed [[link.module]] entry."""
    name: str          # load module name, e.g. "HTTPD"
    entry: str         # entry point
    options: list[str] # IEWL options, e.g. ["RENT", "REUS"]
    include: list[str] # NCALIB members to include


@dataclass
class InstallDataset:
    """Parsed [mvs.install.datasets.*] entry."""
    key: str           # matches build dataset key
    name: str          # e.g. "HTTPD.LOAD"


@dataclass
class ProjectConfig:
    """Complete parsed project configuration."""

    # [project]
    name: str
    version: str
    type: str          # "runtime", "library", "module", "application"

    # [build]
    cflags: list[str] = field(default_factory=list)
    c_dirs: list[str] = field(default_factory=lambda: ["src/"])
    asm_dirs: list[str] = field(default_factory=list)  # default: empty

    # [dependencies]
    dependencies: dict[str, str] = field(default_factory=dict)

    # [mvs.asm]
    max_rc: int = 4

    # [mvs.build.datasets.*]
    build_datasets: dict[str, DatasetDef] = field(default_factory=dict)

    # [mvs.install]
    install_naming: str | None = None  # "fixed" or "vrm"
    install_datasets: dict[str, InstallDataset] = field(default_factory=dict)

    # [link]
    link_modules: list[LinkModule] = field(default_factory=list)

    # [artifacts]
    artifact_headers: bool = False
    artifact_mvs: bool = False
    artifact_bundle: bool = False

    # [release]
    release_github: str | None = None   # "owner/repo"
    release_version_files: list[str] = field(default_factory=list)

    @classmethod
    def load(cls, path: str | Path = "project.toml") -> "ProjectConfig":
        """Load and validate project.toml.

        Args:
            path: Path to project.toml

        Returns:
            Validated ProjectConfig instance

        Raises:
            ProjectError: If file is missing or invalid
            FileNotFoundError: If file does not exist
        """
        ...

    @classmethod
    def _parse(cls, data: dict) -> "ProjectConfig":
        """Parse raw TOML dict into ProjectConfig.

        Handles all section parsing, default values, and
        type conversion. Does NOT validate — call _validate()
        after parsing.
        """
        ...

    def _validate(self) -> None:
        """Validate the parsed config.

        Checks:
        - Required fields present
        - project.type is valid
        - Dataset space arrays have correct length
        - PO datasets have 4-element space (with dirblks)
        - PS datasets have 3-element space
        - link section only for application/module types
        - install section references existing build datasets

        Raises:
            ProjectError: With descriptive message
        """
        ...

    @property
    def parsed_version(self) -> Version:
        """Return parsed Version object."""
        return Version.parse(self.version)

    @property
    def vrm(self) -> str:
        """Return MVS VRM string for this version."""
        return self.parsed_version.to_vrm()

    def datasets_with_local_dir(self) -> list[DatasetDef]:
        """Return datasets that have local_dir set (need upload)."""
        return [ds for ds in self.build_datasets.values()
                if ds.local_dir is not None]
```

**Parsing rules:**

- `[build.sources]` is optional. If absent, use defaults.
  If `c_dirs` is present, use it. If `asm_dirs` is present, use it.
  Partial override is allowed: only `c_dirs` set → `asm_dirs` stays `[]`.
- `[dependencies]` keys are `"owner/repo"` strings.
- `[[link.module]]` is a TOML array of tables.
- `[mvs.install.datasets.*]` keys SHOULD match build dataset keys.

### 3.3 mbt/config.py

Purpose: Merge all configuration sources. Central config provider.

```python
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
from dataclasses import dataclass, field

from .project import ProjectConfig


# Default values for all config keys
_DEFAULTS = {
    "mvs.host":      "localhost",
    "mvs.port":      "1080",
    "mvs.user":      "IBMUSER",
    "mvs.pass":      "",
    "mvs.hlq":       "IBMUSER",
    "mvs.deps_hlq":  "",          # computed: {hlq}.DEPS
    "jes.jobclass":  "A",
    "jes.msgclass":  "H",
    "build.id":      "",          # empty = not CI
}

# Mapping: dotted config key → environment variable name
_ENV_MAP = {
    "mvs.host":      "MBT_MVS_HOST",
    "mvs.port":      "MBT_MVS_PORT",
    "mvs.user":      "MBT_MVS_USER",
    "mvs.pass":      "MBT_MVS_PASS",
    "mvs.hlq":       "MBT_MVS_HLQ",
    "mvs.deps_hlq":  "MBT_MVS_DEPS_HLQ",
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
        config.hlq          → "IBMUSER"
        config.mvs_host     → "localhost"
        config.project.name → "httpd"
    """

    def __init__(self, project_path: str = "project.toml"):
        self.project = ProjectConfig.load(project_path)
        self._global: dict = self._load_global()
        self._dotenv: dict = self._load_dotenv()

    def _load_global(self) -> dict: ...
    def _load_dotenv(self) -> dict: ...

    def get(self, key: str) -> str:
        """Resolve config value by dotted key.

        Priority: env → .env → global config → default

        Args:
            key: Dotted config key, e.g. "mvs.host"

        Returns:
            Resolved value as string

        Raises:
            KeyError: If key is unknown and has no default
        """
        ...

    def get_sourced(self, key: str) -> ConfigSource:
        """Like get(), but also returns the source.

        Used by mbt doctor for diagnostics.
        """
        ...

    # --- Convenience properties ---

    @property
    def hlq(self) -> str:
        return self.get("mvs.hlq")

    @property
    def deps_hlq(self) -> str:
        """Dependency HLQ. Defaults to {hlq}.DEPS."""
        val = self.get("mvs.deps_hlq")
        return val if val else f"{self.hlq}.DEPS"

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

    @property
    def system_maclibs(self) -> list[str]:
        """System macro libraries from global config.

        Reads [system.maclibs] section. Returns values
        in declaration order.
        """
        section = self._global.get("system", {}).get("maclibs", {})
        return list(section.values())
```

**dotenv parsing:** Simple `KEY=VALUE` per line, `#` comments, no
quotes handling needed, no variable expansion. Keep it simple.

**Global config TOML navigation:** For nested keys like `mvs.host`,
walk the TOML dict: `data["mvs"]["host"]`. Use a helper method.

### 3.4 mbt/datasets.py

Purpose: Compute fully qualified MVS dataset names.

```python
"""Dataset name computation.

Resolves dataset names from project config, version, HLQ,
and dependency information. Handles build datasets, dependency
datasets, install datasets, and CI build-ID overrides.
"""

from dataclasses import dataclass
from .config import MbtConfig
from .version import to_vrm


@dataclass
class ResolvedDataset:
    """A fully qualified dataset with all attributes."""
    dsn: str           # e.g. "IBMUSER.HTTPD.V3R3M1.NCALIB"
    key: str           # e.g. "ncalib"
    suffix: str        # e.g. "NCALIB"
    dsorg: str
    recfm: str
    lrecl: int
    blksize: int
    space: list
    unit: str
    volume: str | None
    local_dir: str | None


class DatasetResolver:
    """Compute dataset names from config.

    Results are cached internally — methods can be called
    multiple times without recomputation.
    """

    def __init__(self, config: MbtConfig):
        self.config = config
        self._build_cache: dict[str, ResolvedDataset] | None = None
        self._install_cache: dict[str, ResolvedDataset] | None = None

    def build_datasets(self) -> dict[str, ResolvedDataset]:
        """Compute build dataset names.

        Pattern: {HLQ}.{PROJECT}.{VRM}.{SUFFIX}
        CI mode: {HLQ}.{PROJECT}.B{BUILD_ID}.{SUFFIX}

        Results are cached after first call.
        """
        if self._build_cache is None:
            self._build_cache = self._compute_build_datasets()
        return self._build_cache

    def _compute_build_datasets(self) -> dict[str, ResolvedDataset]:
        """Internal: compute without caching."""
        ...

    def dependency_datasets(self,
                            lockfile_deps: dict[str, str],
                            package_cache: dict
                            ) -> dict[str, list[ResolvedDataset]]:
        """Compute dependency dataset names.

        Pattern: {DEPS_HLQ}.{DEP_NAME}.{DEP_VRM}.{SUFFIX}

        Args:
            lockfile_deps: {"mvslovers/crent370": "1.0.0", ...}
            package_cache: cached package.toml data per dep

        Returns:
            {"mvslovers/crent370": [ResolvedDataset, ...], ...}
        """
        ...

    def install_datasets(self) -> dict[str, ResolvedDataset]:
        """Compute install dataset names.

        Pattern depends on install_naming:
          "fixed": {HLQ}.{name}
          "vrm":   {HLQ}.{PROJECT}.{VRM}.{SUFFIX}

        Results are cached after first call.
        """
        if self._install_cache is None:
            self._install_cache = self._compute_install_datasets()
        return self._install_cache

    def _compute_install_datasets(self) -> dict[str, ResolvedDataset]:
        """Internal: compute without caching."""
        ...

    def syslib_maclibs(self,
                       lockfile_deps: dict[str, str],
                       package_cache: dict
                       ) -> list[str]:
        """Build SYSLIB MACLIB concatenation list.

        Order (fixed, per spec section 8.3):
        1. Project's own MACLIB (if defined)
        2. Dependency MACLIBs (declaration order)
        3. System MACLIBs (from config)

        Returns:
            List of fully qualified dataset names
        """
        ...

    def syslib_ncalibs(self,
                       lockfile_deps: dict[str, str],
                       package_cache: dict
                       ) -> list[str]:
        """Build NCALIB concatenation list for IEWL.

        Order: Project's NCALIB first, then deps in
        declaration order.
        """
        ...
```

### 3.5 mbt/lockfile.py

Purpose: Read and write `.mbt/mvs.lock`.

```python
"""Lockfile management.

The lockfile pins exact dependency versions for
reproducible builds. Format:

    [metadata]
    generated   = "2026-03-04T10:30:00Z"
    mbt_version = "1.0.0"

    [dependencies]
    "mvslovers/crent370" = "1.0.0"
"""

import tomllib
from pathlib import Path
from dataclasses import dataclass

LOCKFILE_PATH = Path(".mbt") / "mvs.lock"


@dataclass
class Lockfile:
    """Parsed lockfile."""
    generated: str
    mbt_version: str
    dependencies: dict[str, str]  # {"owner/repo": "exact_version"}

    @classmethod
    def load(cls, path: Path = LOCKFILE_PATH) -> "Lockfile | None":
        """Load lockfile. Returns None if not found."""
        ...

    def save(self, path: Path = LOCKFILE_PATH) -> None:
        """Write lockfile as TOML.

        Creates parent directories if needed.
        Includes header comment: AUTO-GENERATED — DO NOT EDIT
        """
        ...

    @classmethod
    def create(cls, dependencies: dict[str, str],
               mbt_version: str) -> "Lockfile":
        """Create new lockfile with current timestamp."""
        ...
```

**Write format:** Since we only read TOML (via tomllib), we write
the lockfile manually as formatted text. It's simple enough:

```python
def save(self, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        "# .mbt/mvs.lock",
        "# AUTO-GENERATED by mbt bootstrap — DO NOT EDIT",
        "",
        "[metadata]",
        f'generated   = "{self.generated}"',
        f'mbt_version = "{self.mbt_version}"',
        "",
        "[dependencies]",
    ]
    for dep, ver in sorted(self.dependencies.items()):
        lines.append(f'"{dep}" = "{ver}"')
    path.write_text("\n".join(lines) + "\n")
```

### 3.6 mbt/output.py

Purpose: Format config data for different consumers.

```python
"""Output formatters for mbtconfig.

Supports multiple output formats:
- shell: KEY=VALUE lines for Make $(eval ...)
- json:  JSON object for debugging/scripting
- doctor: human-readable diagnostics
"""


def format_shell(variables: dict[str, str]) -> str:
    """Format as shell variable assignments.

    Output is designed for Make's $(eval ...) function:
        PROJECT_NAME=httpd
        PROJECT_VERSION=3.3.1-dev
        BUILD_DS_NCALIB=IBMUSER.HTTPD.V3R3M1.NCALIB

    Rules:
    - No quoting (Make doesn't need it for simple values)
    - Lists are space-separated
    - One variable per line
    """
    ...


def format_json(variables: dict) -> str:
    """Format as JSON for debugging."""
    ...


def format_doctor(sourced_values: dict[str, "ConfigSource"]) -> str:
    """Format config diagnostics showing value sources.

    Example output:
        [mbt] Configuration:
          MVS_HOST     = localhost        [default]
          MVS_PORT     = 1080             [~/.mbt/config.toml]
          MVS_HLQ      = CIUSER          [env]
    """
    ...
```

### 3.7 scripts/mbtconfig.py (CLI)

Purpose: CLI entrypoint for config query.

```python
"""mbt config CLI.

Usage:
    mbtconfig.py --output shell    # for Make $(eval ...)
    mbtconfig.py --output json     # for debugging
    mbtconfig.py --get <key>       # single value
    mbtconfig.py --validate        # check project.toml
    mbtconfig.py --doctor          # show sources
"""

import sys
import argparse
from pathlib import Path

# Add parent to path for mbt package import
sys.path.insert(0, str(Path(__file__).parent))

from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.lockfile import Lockfile
from mbt.output import format_shell, format_json, format_doctor


def build_variables(config: MbtConfig) -> dict[str, str]:
    """Build the complete variable dict for shell output.

    This is the central function that computes ALL values
    that Make and executor scripts need:

    PROJECT_NAME, PROJECT_VERSION, PROJECT_TYPE, PROJECT_VRM,
    MBT_VERSION (read from mbt/VERSION file),
    CC, CFLAGS, INCLUDES,
    MVS_HOST, MVS_PORT, MVS_USER, MVS_HLQ, DEPS_HLQ,
    JES_JOBCLASS, JES_MSGCLASS,
    BUILD_DS_<KEY>=<DSN> for each build dataset,
    DEP_<NAME>_VERSION, DEP_<NAME>_VRM,
    DEP_<NAME>_<SUFFIX>=<DSN> for each dependency dataset,
    DEP_<NAME>_HEADERS=contrib/<name>-<ver>/include,
    SYSLIB_MACLIBS (space-separated list),
    SYSLIB_NCALIBS (space-separated list),
    INCLUDES (compiler -I flags, space-separated),
    C_DIRS (space-separated list),
    ASM_DIRS (space-separated list)
    """
    ...


def main():
    parser = argparse.ArgumentParser(description="mbt configuration")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--output", choices=["shell", "json"])
    group.add_argument("--get", metavar="KEY")
    group.add_argument("--validate", action="store_true")
    group.add_argument("--doctor", action="store_true")

    parser.add_argument("--project", default="project.toml")
    args = parser.parse_args()

    try:
        config = MbtConfig(project_path=args.project)
    except Exception as e:
        print(f"[mbt] ERROR: {e}", file=sys.stderr)
        sys.exit(2)

    if args.validate:
        print("[mbt] project.toml is valid")
        sys.exit(0)

    if args.doctor:
        # ... print sourced values
        sys.exit(0)

    if args.get:
        # ... print single value
        sys.exit(0)

    variables = build_variables(config)

    if args.output == "shell":
        print(format_shell(variables))
    elif args.output == "json":
        print(format_json(variables))


if __name__ == "__main__":
    main()
```

### 3.8 scripts/mbtdoctor.py (CLI)

Purpose: Environment verification.

```python
"""mbt doctor — environment verification.

Checks (in order):
1. Python version >= 3.12
2. c2asm370 on PATH
3. make on PATH
4. MVS host reachable (HTTP GET)
5. MVS credentials valid
6. project.toml valid
7. Config source report

Exit codes:
  0 = all checks passed
  2 = one or more checks failed
"""
```

### 3.9 Milestone 1 Acceptance Criteria

```
□ mbt/version.py: Version.parse() and to_vrm() work for all
  documented formats (1.0.0, 3.3.1-dev, 3.3.1-rc1)
□ mbt/version.py: satisfies() handles >=, <, = and combinations
□ mbt/version.py: Prerelease ordering: 1.0.0-dev < 1.0.0-rc1 < 1.0.0
□ mbt/project.py: Loads and validates the httpd example from spec
□ mbt/project.py: Rejects missing required fields with clear error
□ mbt/project.py: Validates space array length (4 for PO, 3 for PS)
□ mbt/config.py: Resolves values in correct priority order
□ mbt/config.py: Reads ~/.mbt/config.toml if present
□ mbt/config.py: Reads .env if present
□ mbt/config.py: MBT_* env vars override everything
□ mbt/datasets.py: Correct DSN for build, dep, install datasets
□ mbt/datasets.py: CI mode uses B{ID} instead of VRM
□ mbt/datasets.py: deps_hlq is correctly applied
□ mbt/datasets.py: SYSLIB order = project → deps (decl order) → system
□ mbtconfig --output shell: produces valid Make eval output
□ mbtconfig --output shell: includes MBT_VERSION from VERSION file
□ mbtconfig --validate: validates project.toml
□ mbtdoctor: checks Python, c2asm370, make, MVS connectivity
□ All exit codes follow spec (0, 1, 2, 3, 4, 5, 99)
```

---

## 4. Milestone 2: Mainframe Communication + Bootstrap

### 4.1 mbt/mvsmf.py

Purpose: HTTP client for mvsMF REST API.

```python
"""mvsMF REST API client.

Communicates with the mvsMF server on MVS via z/OSMF-compatible
REST endpoints. Uses only Python stdlib (urllib).

Base URL: http://{host}:{port}/zosmf
Auth: HTTP Basic

All methods raise MvsMFError on communication failures.
"""

import json
import time
import urllib.request
import urllib.error
import base64
from dataclasses import dataclass


class MvsMFError(Exception):
    """Raised on mvsMF communication errors."""
    pass


@dataclass
class JobResult:
    """Result of a submitted JCL job."""
    jobid: str
    jobname: str
    rc: int           # 0, 4, 8, ... or 9999 for ABEND
    status: str       # "CC", "ABEND", "JCL ERROR", "TIMEOUT"
    spool: str        # concatenated spool output

    @property
    def success(self) -> bool:
        return self.status == "CC"

    @property
    def abended(self) -> bool:
        return self.status == "ABEND"


class MvsMFClient:
    """mvsMF REST API client.

    Usage:
        client = MvsMFClient("localhost", 1080, "IBMUSER", "sys1")
        result = client.submit_jcl("//JOB ...")
    """

    def __init__(self, host: str, port: int,
                 user: str, password: str):
        self._base_url = f"http://{host}:{port}/zosmf"
        self._auth = base64.b64encode(
            f"{user}:{password}".encode()
        ).decode()

    # --- Internal HTTP ---

    def _request(self, method: str, path: str,
                 body: bytes | None = None,
                 content_type: str = "application/json",
                 accept: str = "application/json"
                 ) -> bytes:
        """Execute HTTP request against mvsMF.

        Args:
            method: HTTP method
            path: URL path (appended to base URL)
            body: Request body
            content_type: Content-Type header
            accept: Accept header

        Returns:
            Response body as bytes

        Raises:
            MvsMFError: On HTTP errors or connection failures
        """
        ...

    def _json_request(self, method: str, path: str,
                      body: dict | None = None) -> dict:
        """JSON request/response convenience wrapper."""
        ...

    # --- Job Operations ---

    def submit_jcl(self, jcl_text: str,
                   wait: bool = True,
                   timeout: int = 120) -> JobResult:
        """Submit inline JCL and wait for completion.

        Endpoint: PUT /zosmf/restjobs/jobs
        Content-Type: text/plain

        Args:
            jcl_text: Complete JCL including JOB card
            wait: If True, poll until job completes
            timeout: Max seconds to wait

        Returns:
            JobResult with RC and spool output
        """
        ...

    def _poll_job(self, jobname: str, jobid: str,
                  timeout: int) -> JobResult:
        """Poll job status until OUTPUT or timeout.

        Endpoint: GET /zosmf/restjobs/jobs/{name}/{id}

        Uses progressive backoff to reduce REST load:
        1s, 2s, 3s, 5s, 5s, 5s, ...
        """
        ...

    def _collect_spool(self, jobname: str,
                       jobid: str) -> str:
        """Collect all spool files for a job.

        Endpoint: GET /zosmf/restjobs/jobs/{name}/{id}/files
        Endpoint: GET /zosmf/restjobs/jobs/{name}/{id}/files/{n}/records
        """
        ...

    @staticmethod
    def _parse_retcode(retcode_str: str | None) -> tuple[int, str]:
        """Parse z/OSMF retcode string.

        "CC 0000"    → (0, "CC")
        "CC 0004"    → (4, "CC")
        "ABEND S0C4" → (9999, "ABEND")
        "JCL ERROR"  → (9998, "JCL ERROR")
        None         → (-1, "UNKNOWN")
        """
        ...

    # --- Dataset Operations ---

    def dataset_exists(self, dsn: str) -> bool:
        """Check if dataset exists.

        Endpoint: GET /zosmf/restfiles/ds?dslevel={dsn}
        """
        ...

    def list_datasets(self, prefix: str) -> list[dict]:
        """List datasets matching prefix.

        Endpoint: GET /zosmf/restfiles/ds?dslevel={prefix}

        Returns:
            List of {"dsname": "...", "dsorg": "..."} dicts
        """
        ...

    def create_dataset(self, dsn: str, dsorg: str,
                       recfm: str, lrecl: int, blksize: int,
                       space: list, unit: str = "SYSDA",
                       volume: str | None = None) -> None:
        """Create a new dataset.

        Endpoint: POST /zosmf/restfiles/ds/{dsn}
        Body: JSON with DCB attributes

        The space array is mapped to the JSON body:
          ["TRK", 10, 5, 10] → alcunit="TRK", primary=10,
                                secondary=5, dirblk=10
        """
        ...

    def delete_dataset(self, dsn: str) -> None:
        """Delete a dataset.

        Endpoint: DELETE /zosmf/restfiles/ds/{dsn}
        """
        ...

    def list_members(self, dsn: str) -> list[str]:
        """List PDS members.

        Endpoint: GET /zosmf/restfiles/ds/{dsn}/member

        Returns:
            List of member names
        """
        ...

    def write_member(self, dsn: str, member: str,
                     content: str) -> None:
        """Write text content to PDS member.

        Endpoint: PUT /zosmf/restfiles/ds/{dsn}({member})
        Content-Type: text/plain
        """
        ...

    def read_member(self, dsn: str, member: str) -> str:
        """Read PDS member content.

        Endpoint: GET /zosmf/restfiles/ds/{dsn}({member})
        """
        ...

    def upload_binary(self, dsn: str,
                      data: bytes) -> None:
        """Upload binary data to sequential dataset.

        Used for XMIT file uploads.

        Endpoint: PUT /zosmf/restfiles/ds/{dsn}
        Content-Type: application/octet-stream
        X-IBM-Data-Type: binary
        """
        ...

    # --- Connectivity ---

    def ping(self) -> bool:
        """Test connectivity to mvsMF.

        Returns True if server responds.
        """
        ...
```

### 4.2 mbt/jcl.py

Purpose: JCL template rendering.

```python
"""JCL template rendering.

Uses string.Template for variable substitution and
Python helper functions for dynamic sections (SYSLIB concat).

Templates are .tpl files in mbt/templates/jcl/.
Variables use $NAME or ${NAME} syntax.
"""

from string import Template
from pathlib import Path


# Template directory relative to this file
_TEMPLATE_DIR = Path(__file__).parent.parent.parent / "templates" / "jcl"


def render_template(template_name: str,
                    variables: dict[str, str]) -> str:
    """Render a JCL template with variables.

    Args:
        template_name: e.g. "asm.jcl.tpl"
        variables: Template variables

    Returns:
        Rendered JCL text
    """
    tpl_path = _TEMPLATE_DIR / template_name
    tpl = Template(tpl_path.read_text())
    return tpl.safe_substitute(variables)


def render_syslib_concat(datasets: list[str]) -> str:
    """Generate SYSLIB DD concatenation JCL fragment.

    Args:
        datasets: List of fully qualified dataset names

    Returns:
        JCL fragment:
          //SYSLIB   DD DSN=first,DISP=SHR
          //         DD DSN=second,DISP=SHR
    """
    if not datasets:
        return "//SYSLIB   DD DUMMY"
    lines = [f"//SYSLIB   DD DSN={datasets[0]},DISP=SHR"]
    for dsn in datasets[1:]:
        lines.append(f"//         DD DSN={dsn},DISP=SHR")
    return "\n".join(lines)


def render_include_concat(members: list[str],
                          dsn: str) -> str:
    """Generate INCLUDE statements for IEWL linkedit.

    Args:
        members: List of member names to include
        dsn: Dataset name containing the members

    Returns:
        JCL fragment:
          INCLUDE NCALIB(MEMBER1)
          INCLUDE NCALIB(MEMBER2)
    """
    ...


def jobcard(jobname: str, jobclass: str,
            msgclass: str, description: str = "MBT"
            ) -> str:
    """Generate a standard JOB card.

    Jobname is truncated to 8 characters (MVS limit).
    """
    jn = jobname[:8].upper()
    return (
        f"//{jn} JOB ({jobclass}),'{description}',\n"
        f"//          CLASS={jobclass},"
        f"MSGCLASS={msgclass},\n"
        f"//          MSGLEVEL=(1,1)"
    )
```

### 4.3 mbt/dependencies.py

Purpose: Resolve dependencies via GitHub Releases API.

```python
"""Dependency resolution via GitHub Releases.

Queries the GitHub Releases API to find the highest
version satisfying each dependency constraint. Downloads
release assets and manages the local cache.
"""

import json
import tarfile
import urllib.request
from pathlib import Path
from dataclasses import dataclass

from .version import Version, satisfies
from .lockfile import Lockfile

CACHE_DIR = Path.home() / ".mbt" / "cache"


@dataclass
class ResolvedDependency:
    """A resolved dependency with download URLs."""
    owner: str
    repo: str
    version: str
    assets: dict[str, str]  # {"package.toml": url, ...}


def resolve_dependencies(
    declared: dict[str, str],
    lockfile: Lockfile | None = None,
    update: bool = False
) -> dict[str, str]:
    """Resolve dependency versions.

    If lockfile exists and update=False, use pinned versions.
    Otherwise, query GitHub API for latest matching versions.

    Args:
        declared: {"mvslovers/crent370": ">=1.0.0", ...}
        lockfile: Existing lockfile (may be None)
        update: If True, ignore lockfile and re-resolve

    Returns:
        {"mvslovers/crent370": "1.0.0", ...} exact versions

    Raises:
        DependencyError: If resolution fails
    """
    ...


def download_dependency(owner: str, repo: str,
                        version: str) -> Path:
    """Download dependency assets to cache.

    Cache structure:
        ~/.mbt/cache/{owner}/{repo}/{version}/
            package.toml
            {name}-{version}-headers.tar.gz
            {name}-{version}-mvs.tar.gz

    Skips download if cache is populated.

    Returns:
        Path to cache directory
    """
    ...


def extract_headers(cache_dir: Path,
                    dep_name: str,
                    dep_version: str) -> Path:
    """Extract headers tarball to contrib/.

    Extracts to: contrib/{name}-{version}/include/

    Returns:
        Path to include directory
    """
    ...


def load_package_toml(owner: str, repo: str,
                      version: str) -> dict:
    """Load package.toml from cache.

    Returns parsed TOML dict.
    """
    ...


class DependencyError(Exception):
    pass
```

### 4.4 scripts/mbtbootstrap.py

Purpose: Orchestrate the full bootstrap process.

```python
"""mbt bootstrap — dependency resolution and provisioning.

Steps (in order):
1. Validate project.toml
2. Resolve dependencies (lockfile or fresh)
3. Download dependency assets (cache-aware)
4. Extract headers → contrib/{dep}-{version}/include/
5. Upload XMIT files to mainframe
6. RECEIVE XMIT files (TSO-in-Batch)
7. Allocate project build datasets (skip if existing)
8. Upload local_dir contents to corresponding datasets

Usage:
    mbtbootstrap.py                 # use lockfile
    mbtbootstrap.py --update        # re-resolve all deps

Exit codes per spec section 11.1
"""
```

### 4.5 Milestone 2 Acceptance Criteria

```
□ mvsmf.py: Can ping() a running mvsMF instance
□ mvsmf.py: Can submit_jcl() and get RC back
□ mvsmf.py: Job polling uses progressive backoff (1s → 2s → 3s → 5s)
□ mvsmf.py: Spool output is captured on failure
□ mvsmf.py: dataset_exists(), create_dataset(), delete_dataset() work
□ mvsmf.py: write_member() and read_member() round-trip text
□ mvsmf.py: upload_binary() can send an XMIT file
□ jcl.py: render_template() substitutes all variables
□ jcl.py: render_syslib_concat() produces correct JCL
□ dependencies.py: Can query GitHub Releases API
□ dependencies.py: Selects correct version for constraint
□ dependencies.py: Downloads and caches assets
□ dependencies.py: Extracts headers to contrib/
□ mbtbootstrap: Full bootstrap on clean environment succeeds
□ mbtbootstrap: Second run with lockfile skips resolution
□ mbtbootstrap: --update forces re-resolution
□ mbtbootstrap: Existing datasets produce warning, not error
□ mbtbootstrap: local_dir contents uploaded to MACLIB
```

---

## 5. Milestone 3: Build Executors

### 5.1 scripts/mvsasm.py

```python
"""MVS Assembler executor.

Compiles and assembles sources on the MVS mainframe:
1. For each source in c_dirs: already cross-compiled to .s
2. For each source in asm_dirs: native assembler
3. Upload source to MVS
4. Submit assembly JCL
5. Check RC against max_rc
6. On failure: capture spool, write to .mbt/logs/

Log format (per spec section 11.2):
    [mvsasm] Assembling HTTPD...
    [mvsasm] HTTPD assembled (RC=0)
    [mvsasm] ERROR: HTTPSRV failed (RC=8, max_rc=4)
"""
```

### 5.2 scripts/mvslink.py

```python
"""MVS Linkedit executor.

Reads [[link.module]] entries from project.toml and
submits IEWL linkedit JCL for each module.

For project types without [link]: prints info, exits 0.
"""
```

### 5.3 scripts/mvsinstall.py

```python
"""MVS Install executor.

Copies members from build datasets to install datasets
using IEBCOPY (replace mode).

For project types without [mvs.install]: prints info, exits 0.
"""
```

### 5.4 Milestone 3 Acceptance Criteria

```
□ mvsasm: Assembles a single .s file on MVS, gets RC=0
□ mvsasm: Handles RC=4 as warning (default max_rc)
□ mvsasm: Fails on RC=8 with spool capture
□ mvsasm: Writes failure log to .mbt/logs/
□ mvsasm: SYSLIB concatenation in correct order
□ mvsasm: Handles both c_dirs and asm_dirs sources
□ mvslink: Links a module with RENT/REUS options
□ mvslink: Skips gracefully for library projects
□ mvsinstall: Copies to fixed-named install datasets
□ mvsinstall: Creates install dataset if missing
□ mvsinstall: Skips gracefully for library projects
```

---

## 6. Milestone 4: Packaging + Utility Commands

### 6.1 scripts/mvspackage.py

```python
"""Package executor.

Creates release artifacts:
1. package.toml (auto-generated)
2. {name}-{version}-headers.tar.gz
3. {name}-{version}-mvs.tar.gz (XMIT files downloaded from MVS)
4. {name}-{version}-bundle.tar.gz (applications only)

Output directory: dist/
"""
```

### 6.2 scripts/mbtgraph.py

```python
"""mbt graph — dependency tree display.

Reads lockfile + cached package.toml files to build
and display the full dependency tree:

    httpd v3.3.1-dev
     ├─ crent370 v1.0.0
     ├─ ufs370 v1.0.0
     │   └─ crent370 v1.0.0
     └─ mqtt370 v1.0.0
         ├─ crent370 v1.0.0
         └─ lua370 v1.0.0
"""
```

### 6.3 scripts/mbtdatasets.py

```python
"""mbt datasets — mainframe dataset management.

Shows all project-related datasets and their status.
Supports --delete-build and --delete-deps flags.
Supports --check for CI validation.
"""
```

### 6.4 Milestone 4 Acceptance Criteria

```
□ mvspackage: Generates valid package.toml
□ mvspackage: Creates headers tarball with correct structure
□ mvspackage: Creates MVS tarball with XMIT files
□ mbtgraph: Displays tree from lockfile + package cache
□ mbtgraph: Shows transitive deps from cached package.toml
□ mbtdatasets: Lists build, dep, and install datasets
□ mbtdatasets: Shows exists/missing status
□ mbtdatasets: --delete-build removes build datasets
□ mbtdatasets: --check exits non-zero if datasets missing
```

---

## 7. Milestone 5: Make Integration + CI/CD

### 7.1 mk/core.mk

```makefile
# mk/core.mk — Main include file for consumer projects.
#
# Consumer Makefile needs only:
#   MBT_ROOT := mbt
#   include $(MBT_ROOT)/mk/core.mk

# Resolve paths
MBT_SCRIPTS := $(MBT_ROOT)/scripts
MBT_BIN     := $(MBT_ROOT)/bin

# Load defaults
include $(MBT_ROOT)/mk/defaults.mk

# Load config from Python (single invocation)
BUILD_VARS := $(shell python3 $(MBT_SCRIPTS)/mbtconfig.py \
    --project project.toml --output shell 2>/dev/null)

ifneq ($(.SHELLSTATUS),0)
$(error [mbt] Failed to load config. Run: make doctor)
endif

$(eval $(BUILD_VARS))

# Load rules and targets
include $(MBT_ROOT)/mk/rules.mk
include $(MBT_ROOT)/mk/targets.mk
```

### 7.2 mk/defaults.mk

```makefile
# mk/defaults.mk — Build defaults

CC       ?= c2asm370
CFLAGS   := -S -O1

# Convention directories
SRC_DIRS  ?= src/
ASM_DIRS  ?=
```

### 7.3 mk/targets.mk

```makefile
# mk/targets.mk — Standard build targets

.PHONY: doctor bootstrap build link install package \
        release clean distclean run-mvs stop-mvs

doctor:
	@python3 $(MBT_SCRIPTS)/mbtdoctor.py

bootstrap:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py $(ARGS)

build: _cross_compile _assemble

_cross_compile:
	@# Cross-compile C sources via c2asm370
	@# Uses C_DIRS from mbtconfig output
	@for dir in $(C_DIRS); do \
	    for src in $$dir*.c; do \
	        [ -f "$$src" ] || continue; \
	        base=$$(basename $$src .c); \
	        echo "[mbt] Cross-compiling $$src..."; \
	        $(CC) $(CFLAGS) $(PROJECT_CFLAGS) $(INCLUDES) \
	            -o asm/$$base.s $$src || exit 1; \
	    done; \
	done

_assemble:
	@python3 $(MBT_SCRIPTS)/mvsasm.py

link:
	@python3 $(MBT_SCRIPTS)/mvslink.py

install:
	@python3 $(MBT_SCRIPTS)/mvsinstall.py

package:
	@python3 $(MBT_SCRIPTS)/mvspackage.py

release:
	@python3 $(MBT_SCRIPTS)/mvsrelease.py $(VERSION)

clean:
	@echo "[mbt] Cleaning build artifacts..."
	@rm -rf asm/*.s .mbt/logs/
	@python3 $(MBT_SCRIPTS)/mbtdatasets.py --delete-build --quiet

distclean: clean
	@echo "[mbt] Deep clean..."
	@rm -rf contrib/ .mbt/
	@python3 $(MBT_SCRIPTS)/mbtdatasets.py --delete-build --quiet

# Docker targets
run-mvs:
	@$(MAKE) -C $(MBT_ROOT)/docker up

stop-mvs:
	@$(MAKE) -C $(MBT_ROOT)/docker down
```

### 7.4 mk/rules.mk

```makefile
# mk/rules.mk — Pattern rules
#
# The cross-compilation is handled in targets.mk _cross_compile.
# Assembly and MVS operations are handled by Python executors.
# This file is reserved for additional pattern rules if needed.

# Ensure asm output directory exists
$(shell mkdir -p asm)
```

### 7.5 scripts/mvsrelease.py

```python
"""Release executor.

Local-only workflow:
1. Update version in all version_files
2. git add changed files
3. git commit -m "Release v{version}"
4. git tag v{version}
5. git push origin main --tags

CI handles: distclean → bootstrap → build → link → package →
            GitHub Release creation.

Usage:
    mvsrelease.py 3.3.1
"""
```

### 7.6 CI Workflows

Implement the shared workflows as specified in spec sections 15.1
and 15.2. Key points:

- `build.yml`: workflow_call with secrets for MVS connection
- `release.yml`: triggered on version tags, validates tag vs project.toml
- Both set `MBT_BUILD_ID` from `github.run_number`
- Both cache `~/.mbt/cache/` keyed on lockfile hash
- Both upload `.mbt/logs/` as artifact (always, even on failure)
- Release workflow runs distclean → bootstrap → build → link → package
- Post-step: `make distclean` for CI dataset cleanup

### 7.7 Milestone 5 Acceptance Criteria

```
□ Consumer project with 2-line Makefile builds successfully
□ make doctor runs all checks
□ make bootstrap resolves deps and provisions datasets
□ make build cross-compiles and assembles on MVS
□ make link produces load module
□ make install copies to install datasets
□ make package creates dist/ with all artifacts
□ make release updates version, commits, tags, pushes
□ make clean removes build artifacts
□ make distclean does full cleanup
□ CI build workflow runs against MVS/CE in Docker
□ CI release workflow creates GitHub Release with assets
```

---

## 8. JCL Templates

All templates use `$VARIABLE` syntax (Python string.Template).
Dynamic sections (SYSLIB) are pre-rendered by jcl.py helpers
and inserted as a single `$SYSLIB_CONCAT` variable.

### 8.1 alloc.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* Allocate dataset: $DSN
//*-----------------------------------------------------------
//ALLOC   EXEC PGM=IEFBR14
//DD1     DD DSN=$DSN,
//           DISP=(NEW,CATLG,DELETE),
//           UNIT=$UNIT,
//           SPACE=($SPACE_UNIT,($SPACE_PRI,$SPACE_SEC,$SPACE_DIR)),
//           DCB=(DSORG=$DSORG,RECFM=$RECFM,LRECL=$LRECL,BLKSIZE=$BLKSIZE)
//
```

Note: For PS datasets (no dirblks), generate a variant without
`$SPACE_DIR`. Handle this in jcl.py by rendering the SPACE
parameter before template substitution.

### 8.2 asm.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* Assemble: $MEMBER
//*-----------------------------------------------------------
//ASM     EXEC PGM=IFOX00,
//          PARM='DECK,NOLOAD,TERM,XREF(SHORT)'
$SYSLIB_CONCAT
//SYSUT1   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSUT2   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSUT3   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSPUNCH DD DSN=$PUNCH_DSN($MEMBER),DISP=SHR
//SYSPRINT DD SYSOUT=*
//SYSGO    DD DUMMY
//SYSIN    DD *
$ASM_SOURCE
/*
//
```

### 8.3 ncallink.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* NCAL Link: $MEMBER
//*-----------------------------------------------------------
//LINK    EXEC PGM=IEWL,PARM='NCAL,LIST,XREF,LET'
//SYSUT1   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSPRINT DD SYSOUT=*
//SYSLMOD  DD DSN=$NCALIB_DSN($MEMBER),DISP=SHR
//SYSLIN   DD DSN=$PUNCH_DSN($MEMBER),DISP=SHR
//
```

### 8.4 link.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* Full Linkedit: $MODULE_NAME
//*-----------------------------------------------------------
//LINK    EXEC PGM=IEWL,PARM='$LINK_OPTIONS'
//SYSUT1   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//SYSPRINT DD SYSOUT=*
//SYSLMOD  DD DSN=$SYSLMOD_DSN($MODULE_NAME),DISP=SHR
$NCALIB_CONCAT
//SYSLIN   DD *
$INCLUDE_STMTS
 ENTRY $ENTRY_POINT
 NAME $MODULE_NAME(R)
/*
//
```

### 8.5 delete.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* Delete dataset: $DSN
//*-----------------------------------------------------------
//DELETE  EXEC PGM=IEFBR14
//DD1     DD DSN=$DSN,
//           DISP=(OLD,DELETE,DELETE)
//
```

### 8.6 copy.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* Copy: $SRC_DSN → $DST_DSN
//*-----------------------------------------------------------
//COPY    EXEC PGM=IEBCOPY
//SYSPRINT DD SYSOUT=*
//SYSUT3   DD UNIT=SYSDA,SPACE=(CYL,(1,1))
//INDD     DD DSN=$SRC_DSN,DISP=SHR
//OUTDD    DD DSN=$DST_DSN,DISP=SHR
//SYSIN    DD *
 COPY INDD=INDD,OUTDD=OUTDD
/*
//
```

### 8.7 receive.jcl.tpl

```jcl
$JOBCARD
//*-----------------------------------------------------------
//* TSO RECEIVE: $XMIT_DSN → $TARGET_DSN
//*-----------------------------------------------------------
//RECV    EXEC PGM=IKJEFT01
//SYSTSPRT DD SYSOUT=*
//SYSTSIN  DD *
 RECEIVE INDSN('$XMIT_DSN')
 DATASET('$TARGET_DSN')
/*
//
```

Note: The RECEIVE template needs validation against MVS/CE.
This is listed as an open point in the spec (Appendix B.4).

---

## 9. Error Handling Conventions

### 9.1 Exit Code Constants

Define in `mbt/__init__.py`:

```python
"""mbt package constants."""

# Exit codes (spec section 11.1)
EXIT_SUCCESS    = 0
EXIT_BUILD      = 1  # assembly/link failure
EXIT_CONFIG     = 2  # config/validation error
EXIT_DEPENDENCY = 3  # resolution/download failure
EXIT_MAINFRAME  = 4  # mvsMF communication error
EXIT_DATASET    = 5  # dataset operation failure
EXIT_INTERNAL   = 99 # unexpected exception
```

### 9.2 Logging Convention

All output uses a prefix:

```python
def log(module: str, msg: str) -> None:
    print(f"[{module}] {msg}")

def log_warn(module: str, msg: str) -> None:
    print(f"[{module}] WARNING: {msg}")

def log_error(module: str, msg: str) -> None:
    print(f"[{module}] ERROR: {msg}", file=sys.stderr)
```

### 9.3 JES Log Capture

On job failure, executors MUST:

```python
def handle_job_failure(result: JobResult, module: str,
                      context: str = ""):
    """Handle a failed JES job.

    Args:
        result: JobResult from mvsMF
        module: Executor name for log prefix
        context: Additional context (e.g. member name)
    """
    log_dir = Path(".mbt/logs")
    log_dir.mkdir(parents=True, exist_ok=True)

    # Log filename: {module}-{context}-{jobid}.log
    # e.g. asm-HTTPD-JOB00456.log, link-HTTPD-JOB00789.log
    parts = [module]
    if context:
        parts.append(context)
    parts.append(result.jobid)
    log_file = log_dir / f"{'-'.join(parts)}.log"
    log_file.write_text(result.spool)

    log_error(module,
        f"{result.jobname} failed (RC={result.rc})")
    log(module, f"Job: {result.jobname} / {result.jobid}")
    log(module, f"Log: {log_file}")

    # Print SYSPRINT section to stderr
    # (extract from spool by looking for "--- SYSPRINT ---")
```

### 9.4 Top-Level Exception Handler

Every CLI script should wrap main() in:

```python
if __name__ == "__main__":
    try:
        sys.exit(main())
    except ProjectError as e:
        log_error("mbt", str(e))
        sys.exit(EXIT_CONFIG)
    except DependencyError as e:
        log_error("mbt", str(e))
        sys.exit(EXIT_DEPENDENCY)
    except MvsMFError as e:
        log_error("mbt", str(e))
        sys.exit(EXIT_MAINFRAME)
    except Exception as e:
        log_error("mbt", f"Internal error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(EXIT_INTERNAL)
```

---

## 10. Testing Strategy

### 10.1 Unit Tests (no MVS required)

```
tests/
├── test_version.py        # VRM conversion, satisfies()
├── test_project.py        # project.toml parsing, validation
├── test_config.py         # config merge, env var priority
├── test_datasets.py       # DSN computation, SYSLIB order
├── test_lockfile.py       # lockfile read/write round-trip
├── test_jcl.py            # template rendering
└── test_output.py         # shell/json formatting
```

Use `unittest` (stdlib). No pytest dependency needed.

Test fixtures: create example project.toml, config.toml, and
lockfile as test data in `tests/fixtures/`.

### 10.2 Integration Tests (requires MVS/CE Docker)

```
tests/integration/
├── test_mvsmf.py          # client against live MVS
├── test_bootstrap.py      # full bootstrap cycle
├── test_build.py          # assembly on MVS
└── test_lifecycle.py      # bootstrap → build → link → clean
```

Require `make run-mvs` first. Set env vars for connection.

### 10.3 Reference Project: examples/hello370

A minimal project in the mbt repo itself that serves as both
smoke test and documentation for new users. Implement this
**before** starting on executors (Milestone 3) — it's the target
you develop and test against.

```
mbt/examples/hello370/
├── project.toml
├── src/
│   └── hello.c
├── asm/
│   └── start.s            # minimal startup stub (optional)
├── Makefile
└── README.md
```

**project.toml:**

```toml
[project]
name    = "hello370"
version = "1.0.0"
type    = "application"

[mvs.build.datasets.punch]
suffix    = "OBJECT"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 5, 2, 5]

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

[dependencies]
"mvslovers/crent370" = ">=1.0.0"

[[link.module]]
name    = "HELLO"
entry   = "HELLO"
options = ["RENT", "REUS"]
include = ["HELLO"]

[artifacts]
mvs = true

[release]
github = "mvslovers/hello370"
version_files = ["project.toml"]
```

**Makefile:**

```makefile
MBT_ROOT := ../../
include $(MBT_ROOT)/mk/core.mk
```

This project exercises the full lifecycle: bootstrap (resolve
crent370), cross-compile, assemble, NCAL link, full linkedit,
package. If hello370 builds, the core pipeline works.

---

## 11. Coding Standards

### 11.1 Python Style

- Python 3.12+ (use `str | None` not `Optional[str]`)
- Type hints on all public functions
- Docstrings on all public classes and functions
- No external dependencies (stdlib only)
- Comments and documentation in English
- `from __future__ import annotations` not needed (3.12+)

### 11.2 Error Messages

Always include context:

```python
# Bad:
raise ProjectError("Invalid type")

# Good:
raise ProjectError(
    f"Invalid project type '{value}'. "
    f"Must be one of: runtime, library, module, application"
)
```

### 11.3 File I/O

- Use `pathlib.Path` consistently
- Use `tomllib` for TOML reading (stdlib since 3.11)
- Write TOML manually (no tomli-w dependency)
- All file operations use UTF-8

### 11.4 HTTP Calls

- Use `urllib.request` (stdlib)
- Set reasonable timeouts (10s for API calls, 120s for job wait)
- Include User-Agent header: `mbt/{version}`
- Handle connection errors with clear messages

---

*End of Implementation Guide*
