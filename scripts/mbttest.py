"""mbt v2 test-mvs -- deploy [[test]] modules to a TESTLIB and run them on MVS.

Each [[test]] builds a standalone load module (build/NAME.iebcopy). This driver:
  1. packs the built test modules into one TESTLIB XMIT and RECEIVEs it into
     {HLQ}.{PROJECT}.{VRM}.TESTLIB (separate from the production LINKLIB, so
     'make deploy' stays clean and tests are never shipped)
  2. generates build/test-runner.jcl: per test a BATCH step (EXEC PGM=) and a
     TSO step (IKJEFT01 CALL), STEPLIB = TESTLIB + LINKLIB, COND=EVEN, so one
     failure never blocks the rest
  3. submits it, parses each step's RC (IEF142I/IEF450I) and each leg's
     "N/M passed (K failed)" summary
  4. prints a per-test matrix and exits nonzero if any test failed

Tests LOAD data modules (IRXANCHR/IRXPARMS/IRXTSPRM/...) at runtime; those live
in the production LINKLIB, hence the STEPLIB concatenation TESTLIB+LINKLIB. The
production LINKLIB must exist -- run 'make deploy' before 'make test-mvs'.

Exit codes: 0 all passed; 2 config/validation; 4 mainframe error; 1 tests failed.
"""

import os
import re
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
from mbt.mvsmf import MvsMFError
from mbt.jcl import jobcard
from mbt.project import ProjectError
from mbt.version import Version

# Reuse the deploy plumbing (pack + upload + RECEIVE) verbatim.
from mbtdeploy import (
    _make_client, _load_project, _staging_space, _pack, _receive_xmit,
    _resolve_target as _resolve_linklib, STAGING_SUFFIX,
)

EXIT_TESTS_FAILED = 1

# Per-step region. MVS 3.8j does NOT treat REGION=0M on an EXEC as "unlimited"
# (it falls back to the ~512K default -> S878/S80A even for tiny tests); a
# concrete value is required. v1's tstall.jcl used 4M for all 1200 tests; 8M
# adds headroom for the larger v2 load modules.
RUNNER_REGION = "8M"


def _log(msg: str) -> None:
    print(f"[mbt] {msg}")


def _log_error(msg: str) -> None:
    print(f"[mbt] ERROR: {msg}", file=sys.stderr)


def _test_names(project: dict) -> list:
    """[[test]] member names."""
    return [t["name"] for t in project.get("test", []) if t.get("name")]


def _built_tests(project: dict, builddir: Path) -> list:
    return [n for n in _test_names(project)
            if (builddir / f"{n}.iebcopy").is_file()]


def _resolve_testlib(config: MbtConfig, project: dict) -> str:
    test = project.get("test_deploy", {})
    if test.get("target"):
        return test["target"]
    name = config.project.name.upper()
    vrm = Version.parse(config.project.version).to_vrm()
    return f"{config.hlq}.{name}.{vrm}.TESTLIB"


def _resolve_fixtures(project: dict, tests: list, config: MbtConfig) -> dict:
    """Resolve each selected test's [[test.fixture]] blocks.

    Returns { test: {"pds": dsn, "dds": [ddname], "members": [(name, text)]} }
    for tests that declare fixtures. Each test gets its own per-test fixture PDS
    (member names may collide across tests, e.g. TSTLOAD's HELLO vs TSTJCL's),
    and all of a test's DDs point at it. Member name = file basename uppercased.
    """
    want = {t.upper() for t in tests}
    name = config.project.name.upper()
    out = {}
    for t in project.get("test", []):
        tn = t.get("name", "")
        if tn.upper() not in want or not t.get("fixture"):
            continue
        dds, members, seen = [], [], set()
        for fx in t["fixture"]:
            dds.append(fx["dd"])
            for mfile in fx.get("members", []):
                member = Path(mfile).stem.upper()[:8]
                if member in seen:
                    continue
                seen.add(member)
                text = Path(mfile).read_text()
                members.append((member, text))
        out[tn] = {
            "pds": f"{config.hlq}.{name}.FIX.{tn}",
            "dds": dds,
            "members": members,
        }
    return out


