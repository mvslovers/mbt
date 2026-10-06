#!/usr/bin/env python3
"""Schema 2 against schema 3: one mbt 3 binary builds a checkout diff23.py
left behind from its project.toml, and a copy of it after `mbt migrate` from
its mbt.toml.  Same commit, same staged deps, clock pinned.

usage: schema23.py MBT3_BINARY WORKDIR PROJ ...

Compared: every object deck, archive and load module (build --all --tests);
the package -- load XMIT, lib tarball, SMP dist -- member by member; the
host test results as a set (schema 3 runs them in name order).

Expected, and reported as such: the install JCL lists the modules in name
order (SELECT MEMBER and the ++MOD statements), so it differs from the v2
order where that was not alphabetical -- compared after sorting its lines and
the names inside SELECT MEMBER=(...).

Both trees must carry the same build stamp: the migrated copy is dirty (it
removed project.toml and VERSION), so the original is made dirty too, by a
blank line appended to a tracked README.md that is restored afterwards.
"""
import hashlib, os, re, shutil, subprocess, sys, tarfile, zipfile

CLOCK = {"LDDATE": "26276", "LDTIME": "120000", "ASMDATE": "10/03/26", "ASMTIME": "12.00"}
KINDS = (".o", ".a", ".iebcopy")
SKIP = ("httpd-webroot.img",)
PRE = {"httpd": "make webroot"}   # a Makefile step mbt 3 cannot run yet (tasks)

mbt3, work = os.path.abspath(sys.argv[1]), os.path.abspath(sys.argv[2])
env = dict(os.environ, **CLOCK)

def run(cmd, cwd):
    return subprocess.run(cmd, cwd=cwd, shell=True, env=env, capture_output=True, text=True)

def build_hashes(d):
    out = {}
    for dp, _, fn in os.walk(os.path.join(d, "build")):
        if "pkg-lib" in dp or "webroot" in dp:
            continue
        for f in fn:
            if f.endswith(KINDS):
                p = os.path.join(dp, f)
                out[os.path.relpath(p, d)] = hashlib.sha256(open(p, "rb").read()).hexdigest()
    return out

def norm_names(line):
    # "a, c, b" inside a test line or SELECT MEMBER=(...): order is not compared
    line = re.sub(r"MEMBER=\(([^)]*)\)", lambda m: "MEMBER=(" + ",".join(sorted(m.group(1).split(","))) + ")", line)
    if ": " in line:
        head, _, rest = line.partition(": ")
        line = head + ": " + ", ".join(sorted(rest.split(", ")))
    return line

def content(name, data):
    if name.endswith("-inst.jcl"):
        data = "\n".join(sorted(norm_names(l) for l in data.decode("latin-1").splitlines())).encode()
    return hashlib.sha256(data).hexdigest()

def dist_hashes(d):
    out = {}
    dist = os.path.join(d, "dist")
    for f in sorted(os.listdir(dist)) if os.path.isdir(dist) else []:
        p = os.path.join(dist, f)
        if f.endswith("-load.xmit"):
            out[f] = hashlib.sha256(open(p, "rb").read()).hexdigest()
        elif f.endswith(".tar.gz"):
            with tarfile.open(p) as t:
                for m in t.getmembers():
                    b = os.path.basename(m.name)
                    if m.isfile() and not b.startswith("._") and b not in SKIP:
                        out[f + "!" + m.name] = content(m.name, t.extractfile(m).read())
        elif f.endswith(".zip"):
            with zipfile.ZipFile(p) as z:
                for n in z.namelist():
                    b = os.path.basename(n)
                    if not n.endswith("/") and not b.startswith("._") and b not in SKIP:
                        out[f + "!" + n] = content(n, z.read(n))
    return out

def compare(a, b):
    diff = sorted(k for k in a if k in b and a[k] != b[k])
    miss = sorted(k for k in a if k not in b)
    extra = sorted(k for k in b if k not in a)
    return diff, miss, extra

bad = 0
for proj in sys.argv[3:]:
    A = os.path.join(work, proj)
    B = os.path.join(work, "..", "schema23", proj)
    if run("git status --porcelain --untracked-files=no", A).stdout.strip():
        print(f"{proj:11s} SKIP: {A} has tracked changes"); bad += 1; continue
    shutil.rmtree(B, ignore_errors=True)
    os.makedirs(os.path.dirname(B), exist_ok=True)
    run(f"rsync -a --exclude build --exclude dist '{A}/' '{B}/'", work)
    m = run(f"{mbt3} migrate", B)
    if m.returncode:
        print(f"{proj:11s} migrate FAILED\n{m.stdout}{m.stderr}"); bad += 1; continue
    readme = os.path.join(A, "README.md")
    with open(readme, "a") as fh:
        fh.write("\n")
    try:
        res = {}
        for side, d in (("2", A), ("3", B)):
            run("rm -rf build dist .mbt/cmd .mbt/include .mbt/config.mk", d)
            r = run(f"{mbt3} build --all --tests", d)
            h = build_hashes(d)
            t = run(f"{mbt3} test", d)
            tests = sorted(norm_names(l) for l in t.stdout.splitlines() if l.strip() and not l.startswith("[mbt]   cc"))
            run("rm -rf build dist .mbt/cmd", d)
            if proj in PRE:
                if side == "2":
                    run(PRE[proj], A)
                os.makedirs(os.path.join(B, "build"), exist_ok=True)
                if side == "3":
                    run(f"rsync -a '{A}/build/webroot' '{B}/build/'", work)
            p = run(f"{mbt3} package", d)
            res[side] = (r.returncode, h, t.returncode, tests, p.returncode, dist_hashes(d), p.stderr)
        r2, h2, t2, l2, p2, d2, _ = res["2"]
        r3, h3, t3, l3, p3, d3, e3 = res["3"]
        bd, bm, bx = compare(h2, h3)
        dd, dm, dx = compare(d2, d3)
        tests_same = l2 == l3
        ok = (r2 == r3 == 0 and not (bd or bm or bx) and p2 == p3 == 0 and t2 == t3 and tests_same
              and not (dd or dm or dx))
        print(f"{proj:11s} build: {len(h2)} outputs, diff={len(bd)} missing={len(bm)} extra={len(bx)} | "
              f"test-host rc {t2}/{t3} {'same' if tests_same else 'DIFF'} | "
              f"package: {len(d2)} entries, diff={len(dd)} missing={len(dm)} extra={len(dx)} | "
              f"{'OK' if ok else 'FAIL'}")
        for k in (bd + bm + bx)[:6]:
            print("   build:", k)
        for k in (dd + dm + dx)[:8]:
            print("   package:", k)
        if not tests_same:
            for l in sorted(set(l2) ^ set(l3))[:6]:
                print("   test:", l)
        if p3:
            print(e3[-400:])
        bad += not ok
    finally:
        run("git checkout -- README.md", A)
sys.exit(1 if bad else 0)
