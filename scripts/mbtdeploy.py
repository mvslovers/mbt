"""mbt v2 deploy — upload built module XMITs to MVS and RECEIVE them.

For each production module the v2 build produces build/{NAME}.xmit
(a TRANSMIT-format load library written by ld370 -xmit).  Deploy, per
module:
  1. stage the XMIT into a sequential dataset on MVS ({HLQ}.MBT.XMIT.IN)
  2. submit a TSO RECEIVE job to unpack it into the target load library
  3. delete the staging dataset

All modules RECEIVE into the SAME target LINKLIB (RECEIVE merges members
into an existing PDS).  When the build later emits a single combined XMIT
holding every module, this becomes one upload + one RECEIVE -- the target
is unchanged.

Target load library (first match wins):
  1. --target on the command line
  2. [deploy] target = "..." in project.toml
  3. {HLQ}.{PROJECT_NAME}.{VRM}.LINKLIB   (default convention)
     e.g. IBMUSER.UFSD.V1R0M0D.LINKLIB for ufsd 1.0.0-dev

Only production modules ([[module]]) are deployed -- tests are not.

Usage:
    mbtdeploy.py [--project project.toml] [--builddir build]
                 [--target DSN] [--module NAME ...] [--dry-run]

Exit codes:
  0  success
  2  config/validation error
  4  mainframe / RECEIVE error
"""

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

from mbt import EXIT_SUCCESS, EXIT_CONFIG, EXIT_MAINFRAME
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


def _resolve_target(args, config: MbtConfig, project: dict) -> str:
    """Resolve the target load library DSN (see module docstring)."""
    if args.target:
        return args.target
    deploy = project.get("deploy", {})
    if deploy.get("target"):
        return deploy["target"]
    name = config.project.name.upper()
    vrm = Version.parse(config.project.version).to_vrm()
    return f"{config.hlq}.{name}.{vrm}.LINKLIB"


def _receive_xmit(client: MvsMFClient, config: MbtConfig,
                  xmit_dsn: str, target_dsn: str) -> int:
    """Submit a TSO RECEIVE job to unpack an XMIT into the target dataset.

    When deps_volume is configured, RECEIVE places a newly allocated
    target on that volume (ignored if the target already exists).
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
    parser.add_argument("--target",
                        help="override target load library DSN")
    parser.add_argument("--module", action="append", default=[],
                        help="deploy only this module (repeatable)")
    parser.add_argument("--dry-run", action="store_true",
                        help="show what would be deployed, touch nothing")
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

    # -- Determine module set --
    modules = _module_names(project)
    if args.module:
        wanted = {m.upper() for m in args.module}
        have = {m.upper() for m in modules}
        unknown = wanted - have
        if unknown:
            _log_error(f"unknown module(s): {', '.join(sorted(unknown))}")
            return EXIT_CONFIG
        modules = [m for m in modules if m.upper() in wanted]
    if not modules:
        _log_error("no production modules to deploy")
        return EXIT_CONFIG

    # -- Verify artifacts exist before touching MVS --
    builddir = Path(args.builddir)
    xmits = []
    for name in modules:
        xpath = builddir / f"{name}.xmit"
        if not xpath.exists():
            _log_error(f"missing artifact: {xpath} (run 'make' first)")
            return EXIT_CONFIG
        xmits.append((name, xpath))

    target = _resolve_target(args, config, project)
    staging = f"{config.hlq}.{STAGING_SUFFIX}"

    _log(f"Deploy target: {target}")
    _log(f"Modules: {', '.join(name for name, _ in xmits)}")

    if args.dry_run:
        for name, xpath in xmits:
            _log(f"[dry-run] would RECEIVE {xpath} → {target} (member {name})")
        return EXIT_SUCCESS

    client = _make_client(config)

    # A fresh staging dataset is allocated and deleted for EACH module
    # (same defensive pattern as bootstrap): reusing one dataset across
    # modules risks stale trailing records if a prior XMIT was larger.
    failures = 0
    for name, xpath in xmits:
        try:
            if client.dataset_exists(staging):
                client.delete_dataset(staging)
            client.create_dataset(
                staging, "PS", "FB", 80, 3120, ["TRK", 50, 20], "SYSDA"
            )
            _log(f"Uploading {name}.xmit → {staging}...")
            client.upload_binary(staging, xpath.read_bytes())
            _log(f"RECEIVE {staging} → {target} (member {name})...")
            _receive_xmit(client, config, staging, target)
            _log(f"Deployed {name}")
        except MvsMFError as e:
            _log_error(f"deploy failed for {name}: {e}")
            failures += 1
        finally:
            try:
                if client.dataset_exists(staging):
                    client.delete_dataset(staging)
            except MvsMFError:
                _log_warn(f"could not delete staging dataset {staging}")

    if failures:
        _log_error(f"{failures} module(s) failed to deploy")
        return EXIT_MAINFRAME
    _log(f"Deploy complete: {len(xmits)} module(s) → {target}")
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