def _resolve_parms(project: dict, tests: list) -> dict:
    """Per-leg program arguments for the selected tests.

    A test may set `parm` (both legs), and/or `parm_batch` / `parm_tso`
    (per-leg overrides). Returns { test: {"batch": str|None, "tso": str|None} }
    only for tests that set at least one.
    """
    want = {t.upper() for t in tests}
    out = {}
    for t in project.get("test", []):
        tn = t.get("name", "")
        if tn.upper() not in want:
            continue
        common = t.get("parm")
        batch = t.get("parm_batch", common)
        tso = t.get("parm_tso", common)
        if batch is not None or tso is not None:
            out[tn] = {"batch": batch, "tso": tso}
    return out


# Instream delimiter for IEBGENER fixture data. The default '/*' terminator is
# unusable because REXX execs begin with a '/* ... */' comment (cols 1-2 '/*'
# would end the instream early); DLM= moves the terminator off '/*'.
_FIX_DLM = "$A"


def _fixture_dds(fixtures: dict, test: str) -> str:
    """The DD cards (STEPLIB-style) a fixture test's steps need, or ''.

    fixtures[test] = {"pds": dsn, "dds": [ddname, ...], "members": [...]}.
    All of a test's DDs point at its single per-test fixture PDS.
    """
    fx = fixtures.get(test)
    if not fx:
        return ""
    return "".join(f"//{dd:<8} DD DSN={fx['pds']},DISP=SHR\n"
                   for dd in fx["dds"])


def _gen_runner(jobname_card: str, tests: list, testlib: str, linklib: str,
                fixtures: dict = None, parms: dict = None) -> tuple:
    """Build the runner JCL. Return (jcl_text, step_map).

    step_map: { step_name: (test_name, leg) } for leg in {'batch','tso'}.
    fixtures: { test: {"pds": dsn, "dds": [...], "members": [(name, text)]} } --
    members are pre-loaded into the per-test PDS by generated IEBGENER steps
    (the PDS is allocated out-of-band before submit); each DD is added to that
    test's batch + TSO steps.
    parms: { test: {"batch": str|None, "tso": str|None} } -- a per-leg program
    argument (batch via PARM=, TSO via the CALL arg), for tests whose expected
    result differs by environment (e.g. TISTSO asserts is_tso()==0 batch / ==1
    TSO).
    """
    fixtures = fixtures or {}
    parms = parms or {}
    steplib = (f"//STEPLIB  DD DSN={testlib},DISP=SHR\n"
               f"//         DD DSN={linklib},DISP=SHR\n")
    lines = [jobname_card]
    step_map = {}

    # -- fixture-load steps first (members into each test's PDS) --
    fx_i = 0
    for test in tests:
        fx = fixtures.get(test)
        if not fx:
            continue
        for member, text in fx["members"]:
            fx_i += 1
            lines.append(f"//FX{fx_i:03d}  EXEC PGM=IEBGENER")
            lines.append("//SYSPRINT DD SYSOUT=*")
            lines.append("//SYSIN    DD DUMMY")
            lines.append(f"//SYSUT2   DD DSN={fx['pds']}({member}),DISP=SHR")
            lines.append(f"//SYSUT1   DD *,DLM={_FIX_DLM}")
            for ln in text.splitlines():
                lines.append(ln)
            lines.append(_FIX_DLM)

    # -- batch leg --
    for i, t in enumerate(tests, 1):
        b = f"B{i:02d}"
        bp = parms.get(t, {}).get("batch")
        parm = f",PARM='{bp}'" if bp is not None else ""
        lines.append(f"//{b:<8}EXEC PGM={t},COND=EVEN,REGION={RUNNER_REGION}{parm}")
        lines.append(steplib.rstrip())
        fxdd = _fixture_dds(fixtures, t)
        if fxdd:
            lines.append(fxdd.rstrip())
        lines.append("//SYSPRINT DD SYSOUT=*")
        lines.append("//SYSTSPRT DD SYSOUT=*")
        step_map[b] = (t, "batch")

    # -- TSO leg --
    for i, t in enumerate(tests, 1):
        s = f"T{i:02d}"
        lines.append(f"//{s:<8}EXEC PGM=IKJEFT01,DYNAMNBR=50,REGION={RUNNER_REGION},COND=EVEN")
        lines.append(steplib.rstrip())
        fxdd = _fixture_dds(fixtures, t)
        if fxdd:
            lines.append(fxdd.rstrip())
        lines.append("//SYSTSPRT DD SYSOUT=*")
        lines.append("//SYSPRINT DD SYSOUT=*")
        lines.append("//SYSTSIN  DD *")
        tp = parms.get(t, {}).get("tso")
        arg = f" '{tp}'" if tp is not None else ""
        lines.append(f" CALL '{testlib}({t})'{arg}")
        lines.append("/*")
        step_map[s] = (t, "tso")

    return "\n".join(lines) + "\n", step_map


