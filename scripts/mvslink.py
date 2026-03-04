"""MVS Linkedit executor.

Reads [link.module] from project.toml and submits
IEWL full linkedit JCL for the module.

For projects without [[link.module]] or non-application/module types:
prints info and exits 0.

Log format (per spec section 11.2):
    [mvslink] Linking HELLO...
    [mvslink] HELLO linked (RC=0)
    [mvslink] ERROR: HELLO link failed (RC=8)

Usage:
    mvslink.py [--project project.toml]

Exit codes per spec section 11.1.
"""

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import (
    EXIT_SUCCESS, EXIT_BUILD, EXIT_CONFIG,
    EXIT_MAINFRAME, EXIT_INTERNAL,
)
from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.dependencies import load_package_toml
from mbt.jcl import (
    render_template, render_syslib_concat,
    render_include_concat, jobcard,
)
from mbt.lockfile import Lockfile
from mbt.mvsmf import MvsMFClient, MvsMFError, JobResult
from mbt.project import ProjectError, LINK_TYPES

_MOD = "mvslink"


def _log(msg: str) -> None:
    print(f"[{_MOD}] {msg}")


def _log_warn(msg: str) -> None:
    print(f"[{_MOD}] WARNING: {msg}")


def _log_error(msg: str) -> None:
    print(f"[{_MOD}] ERROR: {msg}", file=sys.stderr)


def _save_job_log(result: JobResult, context: str) -> Path:
    log_dir = Path(".mbt") / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = log_dir / f"{_MOD}-{context}-{result.jobid}.log"
    log_file.write_text(result.spool, encoding="utf-8")
    return log_file


def _make_client(config: MbtConfig) -> MvsMFClient:
    return MvsMFClient(
        host=config.mvs_host,
        port=config.mvs_port,
        user=config.mvs_user,
        password=config.mvs_pass,
    )


def _load_package_cache(lockfile: Lockfile | None) -> dict:
    if not lockfile:
        return {}
    cache = {}
    for dep_key, dep_version in lockfile.dependencies.items():
        owner, repo = dep_key.split("/", 1)
        pkg = load_package_toml(owner, repo, dep_version)
        if pkg:
            cache[dep_key] = pkg
    return cache


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt link — full linkedit of load modules on MVS"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)",
    )
    args = parser.parse_args()

    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project

    # Library and runtime projects don't produce load modules
    if project.type not in LINK_TYPES:
        _log(f"Skipping link (project type: {project.type})")
        return EXIT_SUCCESS

    if not project.link_modules:
        _log("No [link.module] defined, skipping.")
        return EXIT_SUCCESS

    # Load lockfile and package cache
    lockfile_path = Path(".mbt") / "mvs.lock"
    lockfile = Lockfile.load(lockfile_path)
    lockfile_deps = dict(lockfile.dependencies) if lockfile else {}
    package_cache = _load_package_cache(lockfile)

    # Resolve dataset names
    resolver = DatasetResolver(config)
    build_ds_map = resolver.build_datasets()

    syslmod_ds = build_ds_map.get("syslmod")
    if not syslmod_ds:
        _log_error("No 'syslmod' dataset defined in project")
        return EXIT_CONFIG
    syslmod_dsn = syslmod_ds.dsn

    # NCALIB concatenation for IEWL SYSLIB: project NCALIB first, then deps
    ncalib_dsns = resolver.syslib_ncalibs(lockfile_deps, package_cache)
    ncalib_concat = render_syslib_concat(ncalib_dsns)

    # Connect to mvsMF
    client = _make_client(config)
    if not client.ping():
        _log_error(
            f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
        )
        return EXIT_MAINFRAME

    # Link each module
    for mod in project.link_modules:
        _log(f"Linking {mod.name}...")

        link_options = ",".join(mod.options) if mod.options else "LET,LIST,XREF"

        # INCLUDE statements reference the SYSLIB DD (which holds the NCaLIBs)
        include_stmts = render_include_concat(mod.include, "SYSLIB")

        jc = jobcard(
            f"MBTLK{mod.name[:3]}",
            config.jes_jobclass,
            config.jes_msgclass,
            "MBTLINK",
        )
        jcl = render_template("link.jcl.tpl", {
            "JOBCARD": jc,
            "MODULE_NAME": mod.name,
            "LINK_OPTIONS": link_options,
            "SYSLMOD_DSN": syslmod_dsn,
            "NCALIB_CONCAT": ncalib_concat,
            "INCLUDE_STMTS": include_stmts,
            "ENTRY_POINT": mod.entry,
        })

        try:
            result = client.submit_jcl(jcl, wait=True, timeout=180)
        except MvsMFError as e:
            _log_error(f"Failed to submit link job for {mod.name}: {e}")
            return EXIT_BUILD

        # Full link: RC=4 (informational warning) may be acceptable with LET
        if result.rc > 4:
            log_file = _save_job_log(result, mod.name)
            _log_error(f"{mod.name} link failed (RC={result.rc})")
            _log(f"Job: {result.jobname} / {result.jobid}")
            _log(f"Log: {log_file}")
            return EXIT_BUILD

        if result.rc > 0:
            _log_warn(f"{mod.name} linked with warnings (RC={result.rc})")
        else:
            _log(f"{mod.name} linked (RC={result.rc})")

    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ProjectError as e:
        _log_error(str(e))
        sys.exit(EXIT_CONFIG)
    except MvsMFError as e:
        _log_error(str(e))
        sys.exit(EXIT_MAINFRAME)
    except Exception as e:
        _log_error(f"Internal error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(EXIT_INTERNAL)
