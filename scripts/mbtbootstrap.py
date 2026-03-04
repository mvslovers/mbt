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

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import (
    EXIT_SUCCESS, EXIT_CONFIG, EXIT_DEPENDENCY,
    EXIT_MAINFRAME, EXIT_DATASET, EXIT_INTERNAL
)
from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.dependencies import (
    resolve_dependencies, download_dependency,
    extract_headers, load_package_toml, DependencyError
)
from mbt.lockfile import Lockfile
from mbt.mvsmf import MvsMFClient, MvsMFError
from mbt.jcl import render_template, jobcard
from mbt.project import ProjectError
from mbt.version import Version


def _log(msg: str) -> None:
    print(f"[mbt] {msg}")


def _log_warn(msg: str) -> None:
    print(f"[mbt] WARNING: {msg}")


def _log_error(msg: str) -> None:
    print(f"[mbt] ERROR: {msg}", file=sys.stderr)


def _make_client(config: MbtConfig) -> MvsMFClient:
    return MvsMFClient(
        host=config.mvs_host,
        port=config.mvs_port,
        user=config.mvs_user,
        password=config.mvs_pass,
    )


def _save_job_log(result, context: str) -> Path:
    """Save job spool output to .mbt/logs/."""
    log_dir = Path(".mbt/logs")
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = log_dir / f"recv-{context}-{result.jobid}.log"
    log_file.write_text(result.spool, encoding="utf-8")
    return log_file


def _receive_xmit(client: MvsMFClient, config: MbtConfig,
                  xmit_dsn: str, target_dsn: str) -> None:
    """Submit a TSO RECEIVE job to unpack an XMIT file on MVS.

    When deps_volume is configured, uses VOLUME+DATASET parameters
    to place the received dataset on a user/work volume instead of
    the public volume (PUB001).
    """
    jn = f"MBTRECV"
    jc = jobcard(jn, config.jes_jobclass, config.jes_msgclass, "MBT RECV")
    volume = config.deps_volume
    if volume:
        receive_cmd = (
            f" RECEIVE INDSN('{xmit_dsn}') -\n"
            f"  VOLUME('{volume}') DATASET('{target_dsn}')"
        )
    else:
        receive_cmd = f" RECEIVE INDSN('{xmit_dsn}')"
    jcl = render_template("receive.jcl.tpl", {
        "JOBCARD": jc,
        "XMIT_DSN": xmit_dsn,
        "TARGET_DSN": target_dsn,
        "RECEIVE_CMD": receive_cmd,
    })
    result = client.submit_jcl(jcl)
    if not result.success or result.rc > 4:
        # Save spool output for diagnosis
        dsn_short = target_dsn.rsplit(".", 1)[-1]
        log_file = _save_job_log(result, dsn_short)
        raise MvsMFError(
            f"RECEIVE job failed (RC={result.rc}) for {xmit_dsn}\n"
            f"[mbt]        Log saved to {log_file}"
        )


def _alloc_dataset(client: MvsMFClient, config: MbtConfig,
                   ds) -> None:
    """Allocate one build dataset. Warn and skip if already exists."""
    from mbt.datasets import ResolvedDataset
    if client.dataset_exists(ds.dsn):
        _log_warn(f"Dataset already exists, skipping: {ds.dsn}")
        return
    _log(f"Allocating {ds.dsn}...")
    client.create_dataset(
        dsn=ds.dsn,
        dsorg=ds.dsorg,
        recfm=ds.recfm,
        lrecl=ds.lrecl,
        blksize=ds.blksize,
        space=ds.space,
        unit=ds.unit,
        volume=ds.volume,
    )


