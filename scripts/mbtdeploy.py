"""mbt v2 deploy — pack built modules into one LINKLIB XMIT and RECEIVE it.

The build leaves a per-module IEBCOPY unload build/{NAME}.iebcopy for every
module it links.  Unlike a bare load module, the unload carries the PDS2
directory (entry point + module length), so ld370 --pack can combine the
unloads into one multi-member LINKLIB XMIT with every member intact.
The module set follows the build:

    make clean && make ufsd && make deploy   -> LINKLIB with just UFSD
    make            && make deploy            -> LINKLIB with all modules

Steps:
  1. ld370 --pack <build/NAME.iebcopy ...> -o build/{PROJECT}.deploy -xmit
     --dsn {TARGET}    (one or many modules; entry/modlen come from the
     .iebcopy directories)
  2. upload build/{PROJECT}.deploy.xmit to staging ({HLQ}.MBT.XMIT.IN)
  3. DELETE the target LINKLIB if it exists  (NJE RECEIVE refuses to
     merge into an existing dataset -> "replace" semantics)
  4. TSO RECEIVE staging -> target  (allocates the target from the XMIT's
     saved attributes, so no SPACE calculation is needed)
  5. delete the staging dataset

The RECEIVE job's spool is kept in build/receive.spool, and its poll scales
with the XMIT (MBT_DEPLOY_TIMEOUT overrides) -- a poll that expires while the
RECEIVE is still writing would report a failed deploy, and the obvious retry
deletes the dataset that job is filling.

Target LINKLIB (first match wins):
  1. --target on the command line
  2. [deploy] target = "..." in project.toml
  3. {HLQ}.{PROJECT_NAME}.{VRM}.LINKLIB   (default; e.g.
     IBMUSER.UFSD.V1R0M0D.LINKLIB for ufsd 1.0.0-dev)

Exit codes:
  0  success
  1  pack (ld370) failure
  2  config/validation error
  4  mainframe / RECEIVE error
"""

import os
import sys
import argparse
import subprocess
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

from mbt import EXIT_SUCCESS, EXIT_BUILD, EXIT_CONFIG, EXIT_MAINFRAME
from mbt.config import MbtConfig
from mbt.mvsmf import MvsMFClient, MvsMFError
from mbt.jcl import render_template, jobcard
from mbt.project import ProjectError
from mbt.spool import JCL_ERROR_RE, jcl_diagnostics
from mbt.version import Version

# Shared staging dataset for the XMIT upload (same convention as bootstrap).
STAGING_SUFFIX = "MBT.XMIT.IN"


def _log(msg: str) -> None:
    print(f"[mbt] {msg}")


def _log_warn(msg: str) -> None:
    print(f"[mbt] WARNING: {msg}")


def _log_error(msg: str) -> None:
    print(f"[mbt] ERROR: {msg}", file=sys.stderr)


def _log_cont(msg: str) -> None:
    """A continuation line under a _log_error headline (aligned under it)."""
    print(f"[mbt]        {msg}", file=sys.stderr)


class ReceiveError(MvsMFError):
    """A RECEIVE job that did not come back clean, with its diagnosis.

    Carries the extra lines the caller prints under the headline; an
    `except MvsMFError` that does not know about them still gets a headline
    that says what happened.
    """

    def __init__(self, headline: str, details: list = None):
        super().__init__(headline)
        self.details = details or []


def _make_client(config: MbtConfig) -> MvsMFClient:
    return MvsMFClient(
        host=config.mvs_host,
        port=config.mvs_port,
        user=config.mvs_user,
        password=config.mvs_pass,
    )


def _load_project(path: str) -> dict:
    with open(path, "rb") as f:
        return tomllib.load(f)


def _module_names(project: dict) -> list:
    """Production module names from [[module]] (tests are excluded)."""
    return [m["name"] for m in project.get("module", []) if m.get("name")]


def _built_modules(project: dict, builddir: Path) -> list:
    """Production modules whose IEBCOPY unload is present in builddir."""
    return [n for n in _module_names(project)
            if (builddir / f"{n}.iebcopy").is_file()]


def _resolve_target(args, config: MbtConfig, project: dict) -> str:
    """Resolve the target LINKLIB DSN (see module docstring)."""
    if args.target:
        return args.target
    deploy = project.get("deploy", {})
    if deploy.get("target"):
        return deploy["target"]
    name = config.project.name.upper()
    vrm = Version.parse(config.project.version).to_vrm()
    return f"{config.hlq}.{name}.{vrm}.LINKLIB"


