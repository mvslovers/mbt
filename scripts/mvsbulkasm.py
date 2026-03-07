"""MVS Bulk Assembler executor.

Builds all modules in batches using multi-step JCL. Unlike mvsasm.py
(which inlines source in JCL), this executor uploads .s files to a
SOURCE PDS first, then references them via SYSIN DD DSN=.

Pipeline:
  1. Cross-compile all .c → .s (same as mvsasm)
  2. Upload all .s to SOURCE PDS via mvsMF REST API
  3. Generate multi-step JCL (bulk_batch_size modules per job)
  4. Submit batch jobs, wait for completion
  5. Parse per-step RCs, report failures

Requires [mvs.build.datasets.source] in project.toml.

Usage:
    mvsbulkasm.py [--project project.toml]

Exit codes per spec section 11.1.
"""

import sys
import re
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import (
    EXIT_SUCCESS, EXIT_BUILD, EXIT_CONFIG,
    EXIT_MAINFRAME, EXIT_DATASET, EXIT_INTERNAL,
)
from mbt.config import MbtConfig
from mbt.datasets import DatasetResolver
from mbt.dependencies import load_package_toml
from mbt.jcl import render_template, render_syslib_concat, jobcard
from mbt.lockfile import Lockfile
from mbt.mvsmf import MvsMFClient, MvsMFError, JobResult
from mbt.project import ProjectError

_MOD = "mvsbulkasm"


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
                       package_cache: dict) -> bool:
    """Cross-compile all .c files in c_dirs to .s using c2asm370."""
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


def _find_sources(project) -> list[tuple[Path, str]]:
    """Find all .s source files in c_dirs and asm_dirs."""
    sources = []

    for d in project.c_dirs:
        src_dir = Path(d)
        if not src_dir.is_dir():
            continue
        for f in sorted(src_dir.iterdir()):
            if f.suffix.lower() == ".s" and f.is_file():
                member = f.stem.upper()[:8]
                sources.append((f, member))

    for d in project.asm_dirs:
        src_dir = Path(d)
        if not src_dir.is_dir():
            continue
        for f in sorted(src_dir.iterdir()):
            if f.suffix.lower() in (".s", ".asm") and f.is_file():
                member = f.stem.upper()[:8]
                sources.append((f, member))

    return sources


def _upload_sources(client: MvsMFClient,
                    source_dsn: str,
                    sources: list[tuple[Path, str]]) -> bool:
    """Upload all .s files to SOURCE PDS as members."""
    _log(f"Uploading {len(sources)} source files to {source_dsn}...")
    for src, member in sources:
        content = src.read_text(encoding="utf-8", errors="replace")
        try:
            client.write_member(source_dsn, member, content)
        except MvsMFError as e:
            _log_error(f"Failed to upload {member} to {source_dsn}: {e}")
            return False
    _log(f"Upload complete.")
    return True


def _build_batch_jcl(batch: list[str],
                     batch_num: int,
                     config: MbtConfig,
                     maclibs: list[str],
                     build_ds: dict[str, str]) -> str:
    """Generate multi-step JCL for a batch of members."""
    source_dsn = build_ds["source"]
    punch_dsn = build_ds["punch"]
    ncalib_dsn = build_ds["ncalib"]
    syslib_concat = render_syslib_concat(maclibs)

    jc = jobcard(
        f"MBTBLK{batch_num:02d}",
        config.jes_jobclass,
        config.jes_msgclass,
        "MBTBULK",
    )

    lines = [jc]
    for idx, member in enumerate(batch, start=1):
        seq = f"{idx:02d}"
        step_jcl = render_template("bulk-asm-step.jcl.tpl", {
            "SEQ": seq,
            "MEMBER": member,
            "SYSLIB_CONCAT": syslib_concat,
            "SOURCE_DSN": source_dsn,
            "PUNCH_DSN": punch_dsn,
            "NCALIB_DSN": ncalib_dsn,
        })
        lines.append(step_jcl)

    lines.append("//")
    return "\n".join(lines)


