#!/usr/bin/env python3
"""Differential build: mbt v2 and mbt 3 in the same checkout, same staged deps.

usage: diff23.py MBT3_BINARY WORKDIR PROJ[@COMMIT] ...
Clones (or reuses) each project, stages its dependencies with v2's make deps
(mbt.lock restored afterwards so the tree stays clean), builds with v2
(make all + make test), hashes build/, wipes build/, builds with mbt 3
(build --all --tests) and compares every object deck, archive and load module.
"""
import hashlib, os, subprocess, sys

CLOCK = {"LDDATE": "26276", "LDTIME": "120000", "ASMDATE": "10/03/26", "ASMTIME": "12.00"}
KINDS = (".o", ".a", ".iebcopy")

def sh(cmd, cwd, log, env):
    with open(log, "w") as fh:
        return subprocess.run(cmd, cwd=cwd, shell=True, env=env, stdout=fh, stderr=subprocess.STDOUT).returncode

def hashes(d):
    out = {}
    b = os.path.join(d, "build")
    for dp, _, fn in os.walk(b):
        if "pkg-lib" in dp: continue
        for f in fn:
            if f.endswith(KINDS):
                p = os.path.join(dp, f)
                out[os.path.relpath(p, d)] = hashlib.sha256(open(p, "rb").read()).hexdigest()
    return out

def main():
    mbt3, work = os.path.abspath(sys.argv[1]), sys.argv[2]
    env = dict(os.environ, **CLOCK)
    os.makedirs(work, exist_ok=True)
    bad = 0
    for spec in sys.argv[3:]:
        proj, _, commit = spec.partition("@")
        d = os.path.join(work, proj)
        if not os.path.isdir(d):
            subprocess.run(["git", "clone", "-q", f"https://github.com/mvslovers/{proj}.git", d], check=True)
        if commit:
            subprocess.run(["git", "-C", d, "checkout", "-q", "--force", commit], check=True)
        subprocess.run(["git", "-C", d, "submodule", "update", "-q", "--init", "--recursive"], check=True)
        L = lambda n: os.path.join(work, f"{proj}.{n}.log")
        rc = sh("make distclean >/dev/null 2>&1; make deps && { git ls-files --error-unmatch mbt.lock >/dev/null 2>&1 && git checkout -- mbt.lock || true; }", d, L("deps"), env)
        if rc: print(f"{proj:11s} deps FAILED (see {L('deps')})"); bad += 1; continue
        rc2 = sh("make all && make test", d, L("v2"), env)
        a = hashes(d)
        sh("rm -rf build .mbt/config.mk .mbt/cmd .mbt/include", d, L("wipe"), env)
        rc3 = sh(f"{mbt3} build --all --tests", d, L("v3"), env)
        b = hashes(d)
        same = [k for k in a if b.get(k) == a[k]]
        diff = [k for k in a if k in b and b[k] != a[k]]
        miss = [k for k in a if k not in b]
        extra = [k for k in b if k not in a]
        ok = rc2 == 0 and rc3 == 0 and not diff and not miss and not extra
        bad += not ok
        print(f"{proj:11s} v2rc={rc2} v3rc={rc3} outputs={len(a)} same={len(same)} diff={len(diff)} "
              f"missing={len(miss)} extra={len(extra)} {'OK' if ok else 'FAIL'}")
        for k in (diff + miss + extra)[:6]:
            print(f"   {'diff' if k in diff else 'missing' if k in miss else 'extra'}: {k}")
    sys.exit(1 if bad else 0)

main()