def _upload_local_dir(client: MvsMFClient, ds,
                      local_dir: str) -> None:
    """Upload all files from local_dir to PDS members on MVS."""
    src = Path(local_dir)
    if not src.exists():
        _log_warn(f"local_dir not found: {local_dir}")
        return
    for f in sorted(src.iterdir()):
        if not f.is_file():
            continue
        member = f.stem.upper()[:8]
        _log(f"Uploading {f.name} → {ds.dsn}({member})...")
        content = f.read_text(encoding="utf-8", errors="replace")
        client.write_member(ds.dsn, member, content)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt bootstrap — resolve dependencies and provision MVS"
    )
    parser.add_argument(
        "--update", action="store_true",
        help="Re-resolve all dependencies (ignore lockfile)"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)"
    )
    parser.add_argument(
        "--no-mvs", action="store_true",
        help="Skip MVS operations (resolve and download only)"
    )
    args = parser.parse_args()

    # Step 1: Load and validate project
    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project
    _log(f"Project: {project.name} v{project.version}")

    # Step 2: Resolve dependencies
    lockfile_path = Path(".mbt") / "mvs.lock"
    lockfile = Lockfile.load(lockfile_path)

    if lockfile is not None and not args.update:
        _log("Using existing lockfile.")
        resolved = dict(lockfile.dependencies)
    else:
        if project.dependencies:
            _log("Resolving dependencies...")
        try:
            resolved = resolve_dependencies(
                project.dependencies,
                lockfile=lockfile,
                update=args.update,
            )
        except DependencyError as e:
            _log_error(str(e))
            return EXIT_DEPENDENCY

        # Write new lockfile
        mbt_version = _read_mbt_version()
        new_lockfile = Lockfile.create(resolved, mbt_version)
        new_lockfile.save(lockfile_path)
        _log(f"Lockfile written: {lockfile_path}")

    if not resolved:
        _log("No dependencies declared.")

    # Load package.toml for resolved deps (from cache if available)
    package_cache: dict = {}

    # Step 3: Download dependency assets
    for dep_key, dep_version in resolved.items():
        owner, repo = dep_key.split("/", 1)
        _log(f"Checking {dep_key}@{dep_version}...")
        try:
            cache_dir = download_dependency(owner, repo, dep_version)
        except DependencyError as e:
            _log_error(str(e))
            return EXIT_DEPENDENCY
        pkg = load_package_toml(owner, repo, dep_version)
        if pkg:
            package_cache[dep_key] = pkg

    # Step 4: Extract headers → contrib/
    for dep_key, dep_version in resolved.items():
        owner, repo = dep_key.split("/", 1)
        cache_dir = download_dependency(owner, repo, dep_version)
        try:
            inc_dir = extract_headers(cache_dir, repo, dep_version)
            _log(f"Headers: {inc_dir}")
        except Exception as e:
            # Not all deps have headers; log warning and continue
            _log_warn(f"No headers for {dep_key}: {e}")

    if args.no_mvs:
        _log("Skipping MVS operations (--no-mvs).")
        return EXIT_SUCCESS

    # Steps 5-8: MVS operations
    client = _make_client(config)
    if not client.ping():
        _log_error(
            f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
        )
        return EXIT_MAINFRAME

    resolver = DatasetResolver(config)
    dep_datasets = resolver.dependency_datasets(resolved, package_cache)

    # Step 5+6: Upload XMIT files and RECEIVE them
    for dep_key, dep_version in resolved.items():
        owner, repo = dep_key.split("/", 1)
        dep_name = repo.upper()
        dep_vrm = Version.parse(dep_version).to_vrm()
        cache_dir = download_dependency(owner, repo, dep_version)

        # Find MVS tarball in cache
        mvs_tarball_name = f"{repo}-{dep_version}-mvs.tar.gz"
        mvs_tarball = cache_dir / mvs_tarball_name
        if not mvs_tarball.exists():
            _log_warn(f"No MVS tarball for {dep_key}, skipping upload.")
            continue

        # Extract XMIT files from tarball and upload each.
        # Naming convention: {repo}-{version}-{ds_key}.xmit
        # e.g. crent370-1.0.0-ncalib.xmit → ds_key=ncalib, suffix=NCALIB
        pkg = package_cache.get(dep_key, {})
        provides = (
            pkg.get("mvs", {}).get("provides", {}).get("datasets", {})
        )
        import tarfile as tf_mod
        try:
            with tf_mod.open(mvs_tarball, "r:gz") as tf:
                for member_info in tf.getmembers():
                    if not member_info.name.endswith(".xmit"):
                        continue
                    xmit_basename = Path(member_info.name).name

                    # Extract ds_key by stripping "{repo}-{version}-" prefix
                    prefix = f"{repo}-{dep_version}-"
                    bare = Path(xmit_basename).stem  # remove .xmit
                    if bare.startswith(prefix):
                        ds_key = bare[len(prefix):].lower()
                    else:
                        ds_key = bare.lower()

                    # Suffix from package.toml, fallback to uppercased ds_key
                    suffix = (
                        provides.get(ds_key, {}).get("suffix")
                        or ds_key.upper()[:8]
                    )

                    # Target DSN
                    deps_hlq = config.deps_hlq
                    if deps_hlq:
                        target_dsn = f"{deps_hlq}.{dep_name}.{dep_vrm}.{suffix}"
                    else:
                        target_dsn = f"{dep_name}.{dep_vrm}.{suffix}"

                    # Skip RECEIVE if target already exists
                    if client.dataset_exists(target_dsn):
                        _log(f"{target_dsn} already exists, skipping RECEIVE.")
                        continue

                    # Shared temp XMIT staging DS: {HLQ}.MBT.XMIT.IN
                    xmit_dsn = f"{config.hlq}.MBT.XMIT.IN"
                    if not client.dataset_exists(xmit_dsn):
                        client.create_dataset(
                            xmit_dsn, "PS", "FB", 80, 3120,
                            ["TRK", 50, 20], "SYSDA"
                        )

                    _log(f"Uploading {xmit_basename} → {xmit_dsn}...")
                    f_obj = tf.extractfile(member_info)
                    if f_obj:
                        client.upload_binary(xmit_dsn, f_obj.read())

                    _log(f"RECEIVE {xmit_dsn} → {target_dsn}...")
                    try:
                        _receive_xmit(client, config, xmit_dsn, target_dsn)
                    except MvsMFError as e:
                        _log_error(f"RECEIVE failed for {target_dsn}:\n{e}")
                        return EXIT_MAINFRAME
                    finally:
                        # Delete temp XMIT staging DS after each RECEIVE
                        try:
                            client.delete_dataset(xmit_dsn)
                        except MvsMFError:
                            pass
        except Exception as e:
            _log_warn(f"Cannot process MVS tarball for {dep_key}: {e}")

    # Step 7: Allocate project build datasets
    _log("Allocating project build datasets...")
    build_ds = resolver.build_datasets()
    for key, ds in build_ds.items():
        try:
            _alloc_dataset(client, config, ds)
        except MvsMFError as e:
            _log_error(f"Dataset allocation failed for {ds.dsn}: {e}")
            return EXIT_DATASET

    # Step 8: Upload local_dir contents
    for key, def_ in project.build_datasets.items():
        if def_.local_dir and key in build_ds:
            ds = build_ds[key]
            _log(f"Uploading local_dir {def_.local_dir} → {ds.dsn}...")
            try:
                _upload_local_dir(client, ds, def_.local_dir)
            except MvsMFError as e:
                _log_error(f"Upload failed for {ds.dsn}: {e}")
                return EXIT_DATASET

    _log("Bootstrap complete.")
    return EXIT_SUCCESS


def _read_mbt_version() -> str:
    """Read VERSION file from mbt root."""
    mbt_root = Path(__file__).parent.parent
    version_file = mbt_root / "VERSION"
    if version_file.exists():
        return version_file.read_text(encoding="utf-8").strip()
    return "0.0.0"


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ProjectError as e:
        _log_error(str(e))
        sys.exit(EXIT_CONFIG)
    except DependencyError as e:
        _log_error(str(e))
        sys.exit(EXIT_DEPENDENCY)
    except MvsMFError as e:
        _log_error(str(e))
        sys.exit(EXIT_MAINFRAME)
    except Exception as e:
        _log_error(f"Internal error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(EXIT_INTERNAL)
