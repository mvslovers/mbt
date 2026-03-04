"""mbt package executor — create release artifacts.

Creates release artifacts in dist/:
1. package.toml (auto-generated manifest)
2. {name}-{version}-headers.tar.gz (if artifacts.headers = true)
3. {name}-{version}-mvs.tar.gz (if artifacts.mvs = true)
4. {name}-{version}-bundle.tar.gz (if artifacts.package_bundle = true)

The MVS tarball contains XMIT files downloaded from the mainframe
via TSO TRANSMIT (IKJEFT01). Each build dataset that should be
shipped is transmitted to a sequential dataset, downloaded as
binary, and included in the tarball.

Usage:
    mvspackage.py
    mvspackage.py --project other.toml

Exit codes per spec section 11.1
"""

import io
import sys
import tarfile
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import (
    EXIT_SUCCESS, EXIT_CONFIG,
    EXIT_MAINFRAME, EXIT_INTERNAL
)
from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.lockfile import Lockfile
from mbt.mvsmf import MvsMFClient, MvsMFError
from mbt.jcl import jobcard
from mbt.project import ProjectError


MODULE = "mvspackage"


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


def _read_mbt_version() -> str:
    """Read VERSION file from mbt root."""
    mbt_root = Path(__file__).parent.parent
    version_file = mbt_root / "VERSION"
    if version_file.exists():
        return version_file.read_text(encoding="utf-8").strip()
    return "0.0.0"


def _generate_package_toml(config: MbtConfig,
                           lockfile: Lockfile | None,
                           dist_dir: Path) -> Path:
    """Generate package.toml manifest for this release.

    Includes project metadata, resolved dependency versions,
    artifact filenames, and provided dataset definitions.
    """
    project = config.project
    name = project.name
    version = project.version
    mbt_version = _read_mbt_version()

    lines = [
        "[package]",
        f'name    = "{name}"',
        f'version = "{version}"',
        f'type    = "{project.type}"',
        f'mbt     = "{mbt_version}"',
        "",
    ]

    # Resolved dependency versions
    lines.append("[package.dependencies]")
    if lockfile and lockfile.dependencies:
        for dep, ver in sorted(lockfile.dependencies.items()):
            lines.append(f'"{dep}" = "{ver}"')
    lines.append("")

    # Artifact filenames
    lines.append("[artifacts]")
    if project.artifact_headers:
        lines.append(f'headers = "{name}-{version}-headers.tar.gz"')
    if project.artifact_mvs:
        lines.append(f'mvs     = "{name}-{version}-mvs.tar.gz"')
    if project.artifact_bundle:
        lines.append(f'bundle  = "{name}-{version}-bundle.tar.gz"')
    lines.append("")

    # Provided datasets (from build datasets)
    for key, ds in project.build_datasets.items():
        lines.append(f"[mvs.provides.datasets.{key}]")
        lines.append(f'suffix    = "{ds.suffix}"')
        lines.append(f'dsorg     = "{ds.dsorg}"')
        lines.append(f'recfm     = "{ds.recfm}"')
        lines.append(f"lrecl     = {ds.lrecl}")
        lines.append(f"blksize   = {ds.blksize}")
        space_str = ", ".join(
            f'"{s}"' if isinstance(s, str) else str(s)
            for s in ds.space
        )
        lines.append(f"space     = [{space_str}]")
        lines.append("")

    out = dist_dir / "package.toml"
    out.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return out


def _create_headers_tarball(config: MbtConfig,
                            dist_dir: Path) -> Path | None:
    """Create {name}-{version}-headers.tar.gz from include/ directory.

    The tarball structure is: {name}-{version}/include/...
    """
    project = config.project
    if not project.artifact_headers:
        return None

    include_dir = Path("include")
    if not include_dir.is_dir():
        _log("No include/ directory found, skipping headers tarball.")
        return None

    name = project.name
    version = project.version
    tarball_name = f"{name}-{version}-headers.tar.gz"
    tarball_path = dist_dir / tarball_name
    prefix = f"{name}-{version}"

    with tarfile.open(tarball_path, "w:gz") as tf:
        for f in sorted(include_dir.rglob("*")):
            if f.is_file():
                arcname = f"{prefix}/{f}"
                tf.add(str(f), arcname=arcname)

    _log(f"Created {tarball_path}")
    return tarball_path


