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
  4. prints a per-test matrix and exits nonzero if any test failed -- unless
     no step got a verdict at all, which is a job-level fault (JCL error,
     expired poll, unreadable spool) and is reported as such instead of as
     failed tests.  A step left without a verdict by a *partial* readback is
     shown as `??` and counted as neither pass nor fail.

Tests LOAD data modules (IRXANCHR/IRXPARMS/IRXTSPRM/...) at runtime; those live
in the production LINKLIB, hence the STEPLIB concatenation TESTLIB+LINKLIB. The
production LINKLIB must exist -- run 'make deploy' before 'make test-mvs'.

Exit codes: 0 all passed; 2 config/validation; 4 mainframe error (including a
runner job that did not run, and one whose output could not be read back);
1 tests failed.
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
from mbt.spool import JCL_ERROR_RE, jcl_diagnostics
from mbt.version import Version

# Reuse the deploy plumbing (pack + upload + RECEIVE) verbatim.
from mbtdeploy import (
    _make_client, _load_project, _staging_space, _pack, _receive_xmit,
    _resolve_target as _resolve_linklib, ReceiveError, STAGING_SUFFIX,
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


def _log_warn(msg: str) -> None:
    print(f"[mbt] WARNING: {msg}", file=sys.stderr)


def _log_cont(msg: str) -> None:
    """A continuation line under a _log_error headline (aligned under it)."""
    print(f"[mbt]        {msg}", file=sys.stderr)


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
    # TESTLIB is the STEPLIB; the production LINKLIB (if deployed) follows so a
    # test can LOAD production modules. linklib may be None (nothing deployed).
    steplib = f"//STEPLIB  DD DSN={testlib},DISP=SHR\n"
    if linklib:
        steplib += f"//         DD DSN={linklib},DISP=SHR\n"
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
        lines.append("//SYSTERM  DD SYSOUT=*")
        lines.append("//SYSTSPRT DD SYSOUT=*")
        lines.append("//SYSUDUMP DD SYSOUT=*")
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
        lines.append("//SYSTERM  DD SYSOUT=*")
        lines.append("//SYSTSIN  DD *")
        tp = parms.get(t, {}).get("tso")
        arg = f" '{tp}'" if tp is not None else ""
        lines.append(f" CALL '{testlib}({t})'{arg}")
        lines.append("/*")
        step_map[s] = (t, "tso")

    return "\n".join(lines) + "\n", step_map


# The step has no verdict in the spool at all -- neither executed, nor abended,
# nor skipped. Shared with _job_failure(), which fires when *every* step reads
# this way; keep it a constant so editing the display text cannot silently
# disable that guard.
_NO_RC = "NO RC"


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
    return (None, _NO_RC)


# How many failed readback requests to print before summarising the rest.  A
# full-suite runner has ~110 spool files; when mvsMF is abending, every one of
# them fails and the list is the same message 110 times over.
MAX_SPOOL_ERRORS = 5


def _readback_details(spool_errors: list) -> list:
    """The failed requests, plus how to get the return codes without them.

    IEFACTRT writes each step's RC to SYSLOG, so the run above is recoverable
    from the console alone -- which is the point: this diagnosis fires exactly
    when the REST API is the thing that is broken.
    """
    shown = list(spool_errors[:MAX_SPOOL_ERRORS])
    if len(spool_errors) > MAX_SPOOL_ERRORS:
        shown.append(f"... and {len(spool_errors) - MAX_SPOOL_ERRORS} more")
    return shown + [
        "this is the readback failing, not the job -- the tests may well "
        "have passed",
        "the return codes are on the console: IEFACTRT writes the per-step RC "
        "to SYSLOG,",
        "  IEFACTRT B05     /TSTEXPIR/00:00:00.02/00:00:00.05/00000/MBTTEST",
        "  " + " " * 51 + "^^^^^ step RC",
        "and $HASP165 carries the job-level MAX COND CODE",
    ]


def _job_failure(status: str, spool: str, rows: dict, jobname: str,
                 jobid: str, timeout: int, runner_path: str,
                 spool_path: str, spool_errors: list = None):
    """Did the job fail as a whole? Return (headline, details) or None.

    The trigger is that *no* step got a verdict -- the exact condition under
    which the matrix would print a full column of `FAIL NO RC` for tests of
    which none is broken and none has run, pointing at everything except the
    cause (#74).  Reading it off the parsed rows rather than re-scanning the
    spool keeps it in step with _parse_step_rc: when every step ABENDed the
    rows carry IEF450I verdicts, so this correctly stays quiet and the matrix
    (which then really is test-level information) prints as before.

    spool_errors: the requests that failed while reading the spool back.  That
    gives the fall-through a third reason to distinguish (#87) -- but only for
    a readback that failed outright.  When it failed in part, some steps do
    carry verdicts, this returns None, and main() marks the rest `??`.
    """
    if not rows or not all(st == _NO_RC
                           for legs in rows.values()
                           for (_rc, st) in legs.values()):
        return None

    # JCL error first, and from the spool as well as the status: _parse_spool_rc
    # searches the whole spool for COND CODE before it looks for IEF452I, so a
    # stray match anywhere can leave the status saying something else.
    if JCL_ERROR_RE.search(spool) or status == "JCL ERROR":
        return (
            f"runner job {jobname} {jobid} was rejected -- no test ran",
            jcl_diagnostics(spool) + [
                f"the generated JCL is in {runner_path}",
            ],
        )

    if status == "TIMEOUT":
        return (
            f"runner job {jobname} {jobid} did not finish within {timeout}s "
            f"-- no test result",
            [
                "the job may still be running -- check it on MVS before rerunning",
                "raise the poll with MBT_TEST_TIMEOUT=<seconds>",
            ],
        )

    # The job ran and the spool could not be read back (#87).  Deliberately
    # third: the two branches above come from the status endpoint, which did
    # answer, and what it says is the better fact -- an unreadable spool is
    # then only how the verdict went missing, not what happened.
    if spool_errors:
        return (
            f"runner job {jobname} {jobid} ran, but its output could not be "
            f"read -- no test result",
            _readback_details(spool_errors) + [
                f"whatever was read is in {spool_path}",
            ],
        )

    return (
        f"runner job {jobname} {jobid} produced no return code for any step "
        f"-- no test ran",
        [
            f"the full spool is in {spool_path}",
            f"the generated JCL is in {runner_path}",
        ],
    )


# Test summary lines vary per test (=== N/M passed ===, Passed: N, ...), but
# every test's CHECK macro prints one "PASS:" / "FAIL:" line per assertion --
# a uniform, format-independent tally (counts both the batch and TSO leg).
_PASS = re.compile(r"^\s*PASS:", re.M)
_FAIL = re.compile(r"^\s*FAIL:", re.M)


def _matrix(rows: dict, spool_errors: list = None) -> tuple:
    """Render the per-test matrix. Return (lines, failed, unread).

    A step with no verdict in a spool that was read in full really is wrong,
    and counts as a failure.  With a hole in the spool it is not a verdict
    about the test at all -- neither pass nor fail, just unknown -- and
    counting it as failed is the same mistranslation as #87, one step down:
    a green test reported as broken because a REST call 500'd.  Those cells
    print `??` and are tallied separately.
    """
    lines = [f"  {'TEST':<10} {'BATCH':<14} {'TSO':<14}",
             f"  {'-'*10} {'-'*14} {'-'*14}"]
    failed = unread = 0
    for test in sorted(rows):
        cells = []
        for leg in ("batch", "tso"):
            rc, st = rows[test].get(leg, (None, "MISSING"))
            if spool_errors and st == _NO_RC:
                unread += 1
                cells.append(f"??   {st}")
                continue
            ok = (rc == 0)
            if not ok:
                failed += 1
            cells.append(("ok " if ok else "FAIL ")
                         + (st if rc in (None, 9999) else f"CC {rc}"))
        lines.append(f"  {test:<10} {cells[0]:<14} {cells[1]:<14}")
    return (lines, failed, unread)


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

    client = _make_client(config)

    # Tests run from TESTLIB (the STEPLIB). The production LINKLIB is
    # concatenated after it so a test can LOAD production modules, but it is
    # optional: a milestone with no deployed modules (or a self-contained
    # test) runs from TESTLIB alone. Require it only when it already exists.
    try:
        if not client.dataset_exists(linklib):
            _log(f"Runtime LINKLIB: {linklib} not deployed yet -- "
                 f"running from TESTLIB only")
            linklib = None
        else:
            _log(f"Runtime LINKLIB: {linklib}")
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
            _receive_xmit(client, config, staging, testlib, args.verbose,
                          spool_path=builddir / "testlib-receive.spool",
                          nbytes=len(xmit_bytes))
        except ReceiveError as e:
            # Already a diagnosis -- do not bury it under a second headline.
            _log_error(str(e))
            for line in e.details:
                _log_cont(line)
            return EXIT_MAINFRAME
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

    # The default 120 s poll is too short for large runners (a full-suite job
    # has 110 steps and runs for several minutes; the poll then gives up with
    # an empty spool).  Scale the timeout with the step count; MBT_TEST_TIMEOUT
    # (seconds) overrides.  When it is still too short, _job_failure() below
    # reports the expired poll rather than a matrix of failed tests.
    timeout = int(os.environ.get("MBT_TEST_TIMEOUT", "0") or "0")
    if timeout <= 0:
        timeout = max(120, 10 * len(step_map))
    try:
        result = client.submit_jcl(jcl, timeout=timeout)
    except MvsMFError as e:
        _log_error(f"runner submit failed: {e}")
        return EXIT_MAINFRAME

    spool = result.spool or ""
    spool_errors = list(result.spool_errors or [])
    spool_path = builddir / "test-runner.spool"
    spool_path.write_text(spool)
    jobname = result.jobname or "MBTTEST"

    # -- per-step RC + aggregate summary --
    rows = {}   # test -> {leg: (rc, status)}
    for step, (test, leg) in step_map.items():
        rows.setdefault(test, {})[leg] = _parse_step_rc(spool, jobname, step)

    # A job that never ran gives no step a verdict; printing the matrix then
    # blames the tests for a job-level fault (#74).  Note the deliberate
    # asymmetry: a TIMEOUT whose spool did arrive complete falls through and
    # prints the matrix below -- real per-step verdicts beat a warning about
    # the poll.  EXIT_MAINFRAME, not EXIT_TESTS_FAILED, so CI can tell the two
    # states apart.
    failure = _job_failure(result.status, spool, rows, jobname, result.jobid,
                           timeout, str(runner_path), str(spool_path),
                           spool_errors)
    if failure:
        headline, details = failure
        _log_error(headline)
        for line in details:
            _log_cont(line)
        return EXIT_MAINFRAME

    n_pass = len(_PASS.findall(spool))
    n_fail = len(_FAIL.findall(spool))

    lines, failed, unread = _matrix(rows, spool_errors)
    print()
    for line in lines:
        print(line)
    print(f"\n  job {jobname} {result.jobid}  | assertions (batch+tso): "
          f"{n_pass} PASS, {n_fail} FAIL")

    # Print this last, after the matrix it qualifies: part of the spool is
    # missing, so both the ?? cells and the assertion tally are short of the
    # truth, and neither is the tests' fault.
    if unread:
        _log_error(f"{unread} step(s) have no verdict because the spool could "
                   f"not be read in full -- shown as ?? above, not as failures")
        for line in _readback_details(spool_errors):
            _log_cont(line)
    elif spool_errors:
        _log_warn(f"part of the spool could not be read ({len(spool_errors)} "
                  f"request(s) failed); every step still has a return code, "
                  f"but the assertion tally may be short")
        for line in spool_errors[:MAX_SPOOL_ERRORS]:
            _log_cont(line)

    if failed:
        _log(f"{failed} step(s) FAILED")
        return EXIT_TESTS_FAILED
    # No step failed, but some have no verdict at all -- that is not a pass.
    # EXIT_MAINFRAME, matching the job-level faults above: the fault is the
    # readback, not the tests.
    if unread:
        return EXIT_MAINFRAME
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
