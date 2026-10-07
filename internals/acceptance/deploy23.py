#!/usr/bin/env python3
"""Compare an MVS-touching command of mbt 2 and mbt 3 against a local
mvsMF stand-in -- no MVS is touched.

usage: deploy23.py MBT3_BINARY CHECKOUT [SPOOLFILE] [--test]
--test compares `make test-mvs` with `mbt test --mvs` instead of deploy.
The checkout must be built (make) and must have no .env: every connection
setting comes from the environment and points at 127.0.0.1.  SPOOLFILE is
what the stub returns as the RECEIVE job's spool (default: COND CODE 0000);
a JCL-error spool exercises the failure path.  Compared: the request
transcripts (repeated status polls collapsed; v2 sleeps between them), the
submitted JCL, and the console lines (as a set: Python buffers stdout).

Intended differences, left out of the comparison: mbt 3 opens one mvsMF
session per run (POST and DELETE /zosmf/services/authenticate), and says
that it falls back to the mbt 2 settings because there is no targets.toml.
MBT_HOME points at an empty directory, so a real ~/.mbt/targets.toml can
never redirect the run to a real system.
"""
import tempfile
import os, socket, subprocess, sys, time
here = os.path.dirname(os.path.abspath(__file__))
mbt3, d = os.path.abspath(sys.argv[1]), sys.argv[2]
args = [a for a in sys.argv[3:] if a != "--test"]
test = "--test" in sys.argv
spool = [os.path.abspath(args[0])] if args else []
if os.path.exists(os.path.join(d, ".env")):
    sys.exit(f"{d}/.env exists -- refusing: it could point at a real system")
def free_port():
    s = socket.socket(); s.bind(("127.0.0.1", 0)); p = s.getsockname()[1]; s.close(); return p
def run(cmd, tag):
    port = free_port(); log = os.path.join(d, f".mbt/stub-{tag}.log")
    for f in (log, log + ".jcl"):
        if os.path.exists(f): os.remove(f)
    stub = subprocess.Popen([sys.executable, os.path.join(here, "stub_mvsmf.py"), str(port), log] + spool)
    time.sleep(1)
    env = dict(os.environ, MBT_HOME=tempfile.mkdtemp(), MBT_MVS_HOST="127.0.0.1", MBT_MVS_PORT=str(port), MBT_MVS_USER="STUBUSR",
               MBT_MVS_PASS="stubpass", MBT_MVS_HLQ="STUBHLQ", LDDATE="26276", LDTIME="120000")
    r = subprocess.run(cmd, cwd=d, env=env, shell=True, capture_output=True, text=True)
    stub.terminate(); stub.wait()
    lines = [l for l in (r.stdout + r.stderr).splitlines() if not l.startswith("make") and not l.startswith("[cc370]") and not l.startswith("[ld370]") and not l.startswith("[ar370]") and not l.startswith("[as370]") and "Tests built" not in l and "writable data" not in l and "module-data" not in l and not l.startswith("[mbt] A module") and not l.startswith("[mbt] An ac") and "libc370 " not in l and "Build complete" not in l and "using the mbt 2 settings" not in l]
    reqs = open(log).read().splitlines() if os.path.exists(log) else []
    reqs = [l for l in reqs if "/zosmf/services/authenticate" not in l]
    collapsed = [l for i, l in enumerate(reqs) if i == 0 or l != reqs[i - 1]]
    jcl = open(log + ".jcl").read() if os.path.exists(log + ".jcl") else ""
    return sorted(lines), collapsed, jcl
o2, q2, j2 = run("make test-mvs" if test else "make deploy", "v2")
o3, q3, j3 = run(f"{mbt3} test --mvs" if test else f"{mbt3} deploy", "v3")
if o2 != o3:
    import difflib
    print("\n".join(difflib.unified_diff(o2, o3, "v2", "v3", lineterm="", n=0)))
ok = o2 == o3 and q2 == q3 and j2 == j3
print(f"console {'same' if o2 == o3 else 'DIFF'}, requests {'same' if q2 == q3 else 'DIFF'} ({len(q2)}), JCL {'same' if j2 == j3 else 'DIFF'}")
sys.exit(0 if ok else 1)