def _parse_batch_results(spool: str,
                         batch: list[str],
                         max_rc: int) -> list[tuple[str, int, bool]]:
    """Parse per-step results from spool output.

    Returns list of (member, rc, ok) tuples.

    Parses IEF142I messages which have the format:
      IEF142I jobname stepname - STEP WAS EXECUTED - COND CODE 0000

    Also detects NOT EXECUTED steps (IEF272I) and ABENDs.
    """
    results = []

    for idx, member in enumerate(batch, start=1):
        seq = f"{idx:02d}"
        asm_step = f"ASM{seq}"

        executed = re.search(
            rf'IEF142I\s+\S+\s+{asm_step}\s+.*COND CODE\s+(\d+)',
            spool
        )
        if executed:
            rc = int(executed.group(1))
        elif re.search(rf'IEF272I\s+\S+\s+{asm_step}\s', spool):
            rc = 9997
        elif re.search(rf'{asm_step}\s.*ABEND', spool):
            rc = 9999
        elif asm_step in spool:
            rc = 9999
        else:
            rc = -1

        ok = 0 <= rc <= max_rc
        results.append((member, rc, ok))

    return results


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt bulk-build — batch assemble on MVS"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)",
    )
    args = parser.parse_args()

    t_start = time.monotonic()

    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project

    lockfile_path = Path(".mbt") / "mvs.lock"
    lockfile = Lockfile.load(lockfile_path)
    lockfile_deps = dict(lockfile.dependencies) if lockfile else {}
    package_cache = _load_package_cache(lockfile)

    resolver = DatasetResolver(config)
    build_ds_map = resolver.build_datasets()
    build_ds = {k: v.dsn for k, v in build_ds_map.items()}

    if "source" not in build_ds:
        _log_error(
            "Bulk build requires [mvs.build.datasets.source] "
            "in project.toml"
        )
        return EXIT_CONFIG

    if "punch" not in build_ds or "ncalib" not in build_ds:
        _log_error("Missing 'punch' or 'ncalib' dataset in project.toml")
        return EXIT_CONFIG

    maclibs = resolver.syslib_maclibs(lockfile_deps, package_cache)

    _log(f"Project: {project.name} v{project.version}")
    _log(f"Batch size: {project.bulk_batch_size}")

    if not _compile_c_sources(project, lockfile_deps, package_cache):
        return EXIT_BUILD

    sources = _find_sources(project)
    if not sources:
        _log_warn("No .s source files found in c_dirs or asm_dirs")
        return EXIT_SUCCESS

    _log(f"Found {len(sources)} source files")

    client = _make_client(config)
    if not client.ping():
        _log_error(
            f"Cannot reach mvsMF at {config.mvs_host}:{config.mvs_port}"
        )
        return EXIT_MAINFRAME

    source_dsn = build_ds["source"]
    if not _upload_sources(client, source_dsn, sources):
        return EXIT_DATASET

    members = [member for _, member in sources]
    batch_size = project.bulk_batch_size
    batches = [members[i:i + batch_size]
               for i in range(0, len(members), batch_size)]

    _log(f"Submitting {len(batches)} batch job(s) "
         f"({len(members)} modules)...")

    max_rc = project.max_rc
    failed = []
    total_ok = 0

    for batch_num, batch in enumerate(batches, start=1):
        _log(f"Batch {batch_num}/{len(batches)} "
             f"({len(batch)} modules: {batch[0]}..{batch[-1]})...")

        jcl = _build_batch_jcl(
            batch, batch_num, config, maclibs, build_ds
        )

        timeout = max(180, len(batch) * 10)
        try:
            result = client.submit_jcl(jcl, wait=True, timeout=timeout)
        except MvsMFError as e:
            _log_error(f"Failed to submit batch {batch_num}: {e}")
            return EXIT_MAINFRAME

        _log(f"Batch {batch_num} completed: "
             f"{result.jobname} / {result.jobid}")

        log_file = _save_job_log(result, f"batch{batch_num:02d}")

        if result.abended or result.status == "JCL ERROR":
            _log_error(
                f"Batch {batch_num} failed: "
                f"{result.status} (RC={result.rc})"
            )
            _log(f"Log: {log_file}")
            for member in batch:
                failed.append(member)
            continue

        step_results = _parse_batch_results(
            result.spool, batch, max_rc
        )
        for member, rc, ok in step_results:
            if ok:
                total_ok += 1
                if rc > 0:
                    _log_warn(f"{member} RC={rc}")
                else:
                    _log(f"{member} RC={rc}")
            else:
                _log_error(f"{member} failed (RC={rc}, max_rc={max_rc})")
                failed.append(member)

    elapsed = time.monotonic() - t_start
    _log(f"Results: {total_ok} OK, {len(failed)} failed "
         f"out of {len(members)} modules in {_fmt_elapsed(elapsed)}")

    if failed:
        _log_error(f"Failed modules: {', '.join(failed)}")
        return EXIT_BUILD

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
