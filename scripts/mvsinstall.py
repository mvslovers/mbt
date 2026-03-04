"""MVS Install executor.

Copies build datasets to install datasets using IEBCOPY (replace mode).
Install datasets are created if they don't exist.

For projects without [mvs.install]: exits 0 silently.

Log format (per spec section 11.2):
    [mvsinstall] Installing IBMUSER.HELLO370.V1R0M0.NCALIB -> IBMUSER.HELLO370.NCALIB...
    [mvsinstall] ncalib installed (RC=0)
    [mvsinstall] ERROR: ncalib install failed (RC=8)

Usage:
    mvsinstall.py [--project project.toml]

Exit codes per spec section 11.1.
"""

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import (
    EXIT_SUCCESS, EXIT_BUILD, EXIT_CONFIG,
    EXIT_DATASET, EXIT_MAINFRAME, EXIT_INTERNAL,
)
from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.jcl import render_template, jobcard
from mbt.mvsmf import MvsMFClient, MvsMFError, JobResult
from mbt.project import ProjectError

_MOD = "mvsinstall"


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


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt install — copy build datasets to install datasets"
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

    if not project.install_naming:
        _log("Skipping install (not configured)")
        return EXIT_SUCCESS

    if not project.install_datasets:
        _log("No install datasets configured, skipping.")
        return EXIT_SUCCESS

    # Resolve dataset names
    resolver = DatasetResolver(config)
    build_ds_map = resolver.build_datasets()
    install_ds_map = resolver.install_datasets()

    # Connect to mvsMF
    client = _make_client(config)
    if not client.ping():
        _log_error(
            f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
        )
        return EXIT_MAINFRAME

    # Install each dataset
    for key, inst_ds in install_ds_map.items():
        build_ds = build_ds_map.get(key)
        if not build_ds:
            _log_warn(f"No build dataset for install key '{key}', skipping")
            continue

        src_dsn = build_ds.dsn
        dst_dsn = inst_ds.dsn
        _log(f"Installing {src_dsn} -> {dst_dsn}...")

        # Allocate destination dataset if missing
        if not client.dataset_exists(dst_dsn):
            _log(f"Allocating install dataset {dst_dsn}...")
            try:
                client.create_dataset(
                    dsn=dst_dsn,
                    dsorg=inst_ds.dsorg,
                    recfm=inst_ds.recfm,
                    lrecl=inst_ds.lrecl,
                    blksize=inst_ds.blksize,
                    space=inst_ds.space,
                    unit=inst_ds.unit,
                    volume=inst_ds.volume,
                )
            except MvsMFError as e:
                _log_error(f"Failed to allocate {dst_dsn}: {e}")
                return EXIT_DATASET

        # Submit IEBCOPY replace job
        jc = jobcard(
            f"MBTCP{key[:4].upper()}",
            config.jes_jobclass,
            config.jes_msgclass,
            "MBTCOPY",
        )
        jcl = render_template("copy.jcl.tpl", {
            "JOBCARD": jc,
            "SRC_DSN": src_dsn,
            "DST_DSN": dst_dsn,
        })

        try:
            result = client.submit_jcl(jcl, wait=True, timeout=120)
        except MvsMFError as e:
            _log_error(f"Failed to submit copy job for {key}: {e}")
            return EXIT_BUILD

        if result.rc > 0:
            log_file = _save_job_log(result, key)
            _log_error(f"{key} install failed (RC={result.rc})")
            _log(f"Job: {result.jobname} / {result.jobid}")
            _log(f"Log: {log_file}")
            return EXIT_BUILD

        _log(f"{key} installed (RC={result.rc})")

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
