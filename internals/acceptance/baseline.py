#!/usr/bin/env python3
"""Rebuild the projects of a baseline manifest and compare the outputs.

usage: baseline.py MANIFEST WORKDIR [--build CMD] [--only PROJ ...]

For every project in MANIFEST: clone it at the manifest's commit (with
submodules) into WORKDIR, run the build with the clock pinned, hash every
output under build/ and dist/ the way the manifest was recorded, and diff.
--build replaces the v2 make sequence (for an mbt 3 binary, say).
Exit 0 when every manifest entry is reproduced; 1 otherwise.
"""
import argparse, hashlib, os, subprocess, sys, tarfile, zipfile

CLOCK = {"LDDATE": "26276", "LDTIME": "120000", "ASMDATE": "10/03/26", "ASMTIME": "12.00"}
V2 = "make deps && make all && make test && make package"
# not reproducible by construction (internals/v2-baseline.md)
UNPINNED = ("httpd-webroot.img",)

def run(cmd, cwd, env, log):
    with open(log, "w") as fh:
        return subprocess.run(cmd, cwd=cwd, env=env, shell=True, stdout=fh, stderr=subprocess.STDOUT).returncode

def outputs(root):
    out = {}
    for sub in ("build", "dist"):
        for dp, _, fn in os.walk(os.path.join(root, sub)):
            for f in fn:
                fp = os.path.join(dp, f); rel = os.path.relpath(fp, root)
                if f.endswith((".o", ".iebcopy", ".a", ".xmit", ".jcl")):
                    out[rel] = hashlib.sha256(open(fp, "rb").read()).hexdigest()
                elif f.endswith((".tar.gz", ".tgz")):
                    with tarfile.open(fp) as t:
                        for m in t.getmembers():
                            if m.isfile() and not os.path.basename(m.name).startswith("._"):
                                out[rel + "!" + m.name] = hashlib.sha256(t.extractfile(m).read()).hexdigest()
                elif f.endswith(".zip"):
                    with zipfile.ZipFile(fp) as z:
                        for n in z.namelist():
                            if not n.endswith("/") and not os.path.basename(n).startswith("._"):
                                out[rel + "!" + n] = hashlib.sha256(z.read(n)).hexdigest()
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("manifest"); ap.add_argument("workdir")
    ap.add_argument("--build", default=V2); ap.add_argument("--only", nargs="*")
    a = ap.parse_args()
    want = {}
    for line in open(a.manifest):
        if line.startswith("#") or not line.strip(): continue
        proj, commit, path, sha = line.rstrip("\n").split("\t")
        want.setdefault((proj, commit), {})[path] = sha
    env = dict(os.environ, **CLOCK)
    os.makedirs(a.workdir, exist_ok=True)
    bad = 0
    for (proj, commit), files in sorted(want.items()):
        if a.only and proj not in a.only: continue
        d = os.path.join(a.workdir, proj)
        if not os.path.isdir(d):
            subprocess.run(["git", "clone", "-q", f"https://github.com/mvslovers/{proj}.git", d], check=True)
        subprocess.run(["git", "-C", d, "checkout", "-q", "--force", commit], check=True)
        subprocess.run(["git", "-C", d, "submodule", "update", "-q", "--init", "--recursive"], check=True)
        subprocess.run("make distclean >/dev/null 2>&1; rm -rf build dist .mbt", cwd=d, shell=True)
        rc = run(a.build, d, env, os.path.join(a.workdir, f"{proj}.log"))
        got = outputs(d)
        miss = [p for p in files if p not in got]
        diff = [p for p in files if p in got and got[p] != files[p] and not p.endswith(UNPINNED)]
        extra = [p for p in got if p not in files]
        ok = rc == 0 and not miss and not diff and not extra
        bad += not ok
        print(f"{proj:11s} {commit} rc={rc} entries={len(files)} same={len(files)-len(miss)-len(diff)} "
              f"diff={len(diff)} missing={len(miss)} extra={len(extra)} {'OK' if ok else 'FAIL'}")
        for p in (diff + miss + extra)[:8]:
            print(f"   {'diff' if p in diff else 'missing' if p in miss else 'extra'}: {p}")
    sys.exit(1 if bad else 0)

if __name__ == "__main__":
    main()
