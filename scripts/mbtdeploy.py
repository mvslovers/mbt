"""mbt v2 deploy — pack built load modules into one XMIT and RECEIVE it.

The build leaves a bare load module build/{NAME} for every module it
links.  Deploy packs the load modules that are present in the build dir
into a single multi-member XMIT (ld370 --pack), uploads it, and RECEIVEs
it into the target LINKLIB.  The module set therefore follows the build:

    make clean && make ufsd && make deploy   -> LINKLIB with just UFSD
    make            && make deploy            -> LINKLIB with all modules

Steps:
  1. Obtain the XMIT:
       - one module  -> use the build's own build/{NAME}.xmit (it already
         carries the correct modlen/entry; ld370 --pack does not yet).
       - many modules -> ld370 --pack <load modules> -o build/{PROJECT}.deploy
         -xmit --dsn {TARGET}
  2. upload the XMIT to a staging dataset ({HLQ}.MBT.XMIT.IN)
  3. DELETE the target LINKLIB if it exists  (NJE RECEIVE refuses to
     merge into an existing dataset -> "replace" semantics)
  4. TSO RECEIVE staging -> target  (allocates the target from the XMIT's
     saved attributes, so no SPACE calculation is needed)
  5. delete the staging dataset

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
from mbt.version import Version

# Shared staging dataset for the XMIT upload (same convention as bootstrap).
STAGING_SUFFIX = "MBT.XMIT.IN"


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


def _load_project(path: str) -> dict:
    with open(path, "rb") as f:
        return tomllib.load(f)


def _module_names(project: dict) -> list:
    """Production module names from [[module]] (tests are excluded)."""
    return [m["name"] for m in project.get("module", []) if m.get("name")]


def _built_modules(project: dict, builddir: Path) -> list:
    """Production modules whose bare load module is present in builddir."""
    return [n for n in _module_names(project) if (builddir / n).is_file()]


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


def _receive_xmit(client: MvsMFClient, config: MbtConfig,
                  xmit_dsn: str, target_dsn: str,
                  verbose: bool = False) -> int:
    """Submit a TSO RECEIVE job to unpack an XMIT into the target dataset.

    The target must NOT exist (RECEIVE refuses to merge); deploy deletes
    it first.  When deps_volume is set, the freshly allocated target is
    placed on that volume.
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
    result = client.submit_jcl(jcl)
    if not result.success or result.rc > 4:
        raise MvsMFError(f"RECEIVE job failed (RC={result.rc}) for {xmit_dsn}")
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
            f"no built load modules in {builddir}/ "
            f"(run 'make' or 'make <module>' first)"
        )
        return EXIT_CONFIG

    target = _resolve_target(args, config, project)

    _log(f"Deploy target: {target}")
    _log(f"Modules ({len(built)}): {', '.join(built)}")

    # -- 1. Obtain the XMIT to RECEIVE (local, no MVS) --
    if len(built) == 1:
        # Single module: the build already produced a correct single-member
        # XMIT (right modlen/entry).  ld370 --pack would lose those, so use
        # the plain build XMIT directly.  RECEIVE uses DATASET(target)
        # explicitly, so the XMIT's embedded DSN does not matter.
        xmit = str(builddir / f"{built[0]}.xmit")
        if not Path(xmit).is_file():
            _log_error(f"missing artifact: {xmit} (run 'make {built[0].lower()}' first)")
            return EXIT_CONFIG
        _log(f"Single module: using build XMIT {Path(xmit).name}")
    else:
        # Multiple modules: pack them into one multi-member XMIT.
        # ".deploy" avoids colliding with a module's own {NAME}.xmit on a
        # case-insensitive filesystem (project "ufsd" vs module "UFSD");
        # module names are MVS members and never contain a dot.
        load_modules = [str(builddir / n) for n in built]
        out = str(builddir / f"{config.project.name}.deploy")
        try:
            xmit = _pack(args.ld, load_modules, out, target, args.verbose)
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
        _receive_xmit(client, config, staging, target, args.verbose)
        _log(f"Deploy complete: {len(built)} module(s) -> {target}")
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
