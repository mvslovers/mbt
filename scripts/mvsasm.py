"""MVS Assembler executor.

For each .s file found in c_dirs (cross-compiled) and asm_dirs (hand-written):
  1. Submit single job with ASM (IFOX00) + NCAL link (IEWL) via asm.jcl.tpl
  2. Wait for result, check RC against max_rc from project.toml
  3. Write failure log to .mbt/logs/ on error

Log format (per spec section 11.2):
    [mvsasm] Assembling HELLO...
    [mvsasm] HELLO assembled (RC=0)
    [mvsasm] ERROR: HELLO failed (RC=8, max_rc=4)

Usage:
    mvsasm.py [--project project.toml] [--member NAME]

Exit codes per spec section 11.1.
"""

import sys
import time
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
from mbt.jcl import render_template, render_syslib_concat, jobcard
from mbt.lockfile import Lockfile
from mbt.mvsmf import MvsMFClient, MvsMFError, JobResult
from mbt.project import ProjectError

_MOD = "mvsasm"


def _log(msg: str) -> None:
    print(f"[{_MOD}] {msg}")


def _log_warn(msg: str) -> None:
    print(f"[{_MOD}] WARNING: {msg}")


def _log_error(msg: str) -> None:
    print(f"[{_MOD}] ERROR: {msg}", file=sys.stderr)


def _fmt_elapsed(seconds: float) -> str:
    """Format elapsed seconds as human-readable string."""
    m, s = divmod(int(seconds), 60)
    if m > 0:
        return f"{m}m {s}s"
    return f"{s}s"


def _save_job_log(result: JobResult, context: str) -> Path:
    """Write spool output to .mbt/logs/."""
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
    """Load package.toml for all lockfile deps from cache."""
    if not lockfile:
        return {}
    cache = {}
    for dep_key, dep_version in lockfile.dependencies.items():
        owner, repo = dep_key.split("/", 1)
        pkg = load_package_toml(owner, repo, dep_version)
        if pkg:
            cache[dep_key] = pkg
    return cache


def _compile_c_sources(project, lockfile_deps: dict,
                       package_cache: dict,
                       member_filter: str | None) -> bool:
    """Cross-compile .c files in c_dirs to .s using c2asm370.

    Core flags: -S -O1
    Extended by project cflags from [build] in project.toml.
    Include paths: ./include + contrib/{pkg_name}-{ver}/include per dep.

    Returns True on success, False on any compile error.
    """
    import subprocess

    include_flags = []
    if Path("include").is_dir():
        include_flags.append("-I./include")
    for dep_key, dep_version in lockfile_deps.items():
        repo = dep_key.split("/")[-1]
        pkg = package_cache.get(dep_key, {})
        pkg_name = pkg.get("package", {}).get("name") or repo
        inc = Path("contrib") / f"{pkg_name}-{dep_version}" / "include"
        if inc.is_dir():
            include_flags.append(f"-I{inc}")

    for d in project.c_dirs:
        src_dir = Path(d)
        if not src_dir.is_dir():
            continue
        for f in sorted(src_dir.glob("*.c")):
            member = f.stem.upper()[:8]
            if member_filter and member != member_filter.upper():
                continue
            out_s = f.with_suffix(".s")
            cmd = (["c2asm370", "-S", "-O1"]
                   + project.cflags
                   + include_flags
                   + ["-o", str(out_s), str(f)])
            _log(f"Cross-compiling {f.name}...")
            try:
                result = subprocess.run(
                    cmd, capture_output=True, text=True
                )
                if result.returncode != 0:
                    _log_error(
                        f"c2asm370 failed for {f.name}:\n"
                        + (result.stderr or result.stdout).rstrip()
                    )
                    return False
            except FileNotFoundError:
                _log_error("c2asm370 not found in PATH")
                return False
    return True


def _find_sources(project, member_filter: str | None) -> list[tuple[Path, str]]:
    """Find .s source files in c_dirs and asm_dirs.

    Returns list of (path, member_name) tuples.
    c_dirs: .s files (cross-compiled from C or pre-generated)
    asm_dirs: .s/.asm files (hand-written assembler)
    """
    sources = []

    for d in project.c_dirs:
        src_dir = Path(d)
        if not src_dir.is_dir():
            continue
        for f in sorted(src_dir.iterdir()):
            if f.suffix.lower() == ".s" and f.is_file():
                member = f.stem.upper()[:8]
                if member_filter and member != member_filter.upper():
                    continue
                sources.append((f, member))

    for d in project.asm_dirs:
        src_dir = Path(d)
        if not src_dir.is_dir():
            continue
        for f in sorted(src_dir.iterdir()):
            if f.suffix.lower() in (".s", ".asm") and f.is_file():
                member = f.stem.upper()[:8]
                if member_filter and member != member_filter.upper():
                    continue
                sources.append((f, member))

    return sources


