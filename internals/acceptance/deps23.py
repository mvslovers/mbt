#!/usr/bin/env python3
"""Compare `make deps ARGS=--update` (mbt 2) with `mbt deps --update` (mbt 3)
in the same checkout: the staged tree under .mbt/deps (every file by SHA-256)
and mbt.lock.  Both re-resolve every range.  Without --update the two differ
on purpose: mbt 3 never moves a pin by itself, mbt 2 re-pinned a republished
prerelease with a warning (#52)."""
import hashlib, os, subprocess, sys
mbt3, work = os.path.abspath(sys.argv[1]), sys.argv[2]
def tree(d):
    out = {}
    for dp, _, fn in os.walk(os.path.join(d, ".mbt", "deps")):
        for f in fn:
            p = os.path.join(dp, f); out[os.path.relpath(p, d)] = hashlib.sha256(open(p, "rb").read()).hexdigest()
    return out
bad = 0
for proj in sys.argv[3:]:
    d = os.path.join(work, proj)
    run = lambda c: subprocess.run(c, cwd=d, shell=True, capture_output=True, text=True)
    run("rm -rf .mbt/deps"); r2 = run("make deps ARGS=--update")
    t2, l2 = tree(d), open(os.path.join(d, "mbt.lock")).read() if os.path.exists(os.path.join(d, "mbt.lock")) else ""
    run("git checkout -- mbt.lock 2>/dev/null; rm -rf .mbt/deps"); r3 = run(f"{mbt3} deps --update")
    t3, l3 = tree(d), open(os.path.join(d, "mbt.lock")).read() if os.path.exists(os.path.join(d, "mbt.lock")) else ""
    ok = r2.returncode == 0 and r3.returncode == 0 and t2 == t3 and l2 == l3
    bad += not ok
    print(f"{proj:11s} v2rc={r2.returncode} v3rc={r3.returncode} files={len(t2)}/{len(t3)} tree={'same' if t2 == t3 else 'DIFF'} lock={'same' if l2 == l3 else 'DIFF'} {'OK' if ok else 'FAIL'}")
    if not ok: print(r3.stdout[-600:], r3.stderr[-600:])
    run("git checkout -- mbt.lock 2>/dev/null")
sys.exit(1 if bad else 0)