def _staging_space(nbytes: int) -> list:
    """TRK space for the FB/80 staging dataset, sized to the XMIT."""
    tracks = max(50, nbytes // 40000 + 30)   # ~40 KB/track + buffer
    return ["TRK", tracks, max(20, tracks // 4)]


def _vlog(verbose: bool, msg: str) -> None:
    """Print an executed command line when --verbose is set."""
    if verbose:
        print(f"[mbt] + {msg}")


def _pack(ld: str, load_modules: list, out: str, dsn: str,
          verbose: bool = False) -> str:
    """Pack load modules into one XMIT via ld370 --pack. Return the .xmit."""
    cmd = [ld, "--pack", *load_modules, "-o", out, "-xmit", "--dsn", dsn]
    _vlog(verbose, " ".join(cmd))
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(
            f"{ld} --pack failed (rc={r.returncode}):\n"
            f"{(r.stderr or r.stdout).strip()}"
        )
    return f"{out}.xmit"


def _receive_timeout(nbytes: int) -> int:
    """Poll timeout for the RECEIVE job, scaled with the XMIT size.

    submit_jcl's flat 120 s default is the wrong risk to take here: a poll
    that expires while the RECEIVE is still writing reports a failed deploy,
    and the obvious retry deletes the very dataset that job is filling (#78,
    and #57 from the other side).  Waiting too long only costs time when
    something really is stuck.  MBT_DEPLOY_TIMEOUT (seconds) overrides.
    """
    env = int(os.environ.get("MBT_DEPLOY_TIMEOUT", "0") or "0")
    if env > 0:
        return env
    return 300 + nbytes // (1024 * 1024) * 60


def _receive_failure(result, target_dsn: str, timeout: int,
                     spool_path: str = ""):
    """Did the RECEIVE fail? Return (headline, details) or None.

    Unlike the test runner's job-level check, there is nothing to infer here:
    the RECEIVE job has a single step, so result.status *is* the
    classification.  What was missing was saying which of them happened --
    result.rc carries _parse_retcode's sentinels (9998 JCL error, 9999
    ABEND/timeout, -1 no retcode at all, which is every job on MVS/CE), and
    printing those raw reads like a return code and names nothing.
    """
    if result.status == "CC" and result.rc <= 4:
        return None

    job = f"{result.jobname} {result.jobid}"
    spool_hint = [f"the full spool is in {spool_path}"] if spool_path else []

    # -- outcome genuinely open: say so, and do not invite a rerun --
    #
    # A rerun deletes the target before its own RECEIVE, so telling the user
    # the dataset is gone when it may not be is how a false alarm turns
    # destructive.  TIMEOUT means the job had not ended; UNKNOWN means it did
    # but its spool named no outcome (or could not be fetched at all), which
    # includes a RECEIVE that in fact succeeded.
    if result.status in ("TIMEOUT", "ACTIVE"):
        return (
            f"RECEIVE job {job} did not finish within {timeout}s "
            f"-- outcome unknown",
            [
                f"the job may still be running and writing {target_dsn}",
                "check it on MVS before rerunning -- a rerun deletes that "
                "dataset first",
                "raise the poll with MBT_DEPLOY_TIMEOUT=<seconds>",
            ] + spool_hint,
        )

    # -- the job ended and said what happened --
    #
    # The target is deleted before the RECEIVE (replace semantics), so in
    # these three cases it is simply not there -- worth saying, because the
    # previous contents are gone with it.
    not_written = f"{target_dsn} was not written"

    if result.status == "JCL ERROR" or JCL_ERROR_RE.search(result.spool or ""):
        return (
            f"RECEIVE job {job} was rejected -- {not_written}",
            jcl_diagnostics(result.spool or "") + spool_hint,
        )

    if result.status == "ABEND":
        return (f"RECEIVE job {job} abended -- {not_written}", spool_hint)

    if result.status == "CC":
        return (f"RECEIVE job {job} failed with RC={result.rc} "
                f"-- {not_written}", spool_hint)

    return (
        f"RECEIVE job {job} ended with no usable status ({result.status}) "
        f"-- outcome unknown",
        [f"check {target_dsn} on MVS before rerunning -- a rerun deletes it "
         f"first"] + spool_hint,
    )


def _receive_xmit(client: MvsMFClient, config: MbtConfig,
                  xmit_dsn: str, target_dsn: str,
                  verbose: bool = False,
                  spool_path=None, nbytes: int = 0) -> int:
    """Submit a TSO RECEIVE job to unpack an XMIT into the target dataset.

    The target must NOT exist (RECEIVE refuses to merge); deploy deletes
    it first.  When deps_volume is set, the freshly allocated target is
    placed on that volume.

    spool_path: where to keep the job's spool, so a failure can be read
    afterwards instead of guessed at.  nbytes sizes the poll (see
    _receive_timeout); 0 keeps the base timeout.

    Raises ReceiveError (an MvsMFError) naming what went wrong.
    """
    jc = jobcard("MBTDEPL", config.jes_jobclass, config.jes_msgclass,
                 "MBT DEPLOY")
    volume = config.deps_volume
    if volume:
        receive_cmd = (
            f" RECEIVE INDSN('{xmit_dsn}') -\n"
            f"  DATASET('{target_dsn}') -\n"
            f"  VOLUME('{volume}')"
        )
    else:
        receive_cmd = (
            f" RECEIVE INDSN('{xmit_dsn}') -\n"
            f"  DATASET('{target_dsn}')"
        )
    _vlog(verbose,
          f"RECEIVE INDSN('{xmit_dsn}') DATASET('{target_dsn}')"
          + (f" VOLUME('{volume}')" if volume else ""))
    jcl = render_template("receive.jcl.tpl", {
        "JOBCARD": jc,
        "XMIT_DSN": xmit_dsn,
        "TARGET_DSN": target_dsn,
        "RECEIVE_CMD": receive_cmd,
    })
    timeout = _receive_timeout(nbytes)
    result = client.submit_jcl(jcl, timeout=timeout)

    if spool_path and result.spool:
        Path(spool_path).write_text(result.spool)

    failure = _receive_failure(result, target_dsn, timeout,
                               str(spool_path) if spool_path else "")
    if failure:
        raise ReceiveError(*failure)
    return result.rc


def main() -> int:
    parser = argparse.ArgumentParser(description="mbt v2 deploy")
    parser.add_argument("--project", default="project.toml")
    parser.add_argument("--builddir", default="build")
    parser.add_argument("--ld", default=os.environ.get("LD", "ld370"),
                        help="ld370 program (for --pack)")
    parser.add_argument("--target",
                        help="override target LINKLIB DSN")
    parser.add_argument("--module", action="append", default=[],
                        help="deploy only this module (repeatable)")
    parser.add_argument("--dry-run", action="store_true",
                        help="pack locally and report, but touch no MVS")
    parser.add_argument("-v", "--verbose", action="store_true",
                        help="echo the ld370/RECEIVE commands that are run")
    args = parser.parse_args()

    # -- Load config + project --
    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG
    try:
        project = _load_project(args.project)
    except (OSError, tomllib.TOMLDecodeError) as e:
        _log_error(f"cannot parse {args.project}: {e}")
        return EXIT_CONFIG

    # -- Determine the module set from what was built --
    builddir = Path(args.builddir)
    built = _built_modules(project, builddir)
    if args.module:
        wanted = {m.upper() for m in args.module}
        known = {m.upper() for m in _module_names(project)}
        unknown = wanted - known
        if unknown:
            _log_error(f"unknown module(s): {', '.join(sorted(unknown))}")
            return EXIT_CONFIG
        built = [m for m in built if m.upper() in wanted]
    if not built:
        _log_error(
            f"no built modules in {builddir}/ "
            f"(run 'make' or 'make <module>' first)"
        )
        return EXIT_CONFIG

    target = _resolve_target(args, config, project)

    _log(f"Deploy target: {target}")
    _log(f"Modules ({len(built)}): {', '.join(built)}")

    # -- 1. Pack the per-module IEBCOPY unloads into one LINKLIB XMIT --
    # Each build/NAME.iebcopy carries its PDS2 directory (entry + modlen),
    # which --pack preserves; a bare load module would lose them.  ".deploy"
    # avoids colliding with build/NAME.iebcopy on a case-insensitive
    # filesystem (project "ufsd" vs module "UFSD").
    images = [str(builddir / f"{n}.iebcopy") for n in built]
    out = str(builddir / f"{config.project.name}.deploy")
    try:
        xmit = _pack(args.ld, images, out, target, args.verbose)
    except RuntimeError as e:
        _log_error(str(e))
        return EXIT_BUILD
    _log(f"Packed {len(built)} module(s) -> {Path(xmit).name}")
    xmit_bytes = Path(xmit).read_bytes()

    if args.dry_run:
        _log(f"[dry-run] would upload {Path(xmit).name} -> staging")
        _log(f"[dry-run] would delete + RECEIVE -> {target}")
        return EXIT_SUCCESS

    # -- 2..5. Upload, replace target, RECEIVE --
    client = _make_client(config)
    staging = f"{config.hlq}.{STAGING_SUFFIX}"
    try:
        if client.dataset_exists(staging):
            client.delete_dataset(staging)
        client.create_dataset(
            staging, "PS", "FB", 80, 3120,
            _staging_space(len(xmit_bytes)), "SYSDA"
        )
        _log(f"Uploading {Path(xmit).name} -> {staging}...")
        client.upload_binary(staging, xmit_bytes)

        if client.dataset_exists(target):
            _log(f"Deleting existing {target} (replace)...")
            client.delete_dataset(target)

        _log(f"RECEIVE {staging} -> {target}...")
        _receive_xmit(client, config, staging, target, args.verbose,
                      spool_path=builddir / "receive.spool",
                      nbytes=len(xmit_bytes))
        _log(f"Deploy complete: {len(built)} module(s) -> {target}")
    except ReceiveError as e:
        # Already a diagnosis -- do not bury it under a second "deploy failed".
        _log_error(str(e))
        for line in e.details:
            _log_cont(line)
        return EXIT_MAINFRAME
    except MvsMFError as e:
        _log_error(f"deploy failed: {e}")
        return EXIT_MAINFRAME
    finally:
        try:
            if client.dataset_exists(staging):
                client.delete_dataset(staging)
        except MvsMFError:
            _log_warn(f"could not delete staging dataset {staging}")

    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
