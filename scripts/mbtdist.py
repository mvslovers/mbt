"""mbt v2 distribution builder -- assemble the SMP4 installation package.

Reads the `[distribution]` table of project.toml and writes, into dist/:

    <product>-<version>-<lib>.xmit   one per [[distribution.library]]
    <product>-<version>-alloc.jcl    allocate the SMP datasets   (run once)
    <product>-<version>-inst.jcl     RECEIVE / APPLY / ACCEPT    (repeatable)
    <product>-<version>-smp.zip      README + every XMIT + both jobs
    <product>-<version>-smp.tar.gz   the same, for people without unzip

The load library XMIT is built by `make package` (ld370 --pack) before this
runs; this script picks it up and adds the source libraries alongside it.

Two transports, on purpose.  The load modules go through SMP, which is what
inventory, PTFs and RESTORE are for.  Sample material -- procedures, PARMLIB
patterns, jobs -- travels as its own XMIT built by xmit370 and is restored by
TSO RECEIVE, so its content answers to nothing but itself.

Everything here is offline: no MVS is touched, which is what lets it run in
the release CI.

Usage:
    python3 mbtdist.py [--project project.toml] [--distdir dist]
"""

import argparse
import os
import shutil
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from mbt import EXIT_CONFIG, EXIT_SUCCESS
from mbt import distribution as D
from mbt.buildstamp import _git
from mbt.jcl import render_template
from mbt.version import to_vrm

try:
    import tomllib
except ModuleNotFoundError:  # pragma: no cover -- Python < 3.11
    import tomli as tomllib


def _log(msg: str) -> None:
    print(f"[mbt] {msg}")


def _fail(msg: str) -> None:
    print(f"[mbt] ERROR: {msg}", file=sys.stderr)
    sys.exit(EXIT_CONFIG)


def _stats_date() -> str | None:
    """The commit date, so a release artifact is byte-reproducible.

    Without it xmit370 takes the ISPF statistics from each file's mtime, and
    two builds of the same commit produce different XMITs.  The commit date is
    both stable and honest -- unlike a fixed constant, it still says something
    true about the members.  Outside a git checkout there is nothing to use
    and the statistics fall back to mtime.
    """
    return _git("log", "-1", "--format=%cd", "--date=format:%Y-%m-%d")


def _stage_library(lib: D.Library, subst: dict[str, str],
                   stage_root: Path) -> Path:
    """Copy a library directory, substituting placeholders, ready to pack.

    The repo keeps `@LINKLIB@` in a procedure's STEPLIB rather than a real
    dataset name, because the name depends on the version being built.  The
    substitution therefore cannot happen in place -- it happens here, into a
    staging copy xmit370 then packs.

    Sub-directories and dot-files are skipped, which is also what xmit370
    does, so the two never disagree about what a library contains.
    """
    src = Path(lib.dir)
    if not src.is_dir():
        raise D.DistributionError(
            f"[[distribution.library]] dir = '{lib.dir}' is not a directory"
        )
    dest = stage_root / lib.target_dd.lower()
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)

    count = 0
    for path in sorted(src.iterdir()):
        if not path.is_file() or path.name.startswith("."):
            continue
        D.member_name(path)           # fail early on an unusable member name
        text = path.read_text(encoding="utf-8")
        for placeholder, value in subst.items():
            text = text.replace(placeholder, value)
        (dest / path.name).write_text(text, encoding="utf-8")
        count += 1
    if not count:
        raise D.DistributionError(
            f"[[distribution.library]] dir = '{lib.dir}' contains no members"
        )
    return dest


def _pack_library(staged: Path, target_dsn: str, out_file: Path,
                  stats_date: str | None) -> None:
    """Pack a staged directory into an XMIT with xmit370.

    xmit370 does its own validation and refuses anything that would not
    survive the trip -- a line longer than the LRECL, a tab, a byte with no
    EBCDIC mapping -- naming file, line and column.  That is the right place
    for those checks: they belong to the transport, not to the SYSMOD.
    """
    cmd = ["xmit370", "create", "-o", str(out_file), "--dsn", target_dsn]
    if stats_date:
        cmd += ["--stats-date", stats_date]
    cmd.append(str(staged))
    try:
        r = subprocess.run(cmd, capture_output=True, text=True)
    except FileNotFoundError:
        raise D.DistributionError(
            "xmit370 not found on PATH. It is part of the cc370 toolchain "
            "(make -C cc370 install); the release CI already installs it."
        )
    if r.returncode != 0:
        raise D.DistributionError(
            f"xmit370 failed for {staged} (rc={r.returncode}):\n"
            f"{r.stderr.strip() or r.stdout.strip()}"
        )


