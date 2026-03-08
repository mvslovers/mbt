"""mbt config CLI.

Usage:
    mbtconfig.py --output shell    # for Make $(eval ...)
    mbtconfig.py --output json     # for debugging
    mbtconfig.py --get <key>       # single value
    mbtconfig.py --validate        # check project.toml
    mbtconfig.py --doctor          # show sources
"""

import sys
import os
import argparse
from pathlib import Path

# Add scripts/ dir to path so 'mbt' package is importable
sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG
from mbt.config import MbtConfig, _ENV_MAP
from mbt.datasets import DatasetResolver
from mbt.lockfile import Lockfile
from mbt.output import format_shell, format_json, format_doctor
from mbt.version import to_vrm


def _mbt_version() -> str:
    """Read mbt VERSION file relative to MBT_ROOT."""
    mbt_root = Path(os.environ.get("MBT_ROOT", Path(__file__).parent.parent))
    version_file = mbt_root / "VERSION"
    if version_file.exists():
        return version_file.read_text(encoding="utf-8").strip()
    return "unknown"


def build_variables(config: MbtConfig) -> dict[str, str]:
    """Build the complete variable dict for shell/json output.

    Computes ALL values that Make and executor scripts need:

    PROJECT_NAME, PROJECT_VERSION, PROJECT_TYPE, PROJECT_VRM,
    MBT_VERSION (read from mbt/VERSION file),
    CC, CFLAGS,
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
    project = config.project
    resolver = DatasetResolver(config)
    lockfile = Lockfile.load()
    lockfile_deps = lockfile.dependencies if lockfile else {}
    package_cache: dict = {}  # populated by mbtbootstrap (Milestone 2)

    variables: dict[str, str] = {}

    # Project metadata
    variables["PROJECT_NAME"] = project.name
    variables["PROJECT_VERSION"] = project.version
    variables["PROJECT_TYPE"] = project.type
    variables["PROJECT_VRM"] = project.vrm
    variables["MBT_VERSION"] = _mbt_version()

    # Compiler
    variables["CC"] = "c2asm370"
    cflags_parts = ["-S", "-O1", f'-DVERSION="{project.version}"'] + project.cflags
    variables["CFLAGS"] = " ".join(cflags_parts)

    # MVS connection
    variables["MVS_HOST"] = config.mvs_host
    variables["MVS_PORT"] = str(config.mvs_port)
    variables["MVS_USER"] = config.mvs_user
    variables["MVS_HLQ"] = config.hlq
    variables["DEPS_HLQ"] = config.deps_hlq

    # JES
    variables["JES_JOBCLASS"] = config.jes_jobclass
    variables["JES_MSGCLASS"] = config.jes_msgclass

    # Build datasets: BUILD_DS_<KEY>=<DSN>
    build_ds = resolver.build_datasets()
    for key, ds in build_ds.items():
        variables[f"BUILD_DS_{key.upper()}"] = ds.dsn

    # Dependency info from lockfile
    for dep_key, dep_version in lockfile_deps.items():
        dep_name = dep_key.split("/")[-1].upper()
        variables[f"DEP_{dep_name}_VERSION"] = dep_version
        variables[f"DEP_{dep_name}_VRM"] = to_vrm(dep_version)
        variables[f"DEP_{dep_name}_HEADERS"] = (
            f"contrib/{dep_key.split('/')[-1]}-{dep_version}/include"
        )

    # Dependency dataset DSNs from package_cache
    dep_datasets = resolver.dependency_datasets(lockfile_deps, package_cache)
    for dep_key, ds_list in dep_datasets.items():
        dep_name = dep_key.split("/")[-1].upper()
        for ds in ds_list:
            variables[f"DEP_{dep_name}_{ds.suffix}"] = ds.dsn

    # SYSLIB lists (space-separated)
    maclibs = resolver.syslib_maclibs(lockfile_deps, package_cache)
    ncalibs = resolver.syslib_ncalibs(lockfile_deps, package_cache)
    variables["SYSLIB_MACLIBS"] = " ".join(maclibs)
    variables["SYSLIB_NCALIBS"] = " ".join(ncalibs)

    # Compiler include flags (-I per dep headers dir)
    includes = ["-Iinclude"]
    for dep_key, dep_version in lockfile_deps.items():
        dep_short = dep_key.split("/")[-1]
        includes.append(f"-Icontrib/{dep_short}-{dep_version}/include")
    variables["INCLUDES"] = " ".join(includes)

    # Source directories
    variables["C_DIRS"] = " ".join(project.c_dirs)
    variables["ASM_DIRS"] = " ".join(project.asm_dirs)

    return variables


def main() -> int:
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
        return EXIT_CONFIG

    if args.validate:
        print("[mbt] project.toml is valid")
        return EXIT_SUCCESS

    if args.doctor:
        sourced = {
            env_name.replace("MBT_", ""): config.get_sourced(config_key)
            for config_key, env_name in _ENV_MAP.items()
        }
        print(format_doctor(sourced))
        return EXIT_SUCCESS

    if args.get:
        try:
            print(config.get(args.get))
            return EXIT_SUCCESS
        except KeyError as e:
            print(f"[mbt] ERROR: {e}", file=sys.stderr)
            return EXIT_CONFIG

    variables = build_variables(config)
    if args.output == "shell":
        print(format_shell(variables))
    elif args.output == "json":
        print(format_json(variables))
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
