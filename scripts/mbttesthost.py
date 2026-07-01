"""mbt v2 test-host -- build and run the test suite natively on the host.

The fast inner loop: every [[test]] whose sources are portable C (the dual
tests) is compiled with the host compiler and run natively, gating on the exit
code (0 = all passed). No MVS round-trip.

A test is host-runnable when all its sources are .c and it does not set
`host = false`. Tests carrying hand-written .asm/.s (an asm entry wrapper,
asm/istso.asm, ...) are MVS-only, and so are pure-C tests that opt out with
`host = false` (they use MVS-only services like __linkds/LINK with no host
equivalent). Both are skipped here; test-mvs still runs them.

Host build = the project's build.cflags (portable: -std=gnu99 etc.) + the
dependency include dirs + mbt/include (mbtcheck.h), plus any [host] extras:

    [host]
    cc = "cc"                                   # native compiler (default: cc)
    cflags = ["-Wall"]                          # extra host cflags
    sources = ["../lstring370/src/lstr#*.c"]    # extra host link sources
                                                # (a dep's source, since the
                                                # staged .a is the cross build)

Exit codes: 0 all passed; 1 a test failed; 2 config error.
"""

import os
import re
import sys
import glob
import shlex
import argparse
import subprocess
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

from mbtconfig import _resolve_sources

EXIT_OK, EXIT_FAIL, EXIT_CONFIG = 0, 1, 2

_PASS = re.compile(r"^\s*PASS:", re.M)
_FAIL = re.compile(r"^\s*FAIL:", re.M)


def _log(m):
    print(f"[mbt] {m}")


def _err(m):
    print(f"[mbt] ERROR: {m}", file=sys.stderr)


def _host_cflags(cfg, mbt_include: Path) -> list:
    """Include/dialect flags for the host build: the project's portable
    build.cflags + every staged dep include + mbt/include + [host].cflags."""
    flags = list(cfg.get("build", {}).get("cflags", []))
    for inc in sorted(glob.glob(".mbt/deps/*/include")):
        flags += ["-I", inc]
    if mbt_include.is_dir():
        flags += ["-I", str(mbt_include)]
    flags += list(cfg.get("host", {}).get("cflags", []))
    return flags


def main() -> int:
    ap = argparse.ArgumentParser(description="mbt v2 test-host")
    ap.add_argument("--project", default="project.toml")
    ap.add_argument("--builddir", default="build")
    ap.add_argument("--only", action="append", default=[], metavar="TEST",
                    help="run only these tests (repeatable)")
    ap.add_argument("-v", "--verbose", action="store_true")
    args = ap.parse_args()

    try:
        with open(args.project, "rb") as f:
            cfg = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        _err(f"cannot parse {args.project}: {e}")
        return EXIT_CONFIG

    host = cfg.get("host", {})
    cc = host.get("cc", os.environ.get("CC", "cc"))
    extra_sources = _resolve_sources(host.get("sources", []))
    replace = host.get("replace", {})
    mbt_include = Path(__file__).resolve().parent.parent / "include"
    cflags = _host_cflags(cfg, mbt_include)

    outdir = Path(args.builddir) / "host"
    outdir.mkdir(parents=True, exist_ok=True)

    want = {x.upper() for x in args.only}
    rows = {}        # name -> (ok, status, npass, nfail)
    skipped = []
    n_pass_tot = n_fail_tot = 0

    for t in cfg.get("test", []):
        name = t.get("name")
        if not name or (want and name.upper() not in want):
            continue
        # Explicit MVS-only opt-out. A test can be pure C yet un-host-able
        # when it uses MVS-only runtime services (e.g. __linkds/LINK, wtof)
        # that have no host equivalent, so the asm-source heuristic below
        # won't catch it. `host = false` skips it here; test-mvs still runs it.
        if t.get("host") is False:
            skipped.append((name, "host = false"))
            continue
        srcs = _resolve_sources(t.get("sources", []), t.get("exclude", []))
        # Swap env-dependent sources for their host equivalent (e.g. the MVS
        # asm/istso.asm -> the host is_tso() stub src/irx#env.c). After the swap,
        # a test with any remaining non-.c source is MVS-only (skip).
        srcs = [replace.get(s, s) for s in srcs]
        seen = set()
        srcs = [s for s in srcs if not (s in seen or seen.add(s))]
        nonc = [s for s in srcs if not s.endswith(".c")]
        if nonc:
            skipped.append((name, nonc[0]))
            continue

        out = str(outdir / name.lower())
        cmd = [cc] + cflags + srcs + extra_sources + ["-o", out]
        if args.verbose:
            _log("+ " + " ".join(shlex.quote(c) for c in cmd))
        comp = subprocess.run(cmd, capture_output=True, text=True)
        if comp.returncode != 0:
            rows[name] = (False, "COMPILE", 0, 0)
            if args.verbose:
                print(comp.stderr.rstrip())
            continue

        run = subprocess.run([out], capture_output=True, text=True)
        spool = run.stdout + run.stderr
        np = len(_PASS.findall(spool))
        nf = len(_FAIL.findall(spool))
        n_pass_tot += np
        n_fail_tot += nf
        ok = (run.returncode == 0)
        rows[name] = (ok, f"rc={run.returncode}", np, nf)

    # -- report --
    print(f"\n  {'TEST':<10} {'RESULT':<12} {'ASSERTIONS':<14}")
    print(f"  {'-'*10} {'-'*12} {'-'*14}")
    failed = 0
    for name in sorted(rows):
        ok, status, np, nf = rows[name]
        if not ok:
            failed += 1
        cells = ("ok  " if ok else "FAIL ") + status
        print(f"  {name:<10} {cells:<12} {np} pass / {nf} fail")
    if skipped:
        print(f"\n  skipped (MVS-only): "
              f"{', '.join(f'{n} ({why})' for n, why in skipped)}")
    print(f"\n  {len(rows)} host test(s) | assertions: "
          f"{n_pass_tot} PASS, {n_fail_tot} FAIL")

    if failed:
        _log(f"{failed} host test(s) FAILED")
        return EXIT_FAIL
    _log("all host tests passed")
    return EXIT_OK


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