def _transmit_dataset(client: MvsMFClient, config: MbtConfig,
                      src_dsn: str) -> bytes | None:
    """Transmit a dataset to XMIT format and download the binary.

    Uses TSO TRANSMIT via IKJEFT01 to create an XMIT file from
    the source dataset, then downloads it as binary.

    Returns the XMIT binary data, or None on failure.
    """
    xmit_dsn = f"{config.hlq}.MBT.XMIT.OUT"

    # Delete temp XMIT dataset if it exists from a previous run
    if client.dataset_exists(xmit_dsn):
        try:
            client.delete_dataset(xmit_dsn)
        except MvsMFError:
            pass

    # Allocate temp sequential dataset for XMIT output
    try:
        client.create_dataset(
            xmit_dsn, "PS", "FB", 80, 3120,
            ["TRK", 100, 50], "SYSDA"
        )
    except MvsMFError as e:
        _log_error(f"Cannot allocate XMIT staging dataset {xmit_dsn}: {e}")
        return None

    # Submit TRANSMIT job
    user = config.mvs_user
    jn = "MBTXMIT"
    jc = jobcard(jn, config.jes_jobclass, config.jes_msgclass, "MBT XMIT")
    jcl = (
        f"{jc}\n"
        f"//XMIT    EXEC PGM=IKJEFT01\n"
        f"//SYSTSPRT DD SYSOUT=*\n"
        f"//SYSTSIN  DD *\n"
        f" TRANSMIT {user}.DUMMY +\n"
        f"   DSNAME('{src_dsn}') +\n"
        f"   OUTDSN('{xmit_dsn}') +\n"
        f"   NOLOG NONOTIFY\n"
        f"/*\n"
        f"//\n"
    )

    try:
        result = client.submit_jcl(jcl)
    except MvsMFError as e:
        _log_error(f"TRANSMIT job failed for {src_dsn}: {e}")
        _cleanup_xmit(client, xmit_dsn)
        return None

    if not result.success and result.rc > 4:
        _log_error(f"TRANSMIT failed for {src_dsn} (RC={result.rc})")
        _cleanup_xmit(client, xmit_dsn)
        return None

    # Download the XMIT binary
    try:
        data = client._request(
            "GET", f"/restfiles/ds/{xmit_dsn}",
            accept="application/octet-stream",
            extra_headers={"X-IBM-Data-Type": "binary"}
        )
    except MvsMFError as e:
        _log_error(f"Cannot download XMIT for {src_dsn}: {e}")
        data = None

    _cleanup_xmit(client, xmit_dsn)
    return data


def _cleanup_xmit(client: MvsMFClient, xmit_dsn: str) -> None:
    """Delete temp XMIT staging dataset."""
    try:
        client.delete_dataset(xmit_dsn)
    except MvsMFError:
        pass


def _create_mvs_tarball(config: MbtConfig, client: MvsMFClient,
                        resolver: DatasetResolver,
                        dist_dir: Path) -> Path | None:
    """Create {name}-{version}-mvs.tar.gz with XMIT files.

    Downloads each build dataset from MVS as XMIT and packages them.
    Tarball structure: {name}-{version}/mvs/{key}.xmit
    """
    project = config.project
    if not project.artifact_mvs:
        return None

    name = project.name
    version = project.version
    tarball_name = f"{name}-{version}-mvs.tar.gz"
    tarball_path = dist_dir / tarball_name
    prefix = f"{name}-{version}"

    build_ds = resolver.build_datasets()
    xmit_files: list[tuple[str, bytes]] = []

    for key, ds in build_ds.items():
        _log(f"Transmitting {ds.dsn} -> XMIT...")
        data = _transmit_dataset(client, config, ds.dsn)
        if data:
            xmit_name = f"{name}-{version}-{key}.xmit"
            xmit_files.append((xmit_name, data))
        else:
            _log(f"Skipping {ds.dsn} (TRANSMIT failed or empty).")

    if not xmit_files:
        _log("No XMIT files produced, skipping MVS tarball.")
        return None

    with tarfile.open(tarball_path, "w:gz") as tf:
        for xmit_name, data in xmit_files:
            arcname = f"{prefix}/mvs/{xmit_name}"
            info = tarfile.TarInfo(name=arcname)
            info.size = len(data)
            tf.addfile(info, io.BytesIO(data))

    _log(f"Created {tarball_path}")
    return tarball_path