def _parse_step_rc(spool: str, jobname: str, step: str):
    """Return (rc:int, status:str) for a step. rc=9999 for ABEND, None if absent."""
    ab = re.search(rf"IEF450I\s+{jobname}\s+{step}\s+-\s+ABEND\s+(\S+)", spool)
    if ab:
        return (9999, f"ABEND {ab.group(1)}")
    cc = re.search(rf"IEF142I\s+{jobname}\s+{step}\s+-\s+STEP WAS EXECUTED\s+-\s+COND CODE\s+(\d+)", spool)
    if cc:
        return (int(cc.group(1)), "CC")
    if re.search(rf"IEF272I\s+{jobname}\s+{step}\s+-\s+STEP WAS NOT EXECUTED", spool):
        return (None, "NOT EXECUTED")
    return (None, "NO RC")


# Test summary lines vary per test (=== N/M passed ===, Passed: N, ...), but
# every test's CHECK macro prints one "PASS:" / "FAIL:" line per assertion --
# a uniform, format-independent tally (counts both the batch and TSO leg).
_PASS = re.compile(r"^\s*PASS:", re.M)
_FAIL = re.compile(r"^\s*FAIL:", re.M)


def main() -> int:
    ap = argparse.ArgumentParser(description="mbt v2 test-mvs")
    ap.add_argument("--project", default="project.toml")
    ap.add_argument("--builddir", default="build")
    ap.add_argument("--ld", default=os.environ.get("LD", "ld370"))
    ap.add_argument("--target", default=None,
                    help="override the runtime production LINKLIB DSN")
    ap.add_argument("--only", action="append", default=[], metavar="TEST",
                    help="run only these tests (repeatable); e.g. rerun the "
                         "failures: --only TSTLOAD --only TSTJCL")
    ap.add_argument("--no-deploy", action="store_true",
                    help="skip the TESTLIB deploy (reuse what is already there)")
    ap.add_argument("-v", "--verbose", action="store_true")
    args = ap.parse_args()

    try:
        config = MbtConfig(project_path=args.project)
        project = _load_project(args.project)
    except (ProjectError, FileNotFoundError, OSError, tomllib.TOMLDecodeError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    builddir = Path(args.builddir)
    tests = _built_tests(project, builddir)
    if args.only:
        want = {x.upper() for x in args.only}
        tests = [t for t in tests if t.upper() in want]
    if not tests:
        _log_error(f"no built test modules in {builddir}/ (run 'make test' first)")
        return EXIT_CONFIG

    testlib = _resolve_testlib(config, project)
    linklib = _resolve_linklib(args, config, project)
    _log(f"Test library:  {testlib} ({len(tests)} test(s))")
    _log(f"Runtime LINKLIB: {linklib}")

    client = _make_client(config)

    # The data modules tests LOAD at runtime live in LINKLIB -> it must exist.
    try:
        if not client.dataset_exists(linklib):
            _log_error(f"{linklib} not found -- run 'make deploy' first "
                       f"(tests LOAD IRXANCHR/IRXPARMS/... from it)")
            return EXIT_CONFIG
    except MvsMFError as e:
        _log_error(f"cannot reach MVS: {e}")
        return EXIT_MAINFRAME

    # -- deploy the test modules to TESTLIB --
    if not args.no_deploy:
        images = [str(builddir / f"{t}.iebcopy") for t in tests]
        out = str(builddir / f"{config.project.name}.test")
        try:
            xmit = _pack(args.ld, images, out, testlib, args.verbose)
        except RuntimeError as e:
            _log_error(str(e))
            return EXIT_CONFIG
        xmit_bytes = Path(xmit).read_bytes()
        staging = f"{config.hlq}.{STAGING_SUFFIX}"
        try:
            if client.dataset_exists(staging):
                client.delete_dataset(staging)
            client.create_dataset(staging, "PS", "FB", 80, 3120,
                                  _staging_space(len(xmit_bytes)), "SYSDA")
            _log(f"Uploading {Path(xmit).name} -> {staging}...")
            client.upload_binary(staging, xmit_bytes)
            if client.dataset_exists(testlib):
                client.delete_dataset(testlib)
            _log(f"RECEIVE {staging} -> {testlib}...")
            _receive_xmit(client, config, staging, testlib, args.verbose)
        except MvsMFError as e:
            _log_error(f"test deploy failed: {e}")
            return EXIT_MAINFRAME
        finally:
            try:
                if client.dataset_exists(staging):
                    client.delete_dataset(staging)
            except MvsMFError:
                pass

    # -- prepare per-test fixture PDSes (allocate empty; the runner's IEBGENER
    #    steps load the members). Each test gets its own PDS so member names may
    #    collide across tests. --
    fixtures = _resolve_fixtures(project, tests, config)
    for tn, fx in fixtures.items():
        pds = fx["pds"]
        try:
            if client.dataset_exists(pds):
                client.delete_dataset(pds)
            client.create_dataset(pds, "PO", "FB", 80, 3120,
                                  ["TRK", 2, 1, 5], "SYSDA")
            _log(f"Fixture {pds} ({len(fx['members'])} member(s) for {tn})")
        except MvsMFError as e:
            _log_error(f"fixture alloc failed for {tn}: {e}")
            return EXIT_MAINFRAME

    # -- generate + submit the runner --
    jc = jobcard("MBTTEST", config.jes_jobclass, config.jes_msgclass, "MBT TEST")
    parms = _resolve_parms(project, tests)
    jcl, step_map = _gen_runner(jc, tests, testlib, linklib, fixtures, parms)
    runner_path = builddir / "test-runner.jcl"
    runner_path.write_text(jcl)
    _log(f"Runner JCL -> {runner_path} ({len(step_map)} step(s))")

    try:
        result = client.submit_jcl(jcl)
    except MvsMFError as e:
        _log_error(f"runner submit failed: {e}")
        return EXIT_MAINFRAME

    spool = result.spool or ""
    (builddir / "test-runner.spool").write_text(spool)
    jobname = result.jobname or "MBTTEST"

    # -- per-step RC + aggregate summary --
    rows = {}   # test -> {leg: (rc, status)}
    for step, (test, leg) in step_map.items():
        rows.setdefault(test, {})[leg] = _parse_step_rc(spool, jobname, step)
    n_pass = len(_PASS.findall(spool))
    n_fail = len(_FAIL.findall(spool))

    print(f"\n  {'TEST':<10} {'BATCH':<14} {'TSO':<14}")
    print(f"  {'-'*10} {'-'*14} {'-'*14}")
    failed = 0
    for test in sorted(rows):
        cells = []
        for leg in ("batch", "tso"):
            rc, st = rows[test].get(leg, (None, "MISSING"))
            ok = (rc == 0)
            if not ok:
                failed += 1
            cells.append(("ok " if ok else "FAIL ") + (st if rc in (None, 9999) else f"CC {rc}"))
        print(f"  {test:<10} {cells[0]:<14} {cells[1]:<14}")
    print(f"\n  job {jobname} {result.jobid}  | assertions (batch+tso): "
          f"{n_pass} PASS, {n_fail} FAIL")

    if failed:
        _log(f"{failed} step(s) FAILED")
        return EXIT_TESTS_FAILED
    _log("all test steps passed")
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
