"""mbt package executor — create release artifacts.

Creates release artifacts in dist/:
1. package.toml (auto-generated manifest)
2. {name}-{version}-lib-headers.tar.gz  (artifacts.headers = true)
3. {name}-{version}-lib-modules.tar.gz  (artifacts.modules = true)
4. {name}-{version}-load.tar.gz         (artifacts.loads  = true)
5. {name}-{version}-bundle.tar.gz       (artifacts.package_bundle = true)

The modules and load tarballs contain XMIT files downloaded from the
mainframe via TSO TRANSMIT (IKJEFT01).  For selective module export
(module_members != ["*"]) IEBCOPY is used to copy selected NCALIB
members to a temporary PDS before transmission.

package.toml lists headers and modules artifacts.  The load artifact
is an end-user install artifact and is never listed in package.toml.

Usage:
    mvspackage.py
    mvspackage.py --project other.toml

Exit codes per spec section 11.1
"""

import io
import shutil
import subprocess
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


def _save_job_log(result, context: str) -> Path:
    """Write spool output to .mbt/logs/."""
    log_dir = Path(".mbt") / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = log_dir / f"{MODULE}-{context}-{result.jobid}.log"
    log_file.write_text(result.spool, encoding="utf-8")
    return log_file


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
                           dist_dir: Path,
                           client: "MvsMFClient | None" = None) -> Path:
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
    # application: omit — consumers get headers only, not transitive deps
    if project.type != "application":
        lines.append("[package.dependencies]")
        if lockfile and lockfile.dependencies:
            for dep, ver in sorted(lockfile.dependencies.items()):
                lines.append(f'"{dep}" = "{ver}"')
        lines.append("")

    # Artifact filenames
    # loads (SYSLMOD) is an install artifact — never listed as a dep artifact
    lines.append("[artifacts]")
    if project.artifact_headers:
        lines.append(f'headers = "{name}-{version}-lib-headers.tar.gz"')
    if project.artifact_modules:
        lines.append(f'modules = "{name}-{version}-lib-modules.tar.gz"')
    lines.append("")

    # Provided datasets — only when modules artifact is present
    if project.artifact_modules:
        ncalib_ds = project.build_datasets.get("ncalib")
        if ncalib_ds:
            lines.append("[mvs.provides.datasets.ncalib]")
            lines.append(f'suffix    = "{ncalib_ds.suffix}"')
            lines.append(f'dsorg     = "{ncalib_ds.dsorg}"')
            lines.append(f'recfm     = "{ncalib_ds.recfm}"')
            lines.append(f"lrecl     = {ncalib_ds.lrecl}")
            lines.append(f"blksize   = {ncalib_ds.blksize}")
            space_str = ", ".join(
                f'"{s}"' if isinstance(s, str) else str(s)
                for s in ncalib_ds.space
            )
            lines.append(f"space     = [{space_str}]")
            lines.append("")

    # Link section: autocall flag + exports list for non-autocall libs
    if not project.link_autocall:
        lines.append("[link]")
        lines.append("autocall = false")
        exports = _enumerate_ncalib_members(config, client)
        if exports is not None:
            members_str = ", ".join(f'"{m}"' for m in exports)
            lines.append(f"exports  = [{members_str}]")
        lines.append("")

    out = dist_dir / "package.toml"
    out.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return out


