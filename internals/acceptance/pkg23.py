#!/usr/bin/env python3
"""Compare `make package` (mbt 2) with `mbt package` (mbt 3) in checkouts
diff23.py left behind: the load XMIT byte for byte, and the lib tarball member
by member (AppleDouble ._ members, which a macOS tar adds, ignored -- #161)."""
import hashlib, os, subprocess, sys, tarfile
CLOCK = {"LDDATE": "26276", "LDTIME": "120000", "ASMDATE": "10/03/26", "ASMTIME": "12.00"}
mbt3, work = os.path.abspath(sys.argv[1]), sys.argv[2]
env = dict(os.environ, **CLOCK)
def outputs(d):
    out = {}
    dist = os.path.join(d, "dist")
    for f in sorted(os.listdir(dist)) if os.path.isdir(dist) else []:
        p = os.path.join(dist, f)
        if f.endswith("-load.xmit"):
            out[f] = hashlib.sha256(open(p, "rb").read()).hexdigest()
        elif f.endswith("-lib.tar.gz"):
            with tarfile.open(p) as t:
                for m in t.getmembers():
                    if m.isfile() and not os.path.basename(m.name).startswith("._"):
                        out[f + "!" + m.name] = hashlib.sha256(t.extractfile(m).read()).hexdigest()
    return out
bad = 0
for proj in sys.argv[3:]:
    d = os.path.join(work, proj)
    run = lambda c: subprocess.run(c, cwd=d, shell=True, env=env, capture_output=True, text=True)
    run("rm -rf build dist .mbt/cmd"); r2 = run("make package"); a = outputs(d)
    run("rm -rf build dist .mbt/cmd .mbt/config.mk"); r3 = run(f"{mbt3} package"); b = outputs(d)
    diff = [k for k in a if b.get(k) != a[k]]; extra = [k for k in b if k not in a]
    ok = r2.returncode == 0 and r3.returncode == 0 and not diff and not extra and a
    bad += not ok
    print(f"{proj:11s} v2rc={r2.returncode} v3rc={r3.returncode} entries={len(a)}/{len(b)} diff={len(diff)} extra={len(extra)} {'OK' if ok else 'FAIL'}")
    for k in (diff + extra)[:5]: print("   ", k)
    if r3.returncode: print(r3.stderr[-500:])
sys.exit(1 if bad else 0)
