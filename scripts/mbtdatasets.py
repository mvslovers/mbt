"""mbt datasets — mainframe dataset listing and management.

Shows all project-related datasets and their status on MVS.
Queries the mvsMF REST API to check existence.

Output example:
    Build datasets:
      IBMUSER.HTTPD.V3R3M1.OBJECT         (exists)
      IBMUSER.HTTPD.V3R3M1.NCALIB         (exists)
      IBMUSER.HTTPD.V3R3M1.LOAD           (missing)

    Dependency datasets:
      IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB (exists)
      IBMUSER.DEPS.CRENT370.V1R0M0.NCALIB (exists)

    Install datasets:
      IBMUSER.HTTPD.NCALIB                 (exists)
      IBMUSER.HTTPD.LOAD                   (missing)

Flags:
    --delete-build   Delete all build datasets
    --delete-deps    Delete all dependency datasets
    --check          Exit non-zero if expected datasets are missing
    --quiet          Suppress normal output (for use from Make)

Usage:
    mbtdatasets.py
    mbtdatasets.py --delete-build
    mbtdatasets.py --check

Exit codes per spec section 11.1
"""

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import (
    EXIT_SUCCESS, EXIT_CONFIG, EXIT_DATASET,
    EXIT_MAINFRAME, EXIT_INTERNAL
)
from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.lockfile import Lockfile
from mbt.mvsmf import MvsMFClient, MvsMFError
from mbt.project import ProjectError
from mbt.dependencies import load_package_toml


MODULE = "mbtdatasets"


def _log(msg: str) -> None:
    print(f"[{MODULE}] {msg}")


def _log_error(msg: str) -> None:
    print(f"[{MODULE}] ERROR: {msg}", file=sys.stderr)


def _make_client(config: MbtConfig) -> MvsMFClient:
    return MvsMFClient(
        host=config.mvs_host,
        port=config.mvs_port,
        user=config.mvs_user,
        password=config.mvs_pass,
    )


def _check_status(client: MvsMFClient, dsn: str) -> str:
    """Return 'exists' or 'missing' for a dataset."""
    try:
        return "exists" if client.dataset_exists(dsn) else "missing"
    except MvsMFError:
        return "unknown"


def _list_datasets(config: MbtConfig, client: MvsMFClient,
                   resolver: DatasetResolver,
                   lockfile: Lockfile | None,
                   pkg_cache: dict[str, dict],
                   quiet: bool) -> int:
    """List all project datasets with their status.

    Returns count of missing datasets.
    """
    missing_count = 0

    # Build datasets
    build_ds = resolver.build_datasets()
    if build_ds and not quiet:
        print("\nBuild datasets:")
    for key, ds in build_ds.items():
        status = _check_status(client, ds.dsn)
        if status == "missing":
            missing_count += 1
        if not quiet:
            print(f"  {ds.dsn:<44} ({status})")

    # Dependency datasets
    resolved = lockfile.dependencies if lockfile else {}
    dep_ds = resolver.dependency_datasets(resolved, pkg_cache)
    has_dep_ds = any(ds_list for ds_list in dep_ds.values())
    if has_dep_ds and not quiet:
        print("\nDependency datasets:")
    for dep_key, ds_list in dep_ds.items():
        for ds in ds_list:
            status = _check_status(client, ds.dsn)
            if status == "missing":
                missing_count += 1
            if not quiet:
                print(f"  {ds.dsn:<44} ({status})")

    # Install datasets
    install_ds = resolver.install_datasets()
    if install_ds and not quiet:
        print("\nInstall datasets:")
    for key, ds in install_ds.items():
        status = _check_status(client, ds.dsn)
        if status == "missing":
            missing_count += 1
        if not quiet:
            print(f"  {ds.dsn:<44} ({status})")

    if not quiet:
        print()

    return missing_count


def _delete_build_datasets(config: MbtConfig, client: MvsMFClient,
                           resolver: DatasetResolver,
                           quiet: bool) -> int:
    """Delete all build datasets. Returns exit code."""
    build_ds = resolver.build_datasets()
    for key, ds in build_ds.items():
        if client.dataset_exists(ds.dsn):
            if not quiet:
                _log(f"Deleting {ds.dsn}...")
            try:
                client.delete_dataset(ds.dsn)
            except MvsMFError as e:
                _log_error(f"Failed to delete {ds.dsn}: {e}")
                return EXIT_DATASET
        else:
            if not quiet:
                _log(f"{ds.dsn} does not exist, skipping.")
    return EXIT_SUCCESS


def _delete_dep_datasets(config: MbtConfig, client: MvsMFClient,
                         resolver: DatasetResolver,
                         lockfile: Lockfile | None,
                         pkg_cache: dict[str, dict],
                         quiet: bool) -> int:
    """Delete all dependency datasets. Returns exit code."""
    resolved = lockfile.dependencies if lockfile else {}
    dep_ds = resolver.dependency_datasets(resolved, pkg_cache)
    for dep_key, ds_list in dep_ds.items():
        for ds in ds_list:
            if client.dataset_exists(ds.dsn):
                if not quiet:
                    _log(f"Deleting {ds.dsn}...")
                try:
                    client.delete_dataset(ds.dsn)
                except MvsMFError as e:
                    _log_error(f"Failed to delete {ds.dsn}: {e}")
                    return EXIT_DATASET
            else:
                if not quiet:
                    _log(f"{ds.dsn} does not exist, skipping.")
    return EXIT_SUCCESS


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt datasets — mainframe dataset management"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)"
    )
    parser.add_argument(
        "--delete-build", action="store_true",
        help="Delete all build datasets"
    )
    parser.add_argument(
        "--delete-deps", action="store_true",
        help="Delete all dependency datasets"
    )
    parser.add_argument(
        "--check", action="store_true",
        help="Exit non-zero if expected datasets are missing"
    )
    parser.add_argument(
        "--quiet", action="store_true",
        help="Suppress normal output"
    )
    args = parser.parse_args()

    # Load config
    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    # Connect to MVS
    client = _make_client(config)
    if not client.ping():
        _log_error(
            f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
        )
        return EXIT_MAINFRAME

    resolver = DatasetResolver(config)

    # Load lockfile and package cache for dependency dataset resolution
    lockfile = Lockfile.load()
    resolved = lockfile.dependencies if lockfile else {}
    pkg_cache: dict[str, dict] = {}
    for dep_key, dep_version in resolved.items():
        owner, repo = dep_key.split("/", 1)
        pkg = load_package_toml(owner, repo, dep_version)
        if pkg:
            pkg_cache[dep_key] = pkg

    # Handle delete operations
    if args.delete_build:
        rc = _delete_build_datasets(config, client, resolver, args.quiet)
        if rc != EXIT_SUCCESS:
            return rc
        if not args.delete_deps and not args.check:
            return EXIT_SUCCESS

    if args.delete_deps:
        rc = _delete_dep_datasets(
            config, client, resolver, lockfile, pkg_cache, args.quiet
        )
        if rc != EXIT_SUCCESS:
            return rc
        if not args.check:
            return EXIT_SUCCESS

    # List datasets (default action)
    missing = _list_datasets(
        config, client, resolver, lockfile, pkg_cache, args.quiet
    )

    if args.check and missing > 0:
        if not args.quiet:
            _log_error(f"{missing} dataset(s) missing.")
        return EXIT_DATASET

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