def build(project_file: str, distdir: str, builddir: str) -> int:
    with open(project_file, "rb") as f:
        cfg = tomllib.load(f)

    project = cfg.get("project", {})
    name = project.get("name", "unknown")
    version = project.get("version", "0.0.0")
    vrm = to_vrm(version)

    dist = D.parse(cfg, vrm)
    if dist is None:
        _log("No [distribution] section in project.toml -- nothing to build")
        return EXIT_SUCCESS

    modules = [m["name"] for m in cfg.get("module", [])]
    if not modules:
        raise D.DistributionError(
            "[distribution] needs at least one [[module]] to ship"
        )

    prefix = f"{name}-{version}"
    alloc_job = f"{prefix}-alloc.jcl"
    inst_job = f"{prefix}-inst.jcl"
    load_xmit = f"{prefix}-load.xmit"

    out = Path(distdir)
    out.mkdir(parents=True, exist_ok=True)

    if not (out / load_xmit).is_file():
        # package builds it immediately before this step; without it the
        # archive would ship an install job with nothing to install.
        raise D.DistributionError(
            f"{out / load_xmit} not found -- the load library XMIT is built "
            f"by 'make package' and must exist before the package is assembled"
        )

    # ---- the shipped source libraries ----
    # A member may use these rather than hard-code a dataset name that depends
    # on the version being built -- a procedure's STEPLIB above all.
    subst = {
        "@LINKLIB@": dist.smp.target,
        "@VRM@": vrm,
        "@VERSION@": version,
        "@FMID@": dist.smp.fmid,
    }
    for lib in dist.libraries:
        subst[f"@{lib.target_dd}@"] = lib.target

    stats_date = _stats_date()
    stage_root = Path(builddir) / "dist-stage"
    xmit_files = {dist.smp.lklib: load_xmit}
    for lib in dist.libraries:
        fname = f"{prefix}-{lib.target_dd.lower()}.xmit"
        staged = _stage_library(lib, subst, stage_root)
        _pack_library(staged, lib.target, out / fname, stats_date)
        xmit_files[lib.target] = fname
        _log(f"Packaged {fname} ({lib.dir} -> {lib.target})")
    shutil.rmtree(stage_root, ignore_errors=True)

    # ---- the SYSMOD and the two jobs ----
    mcs = D.assemble_mcs(dist, modules, name, version)
    plan = D.receive_plan(dist, xmit_files)

    alloc_jcl = render_template("smpalloc.jcl.tpl", {
        "JOBCARD": D.jobcard(f"{name}ALC", f"{name.upper()[:12]} ALLOC"),
        "PRODUCT": name,
        "VERSION": version,
        "FMID": dist.smp.fmid,
        "INSTALL_JOB": inst_job,
        "ALLOC_DDS": D.render_alloc_dds(dist),
    })

    receive_summary = ["//*   DELOLD    make the RECEIVE targets absent"]
    for step, dsn, _ph, fname in plan:
        receive_summary.append(f"//*   {step:<9} {fname}")
        receive_summary.append(f"//*               -> {dsn}")
    cleanup_note = ("//*   CLEANUP   scratch the staging library, now spent")

    if dist.smp.accept_fmid:
        accept_summary = ("//*   ACCEPT    make this level the base a RESTORE "
                          "returns to")
        accept_step = (
            "//*\n"
            "//* ---- ACCEPT -------------------------------------------------------\n"
            "//* Accepting the FMID once fills the distribution library, so a\n"
            "//* later RESTORE has a previous level to go back to. Without it\n"
            "//* a RESTORE would DELETE the module instead of reverting it.\n"
            "//* Service (PTFs) is deliberately never accepted.\n"
            "//*\n"
            "//ACCEPT  EXEC SMPAPP,COND=(0,NE,APPLY.HMASMP)\n"
            f"{D.render_apply_dds(dist)}\n"
            "//SMPCNTL  DD  *\n"
            f" ACCEPT S({dist.smp.fmid}) DIS(WRITE) .\n"
            "/*"
        )
        last_smp_step = "ACCEPT.HMASMP"
    else:
        accept_summary = ("//*   (no ACCEPT -- this level never becomes the "
                          "restore base)")
        accept_step = "//*"
        last_smp_step = "APPLY.HMASMP"

    cleanup_step = D.render_cleanup_step(dist, last_smp_step)

    inst_jcl = render_template("smpinst.jcl.tpl", {
        "JOBCARD": D.jobcard(f"{name}INS", f"{name.upper()[:12]} INSTALL"),
        "PRODUCT": name,
        "VERSION": version,
        "FMID": dist.smp.fmid,
        "ALLOC_JOB": alloc_job,
        "EDIT_PREFIX": D.XMIT_EDIT_PREFIX,
        "LKLIB": dist.smp.lklib,
        "DELIM": D.INSTREAM_DELIMITER,
        "MCS": mcs.rstrip("\n"),
        "RECEIVE_SUMMARY": "\n".join(receive_summary),
        "RECEIVE_STEPS": D.render_receive_steps(plan),
        "LAST_RECV": plan[-1][0],
        "APPLY_DDS": D.render_apply_dds(dist),
        "ACCEPT_SUMMARY": f"{accept_summary}\n{cleanup_note}",
        "ACCEPT_STEP": accept_step,
        "CLEANUP_STEP": cleanup_step,
    })

    # The jobs are uploaded to an FB/80 dataset and submitted, so they are held
    # to the same card discipline as the SYSMOD they carry. Checking the
    # rendered text rather than the templates is what catches a comment line
    # that only overflows once a long product name lands in it.
    D.check_card_text(alloc_jcl, alloc_job)
    D.check_card_text(inst_jcl, inst_job)

    (out / alloc_job).write_text(alloc_jcl, encoding="utf-8")
    (out / inst_job).write_text(inst_jcl, encoding="utf-8")
    _log(f"Generated {alloc_job} and {inst_job} (FMID {dist.smp.fmid})")

    # ---- archive ----
    payload: list[tuple[str, Path]] = []
    if dist.readme:
        readme = Path(dist.readme)
        if not readme.is_file():
            raise D.DistributionError(
                f"[distribution] readme = '{dist.readme}' does not exist"
            )
        payload.append(("README.md", readme))
    payload.append((load_xmit, out / load_xmit))
    for _step, dsn, _ph, fname in plan:
        if dsn != dist.smp.lklib:
            payload.append((fname, out / fname))
    payload.append((alloc_job, out / alloc_job))
    payload.append((inst_job, out / inst_job))
    for item in dist.extra:
        p = Path(item)
        if not p.is_file():
            raise D.DistributionError(
                f"[distribution] extra = '{item}' does not exist"
            )
        payload.append((p.name, p))

    zip_path = out / f"{prefix}-smp.zip"
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
        for arcname, src in payload:
            z.write(src, f"{prefix}/{arcname}")

    tar_path = out / f"{prefix}-smp.tar.gz"
    with tarfile.open(tar_path, "w:gz") as t:
        for arcname, src in payload:
            t.add(src, f"{prefix}/{arcname}")

    _log(f"Packaged {zip_path.name} and {tar_path.name} "
         f"({len(payload)} files)")
    return EXIT_SUCCESS


def main() -> None:
    parser = argparse.ArgumentParser(
        description="mbt v2 SMP4 distribution builder")
    parser.add_argument("--project", default="project.toml")
    parser.add_argument("--distdir", default="dist")
    parser.add_argument("--builddir", default="build")
    args = parser.parse_args()

    if not os.path.exists(args.project):
        _fail(f"{args.project} not found")

    try:
        sys.exit(build(args.project, args.distdir, args.builddir))
    except D.DistributionError as e:
        _fail(str(e))
    except ValueError as e:
        _fail(str(e))


if __name__ == "__main__":
    main()