def _create_headers_tarball(config: MbtConfig,
                            dist_dir: Path) -> Path | None:
    """Create {name}-{version}-lib-headers.tar.gz from include/ directory.

    The tarball structure is: {name}-{version}/include/...

    If [artifacts] header_files is set, only those files are included.
    Otherwise all files under include/ are included.
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
    tarball_name = f"{name}-{version}-lib-headers.tar.gz"
    tarball_path = dist_dir / tarball_name
    prefix = f"{name}-{version}"

    # Determine which files to include
    header_filter = set(project.artifact_header_files)

    with tarfile.open(tarball_path, "w:gz") as tf:
        for f in sorted(include_dir.rglob("*")):
            if not f.is_file():
                continue
            if header_filter and f.name not in header_filter:
                continue
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

    # Submit TRANSMIT job (TRANSMIT allocates OUTDSN itself)
    user = config.mvs_user
    jn = "MBTXMIT"
    jc = jobcard(jn, config.jes_jobclass, config.jes_msgclass, "MBT XMIT")
    jcl = (
        f"{jc}\n"
        f"//XMIT    EXEC PGM=IKJEFT01\n"
        f"//SYSTSPRT DD SYSOUT=*\n"
        f"//SYSTSIN  DD *\n"
        f" TRANSMIT {user}.DUMMY -\n"
        f"   DSNAME('{src_dsn}') -\n"
        f"   OUTDSN('{xmit_dsn}')\n"
        f"/*\n"
        f"//\n"
    )

    try:
        result = client.submit_jcl(jcl)
    except MvsMFError as e:
        _log_error(f"TRANSMIT job failed for {src_dsn}: {e}")
        _cleanup_xmit(client, xmit_dsn)
        return None

    if not result.success or result.rc > 4:
        log_file = _save_job_log(result, f"xmit-{src_dsn.split('.')[-1]}")
        _log_error(f"TRANSMIT failed for {src_dsn} (RC={result.rc})")
        _log(f"Job: {result.jobname} / {result.jobid}")
        _log(f"Log: {log_file}")
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


def _enumerate_ncalib_members(config: MbtConfig,
                              client: "MvsMFClient | None") -> list[str] | None:
    """List members of the project NCALIB on MVS.

    Returns sorted list of member names, or None if unavailable.
    """
    from mbt.datasets import DatasetResolver
    if client is None:
        _log_error(
            "autocall = false requires MVS connection to enumerate NCALIB members. "
            "Ensure MVS credentials are configured."
        )
        return None
    resolver = DatasetResolver(config)
    build_ds = resolver.build_datasets()
    ncalib_ds = build_ds.get("ncalib")
    if not ncalib_ds:
        _log_error("No 'ncalib' dataset defined — cannot enumerate exports.")
        return None
    try:
        members = client.list_members(ncalib_ds.dsn)
        return sorted(members)
    except MvsMFError as e:
        _log_error(f"Cannot list members of {ncalib_ds.dsn}: {e}")
        return None


def _cleanup_xmit(client: MvsMFClient, xmit_dsn: str) -> None:
    """Delete temp XMIT staging dataset."""
    try:
        client.delete_dataset(xmit_dsn)
    except MvsMFError:
        pass


def _iebcopy_select_members(client: MvsMFClient, config: MbtConfig,
                            src_dsn: str,
                            members: list[str]) -> bytes | None:
    """Copy selected members from src_dsn into a temp PDS, then TRANSMIT.

    Used when artifact_module_members is a non-wildcard list.
    Returns the XMIT binary data, or None on failure.
    """
    tmp_dsn  = f"{config.hlq}.MBT.CLIB.OUT"
    xmit_dsn = f"{config.hlq}.MBT.XMIT.OUT"

    for dsn in (tmp_dsn, xmit_dsn):
        if client.dataset_exists(dsn):
            try:
                client.delete_dataset(dsn)
            except MvsMFError:
                pass

    # Build IEBCOPY SELECT statement — each member as (NAME,,R)
    select_items = ",".join(f"({m},,R)" for m in members)
    select_stmt  = f"    SELECT MEMBER=({select_items})"

    user = config.mvs_user
    jn   = "MBTCLIB"
    jc   = jobcard(jn, config.jes_jobclass, config.jes_msgclass,
                   "MBT CLIENT LIB")
    jcl = (
        f"{jc}\n"
        f"//*/\n"
        f"//* Step 1: copy selected NCALIB members to temp PDS\n"
        f"//*/\n"
        f"//COPY     EXEC PGM=IEBCOPY\n"
        f"//SYSPRINT DD   SYSOUT=*\n"
        f"//SYSUT3   DD   UNIT=SYSDA,SPACE=(CYL,(1,1))\n"
        f"//SYSUT4   DD   UNIT=SYSDA,SPACE=(CYL,(1,1))\n"
        f"//IN       DD   DSN='{src_dsn}',DISP=SHR\n"
        f"//OUT      DD   DSN='{tmp_dsn}',DISP=(NEW,CATLG,DELETE),\n"
        f"//              UNIT=SYSDA,SPACE=(TRK,(5,5,10)),\n"
        f"//              DCB=(DSORG=PO,RECFM=U,BLKSIZE=19069)\n"
        f"//SYSIN    DD   *\n"
        f"    COPY OUTDD=OUT,INDD=IN\n"
        f"{select_stmt}\n"
        f"/*\n"
        f"//*/\n"
        f"//* Step 2: transmit temp PDS to XMIT file\n"
        f"//*/\n"
        f"//XMIT     EXEC PGM=IKJEFT01,COND=(4,LT,COPY)\n"
        f"//SYSTSPRT DD   SYSOUT=*\n"
        f"//SYSTSIN  DD   *\n"
        f" TRANSMIT {user}.DUMMY -\n"
        f"   DSNAME('{tmp_dsn}') -\n"
        f"   OUTDSN('{xmit_dsn}')\n"
        f"/*\n"
        f"//\n"
    )

    try:
        result = client.submit_jcl(jcl)
    except MvsMFError as e:
        _log_error(f"IEBCOPY/TRANSMIT job failed: {e}")
        for dsn in (tmp_dsn, xmit_dsn):
            _cleanup_xmit(client, dsn)
        return None

    if not result.success or result.rc > 4:
        log_file = _save_job_log(result, "clib")
        _log_error(f"IEBCOPY/TRANSMIT failed (RC={result.rc})")
        _log(f"Job: {result.jobname} / {result.jobid}")
        _log(f"Log: {log_file}")
        for dsn in (tmp_dsn, xmit_dsn):
            _cleanup_xmit(client, dsn)
        return None

    # Download the XMIT binary
    try:
        data = client._request(
            "GET", f"/restfiles/ds/{xmit_dsn}",
            accept="application/octet-stream",
            extra_headers={"X-IBM-Data-Type": "binary"}
        )
    except MvsMFError as e:
        _log_error(f"Cannot download XMIT for client lib: {e}")
        data = None

    for dsn in (tmp_dsn, xmit_dsn):
        _cleanup_xmit(client, dsn)
    return data


def _create_modules_tarball(config: MbtConfig, client: MvsMFClient,
                            resolver: DatasetResolver,
                            dist_dir: Path) -> Path | None:
    """Create {name}-{version}-lib-modules.tar.gz with NCALIB XMIT.

    For libraries (module_members == ["*"]): transmit whole NCALIB.
    For applications (explicit module_members): IEBCOPY SELECT + TRANSMIT.
    Tarball structure: {name}-{version}/mvs/{name}-{version}-ncalib.xmit
    """
    project = config.project
    if not project.artifact_modules:
        return None

    build_ds  = resolver.build_datasets()
    ncalib_ds = build_ds.get("ncalib")
    if not ncalib_ds:
        _log("No 'ncalib' dataset defined, skipping modules tarball.")
        return None

    name    = project.name
    version = project.version

    members = project.artifact_module_members
    if members == ["*"]:
        _log(f"Transmitting {ncalib_ds.dsn} -> XMIT (whole NCALIB)...")
        data = _transmit_dataset(client, config, ncalib_ds.dsn)
    else:
        _log(f"Transmitting {ncalib_ds.dsn} members {members} -> XMIT "
             f"(IEBCOPY SELECT)...")
        data = _iebcopy_select_members(client, config, ncalib_ds.dsn, members)

    if not data:
        _log("Skipping modules tarball (TRANSMIT failed or empty).")
        return None

    tarball_name = f"{name}-{version}-lib-modules.tar.gz"
    tarball_path = dist_dir / tarball_name
    prefix       = f"{name}-{version}"
    xmit_name    = f"{name}-{version}-ncalib.xmit"

    with tarfile.open(tarball_path, "w:gz") as tf:
        arcname = f"{prefix}/mvs/{xmit_name}"
        info = tarfile.TarInfo(name=arcname)
        info.size = len(data)
        tf.addfile(info, io.BytesIO(data))

    _log(f"Created {tarball_path}")
    return tarball_path


def _create_loads_tarball(config: MbtConfig, client: MvsMFClient,
                          resolver: DatasetResolver,
                          dist_dir: Path) -> Path | None:
    """Create {name}-{version}-load.tar.gz with SYSLMOD XMIT.

    This artifact is uploaded to GitHub but NOT listed in package.toml —
    it is an end-user install artifact, not a build dependency.
    Tarball structure: {name}-{version}/mvs/{name}-{version}-syslmod.xmit
    """
    project = config.project
    if not project.artifact_loads:
        return None

    build_ds    = resolver.build_datasets()
    syslmod_ds  = build_ds.get("syslmod")
    if not syslmod_ds:
        _log("No 'syslmod' dataset defined, skipping loads tarball.")
        return None

    name    = project.name
    version = project.version

    _log(f"Transmitting {syslmod_ds.dsn} -> XMIT...")
    data = _transmit_dataset(client, config, syslmod_ds.dsn)
    if not data:
        _log("Skipping loads tarball (TRANSMIT failed or empty).")
        return None

    tarball_name = f"{name}-{version}-load.tar.gz"
    tarball_path = dist_dir / tarball_name
    prefix       = f"{name}-{version}"
    xmit_name    = f"{name}-{version}-syslmod.xmit"

    with tarfile.open(tarball_path, "w:gz") as tf:
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


def _git_owner_repo() -> tuple[str, str] | None:
    """Extract owner/repo from git remote 'origin'.

    Parses both SSH (git@github.com:owner/repo.git) and
    HTTPS (https://github.com/owner/repo.git) URLs.
    Returns (owner, repo) or None if not determinable.
    """
    try:
        result = subprocess.run(
            ["git", "remote", "get-url", "origin"],
            capture_output=True, text=True, timeout=5
        )
        if result.returncode != 0:
            return None
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return None

    url = result.stdout.strip()

    # SSH: git@github.com:owner/repo.git
    if ":" in url and "@" in url:
        path = url.split(":", 1)[1]
    # HTTPS: https://github.com/owner/repo.git
    elif "/" in url:
        parts = url.rstrip("/").split("/")
        if len(parts) >= 2:
            path = "/".join(parts[-2:])
        else:
            return None
    else:
        return None

    path = path.removesuffix(".git")
    parts = path.split("/")
    if len(parts) >= 2:
        return parts[-2], parts[-1]
    return None


def _install_to_cache(dist_dir: Path, version: str) -> None:
    """Copy dist/ artifacts into ~/.mbt/cache for local consumption.

    This allows downstream projects to resolve this dependency
    locally without a GitHub Release.
    """
    from mbt.dependencies import CACHE_DIR

    identity = _git_owner_repo()
    if identity is None:
        _log("WARNING: Cannot determine owner/repo from git remote — "
             "skipping local cache install.")
        return

    owner, repo = identity
    cache_dir = CACHE_DIR / owner / repo / version

    if cache_dir.exists():
        shutil.rmtree(cache_dir)
    cache_dir.mkdir(parents=True, exist_ok=True)

    for f in dist_dir.iterdir():
        if f.is_file():
            shutil.copy2(f, cache_dir / f.name)

    _log(f"Installed to local cache: {cache_dir}")


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

    # Connect to MVS if needed
    needs_mvs = (project.artifact_modules or project.artifact_loads
                 or project.artifact_bundle)
    needs_exports = not project.link_autocall
    client = None
    resolver = None

    if needs_mvs or needs_exports:
        client = _make_client(config)
        if not client.ping():
            _log_error(
                f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
            )
            return EXIT_MAINFRAME

    # 1. Generate package.toml
    #    - module: never (standalone program, never a build dep)
    #    - library / application: always (headers and/or modules listed)
    if project.type == "module":
        _log("Skipping package.toml (module type is never a build dependency)")
    else:
        pkg_path = _generate_package_toml(config, lockfile, dist_dir, client)
        _log(f"Generated {pkg_path}")

    # 2. Headers tarball (no MVS needed)
    _create_headers_tarball(config, dist_dir)

    # 3. Modules tarball (NCALIB — build dependency artifact)
    # 4. Loads tarball (SYSLMOD — install artifact, not in package.toml)
    # 5. Bundle
    if needs_mvs:
        resolver = DatasetResolver(config)
        _create_modules_tarball(config, client, resolver, dist_dir)
        _create_loads_tarball(config, client, resolver, dist_dir)
        _create_bundle_tarball(config, client, resolver, dist_dir)

    # Install artifacts to local cache for downstream projects
    _install_to_cache(dist_dir, project.version)

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