def _assemble_one(client: MvsMFClient, config: MbtConfig,
                  maclibs: list[str],
                  src: Path, member: str,
                  build_ds: dict[str, str]) -> bool:
    """Assemble and NCAL-link one source file in a single job.

    Submits one JCL job with two steps: ASM (IFOX00) + LINK (IEWL NCAL).
    The LINK step is conditioned on ASM success (COND=(4,LT,ASM)).

    Returns True on success.
    """
    _log(f"Assembling {member}...")

    punch_dsn = build_ds.get("punch")
    ncalib_dsn = build_ds.get("ncalib")
    if not punch_dsn:
        _log_error(
            f"No 'punch' dataset defined in project (needed for {member})"
        )
        return False

    asm_source = src.read_text(encoding="utf-8", errors="replace")
    syslib_concat = render_syslib_concat(maclibs)

    jc = jobcard(
        f"MBTASM{member[:3]}",
        config.jes_jobclass,
        config.jes_msgclass,
        "MBTASM",
    )
    tpl_vars = {
        "JOBCARD": jc,
        "MEMBER": member,
        "SYSLIB_CONCAT": syslib_concat,
        "PUNCH_DSN": punch_dsn,
        "ASM_SOURCE": asm_source,
    }
    if ncalib_dsn:
        tpl_vars["NCALIB_DSN"] = ncalib_dsn
    jcl = render_template("asm.jcl.tpl", tpl_vars)

    try:
        result = client.submit_jcl(jcl, wait=True, timeout=180,
                                   collect_spool=False)
    except MvsMFError as e:
        _log_error(f"Failed to submit job for {member}: {e}")
        return False

    max_rc = config.project.max_rc
    if result.rc > max_rc:
        spool = client.collect_spool(result.jobname, result.jobid)
        result = JobResult(
            jobid=result.jobid, jobname=result.jobname,
            rc=result.rc, status=result.status, spool=spool)
        log_file = _save_job_log(result, member)
        _log_error(f"{member} failed (RC={result.rc}, max_rc={max_rc})")
        _log(f"Job: {result.jobname} / {result.jobid}")
        _log(f"Log: {log_file}")
        return False

    if result.rc > 0:
        _log_warn(f"{member} assembled + linked (RC={result.rc})")
    else:
        _log(f"{member} assembled + linked (RC={result.rc})")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt assemble — upload and assemble sources on MVS"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)",
    )
    parser.add_argument(
        "--member", default=None,
        help="Assemble only this member name (default: all)",
    )
    args = parser.parse_args()

    t_start = time.monotonic()

    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project

    # Load lockfile and package cache for SYSLIB building
    lockfile_path = Path(".mbt") / "mvs.lock"
    lockfile = Lockfile.load(lockfile_path)
    lockfile_deps = dict(lockfile.dependencies) if lockfile else {}
    package_cache = _load_package_cache(lockfile)

    # Resolve build dataset names
    resolver = DatasetResolver(config)
    build_ds_map = resolver.build_datasets()
    build_ds = {k: v.dsn for k, v in build_ds_map.items()}

    # Build MACLIB concatenation: project → deps → system
    maclibs = resolver.syslib_maclibs(lockfile_deps, package_cache)

    # Compile C sources → .s (c2asm370, runs on host)
    if not _compile_c_sources(project, lockfile_deps, package_cache, args.member):
        return EXIT_BUILD

    # Find .s/.asm source files (includes freshly compiled ones)
    sources = _find_sources(project, args.member)
    if not sources:
        if args.member:
            _log_warn(f"Member '{args.member}' not found in c_dirs/asm_dirs, skipping")
            return EXIT_SUCCESS
        _log_warn("No .s source files found in c_dirs or asm_dirs")
        return EXIT_SUCCESS

    # Connect to mvsMF
    client = _make_client(config)
    if not client.ping():
        _log_error(
            f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
        )
        return EXIT_MAINFRAME

    # Assemble and NCAL-link each source (single job per module)
    built = 0
    for src, member in sources:
        if not _assemble_one(client, config, maclibs, src, member, build_ds):
            elapsed = time.monotonic() - t_start
            _log(f"Build failed after {_fmt_elapsed(elapsed)} "
                 f"({built}/{len(sources)} modules)")
            return EXIT_BUILD
        built += 1

    elapsed = time.monotonic() - t_start
    _log(f"Build complete: {built} modules in {_fmt_elapsed(elapsed)}")
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