def _create_bundle_tarball(config: MbtConfig, client: MvsMFClient,
                           resolver: DatasetResolver,
                           dist_dir: Path) -> Path | None:
    """Create {name}-{version}-bundle.tar.gz (applications only).

    Bundle structure:
        {name}-{version}/
            mvs/           (XMIT files)
            jobs/           (install JCL)
            content/        (static files)
    """
    project = config.project
    if not project.artifact_bundle:
        return None

    name = project.name
    version = project.version
    tarball_name = f"{name}-{version}-bundle.tar.gz"
    tarball_path = dist_dir / tarball_name
    prefix = f"{name}-{version}"

    # Re-use MVS tarball XMIT data if already in dist
    mvs_tarball = dist_dir / f"{name}-{version}-mvs.tar.gz"

    with tarfile.open(tarball_path, "w:gz") as tf:
        # Include XMIT files from MVS tarball if available
        if mvs_tarball.exists():
            with tarfile.open(mvs_tarball, "r:gz") as mvs_tf:
                for member in mvs_tf.getmembers():
                    if member.isfile():
                        f_obj = mvs_tf.extractfile(member)
                        if f_obj:
                            tf.addfile(member, f_obj)
        else:
            # Generate XMIT files directly
            build_ds = resolver.build_datasets()
            for key, ds in build_ds.items():
                data = _transmit_dataset(client, config, ds.dsn)
                if data:
                    xmit_name = f"{name}-{version}-{key}.xmit"
                    arcname = f"{prefix}/mvs/{xmit_name}"
                    info = tarfile.TarInfo(name=arcname)
                    info.size = len(data)
                    tf.addfile(info, io.BytesIO(data))

        # Include content/ directory if present
        content_dir = Path("content")
        if content_dir.is_dir():
            for f in sorted(content_dir.rglob("*")):
                if f.is_file():
                    arcname = f"{prefix}/content/{f.relative_to(content_dir)}"
                    tf.add(str(f), arcname=arcname)

        # Include jobs/ directory if present
        jobs_dir = Path("jobs")
        if jobs_dir.is_dir():
            for f in sorted(jobs_dir.rglob("*")):
                if f.is_file():
                    arcname = f"{prefix}/jobs/{f.relative_to(jobs_dir)}"
                    tf.add(str(f), arcname=arcname)

    _log(f"Created {tarball_path}")
    return tarball_path


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt package — create release artifacts"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)"
    )
    args = parser.parse_args()

    # Load config
    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project
    _log(f"Packaging {project.name} v{project.version}...")

    # Ensure dist/ directory
    dist_dir = Path("dist")
    dist_dir.mkdir(parents=True, exist_ok=True)

    # Load lockfile for dependency info
    lockfile = Lockfile.load()

    # 1. Generate package.toml
    pkg_path = _generate_package_toml(config, lockfile, dist_dir)
    _log(f"Generated {pkg_path}")

    # 2. Headers tarball (no MVS needed)
    _create_headers_tarball(config, dist_dir)

    # 3+4. MVS tarball and bundle (need MVS connection)
    needs_mvs = project.artifact_mvs or project.artifact_bundle
    client = None
    resolver = None

    if needs_mvs:
        client = _make_client(config)
        if not client.ping():
            _log_error(
                f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
            )
            return EXIT_MAINFRAME
        resolver = DatasetResolver(config)
        _create_mvs_tarball(config, client, resolver, dist_dir)
        _create_bundle_tarball(config, client, resolver, dist_dir)

    _log("Packaging complete.")
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
