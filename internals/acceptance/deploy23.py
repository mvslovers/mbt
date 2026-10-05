#!/usr/bin/env python3
"""Compare `make deploy` (mbt 2) with `mbt deploy` (mbt 3) against a local
mvsMF stand-in -- no MVS is touched.

usage: deploy23.py MBT3_BINARY CHECKOUT [SPOOLFILE]
The checkout must be built (make) and must have no .env: every connection
setting comes from the environment and points at 127.0.0.1.  SPOOLFILE is
what the stub returns as the RECEIVE job's spool (default: COND CODE 0000);
a JCL-error spool exercises the failure path.  Compared: the request
transcripts (repeated status polls collapsed; v2 sleeps between them), the
submitted JCL, and the console lines (as a set: Python buffers stdout).
"""
import os, socket, subprocess, sys, time
here = os.path.dirname(os.path.abspath(__file__))
mbt3, d = os.path.abspath(sys.argv[1]), sys.argv[2]
spool = [os.path.abspath(sys.argv[3])] if len(sys.argv) > 3 else []
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
    env = dict(os.environ, MBT_MVS_HOST="127.0.0.1", MBT_MVS_PORT=str(port), MBT_MVS_USER="STUBUSR",
               MBT_MVS_PASS="stubpass", MBT_MVS_HLQ="STUBHLQ", LDDATE="26276", LDTIME="120000")
    r = subprocess.run(cmd, cwd=d, env=env, shell=True, capture_output=True, text=True)
    stub.terminate(); stub.wait()
    lines = [l for l in (r.stdout + r.stderr).splitlines() if not l.startswith("make")]
    reqs = open(log).read().splitlines() if os.path.exists(log) else []
    collapsed = [l for i, l in enumerate(reqs) if i == 0 or l != reqs[i - 1]]
    jcl = open(log + ".jcl").read() if os.path.exists(log + ".jcl") else ""
    return sorted(lines), collapsed, jcl
o2, q2, j2 = run("make deploy", "v2")
o3, q3, j3 = run(f"{mbt3} deploy", "v3")
ok = o2 == o3 and q2 == q3 and j2 == j3
print(f"console {'same' if o2 == o3 else 'DIFF'}, requests {'same' if q2 == q3 else 'DIFF'} ({len(q2)}), JCL {'same' if j2 == j3 else 'DIFF'}")
sys.exit(0 if ok else 1)
