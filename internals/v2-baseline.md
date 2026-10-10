# mbt v2 output baseline — the yardstick for mbt 3

*Measured 2026-10-03. The design proposal (`internals/mbt-3-design.md`, section
"Migration and acceptance") accepts mbt 3 when it produces, for every project,
what mbt v2 produces. This page fixes what "what v2 produces" means, how to
reproduce it, and where it is not reproducible.*

## Result

*Recorded again on 2026-10-05 with the current toolchain; see "Re-recorded" below.*

**mbt v2 builds are reproducible byte for byte once four clock values are
pinned.** Two complete builds of eight projects — different directories, more
than a minute apart — produced **654 identical outputs** (object decks, load
modules, archives, XMIT files, JCL). The only remaining difference inside a
package is one project-specific file (httpd's web root image, below).

On 2026-10-03, with the first toolchain, the same held across the v2 line:
building ufsd, brexx370 and crypto370 with mbt `37b889b` instead of their
pinned `f87bf0c` gave identical outputs (219 of 219). That was not repeated for
the current baseline; all eight projects now pin the same mbt.

## The inputs

| Input | Value |
|---|---|
| Projects | the commits in `internals/baseline/v2-manifest.tsv` (column 2) |
| mbt | each project's pinned submodule, `9947753` (v2.2.0) in all eight |
| Toolchain | cc370 1.4.0 (`2821ebb`), libc370 2.3.1, from the releases |
| Dependencies | as pinned in each project's `mbt.lock` |
| Clock | `LDDATE=26276 LDTIME=120000 ASMDATE=10/03/26 ASMTIME=12.00` |

Commands per project: `make deps`, then `make all`, `make test`, `make package`.

## What changes from build to build, and how it is pinned

| Source | Where | Size | Pinned by |
|---|---|---|---|
| link date/time in the load module (IDR) | every `.iebcopy`, the load XMIT | 2 bytes | `LDDATE`/`LDTIME` (ld370) |
| `INMFTIME` of an XMIT written by `ld370 --pack` | `*-load.xmit` | a few bytes | `LDDATE`/`LDTIME` (ld370 honours them for INMFTIME too) |
| `&SYSDATE`/`&SYSTIME` in assembler source | brexx370 (`PPROC`, `MRXSTART`, `PROLOG` macros), 11 objects | 1 byte each here | `ASMDATE`/`ASMTIME` (as370) |
| tar/gzip/zip member and header times | every `.tar.gz`/`.zip` in `dist/` | — | **not pinned**; compare archive **contents** (the manifest lists each member) |
| `ufsd-utils create` timestamps | httpd's `httpd-webroot.img` inside its dist archive | 24 bytes | **not pinned** — project-specific (httpd's Makefile), not mbt's |

C objects carry no timestamp: all of them were identical without any pinning.

## The manifest

`internals/baseline/v2-manifest.tsv`: project, commit, file, SHA-256 — one line per
output under `build/` and `dist/` (`.o`, `.iebcopy`, `.a`, `.xmit`, `.jcl`), and
one line per member of each `dist/` archive (`archive!member`). 712 entries.
Exception: the `httpd-webroot.img` member hash is from one build and does not
reproduce (above).

An mbt 3 build of the same inputs, with the same clock pinned, must reproduce
every hash. A difference is either a bug in mbt 3 or a deliberate change, and a
deliberate change is named in the PR that makes it.

## Re-recorded (2026-10-05)

The first baseline (2026-10-03) was taken with cc370 1.1.1, libc370 2.1.0 and
mbt 2.0/2.1. Three changes since then alter every load module on purpose, so a
comparison against it would measure them rather than mbt 3:

- cc370 1.3.0 writes `CC370` plus the version instead of `GCCMVS!!` in front of
  every `main`;
- libc370 2.3.0 moved the C startup `@@CRT0` into `libc.a`;
- mbt 2.2.0 (#158, #160) links it from there, so it no longer sits at offset 0.

The projects moved to newer commits as well. Against the first manifest:
362 entries are unchanged (320 object decks among them), 340 changed (all 139
load modules, 162 object decks, 10 XMIT files, 4 archives, 25 archive members),
one left (brexx370's `jccompat.o`, removed with its compatibility layer) and 10
are new (sources added to brexx370).
The clock values are the same as before, so none of that comes from the date.

## Not covered

- **httplua, httprexx, lua370, nsf370** — not yet ported to libc370 2.x; no
  baseline until they build.
- **`make test-mvs` JCL and anything that touches MVS** (deploy, install
  verification) — the generated JCL is rendered offline and could be added; the
  runs themselves are not part of a byte baseline.
- **Host test builds** (`make test-host`) — native binaries, host-compiler
  dependent.

## Found along the way

- **A failed `make deps` is followed by a `make all` that fails with misleading
  errors.** A GitHub API answer `HTTP 403` (the unauthenticated rate limit, after
  many clones in a row) left one dependency unstaged; `make all` then compiled
  anyway and failed with `UINT32 undeclared` and implicit declarations. mbt
  could check that every declared dependency is staged before compiling — a
  requirement for mbt 3. mbt already sends `GITHUB_TOKEN`/`MBT_GITHUB_TOKEN` when
  set, which avoids the limit.
- **`make package` on macOS adds AppleDouble `._*` members** to the lib
  tarball, one per file with extended attributes (#161); release builds run on
  Linux and are clean. They are left out of the manifest: 28 such members
  appeared in the re-recording, none in the first one.
- **A moving dependency makes the manifest unreproducible.** A project that
  depends on a `-dev` prerelease gets a different tarball whenever that
  prerelease is republished (every push to the dependency's `main`), and the
  old one is gone. Re-running the 2026-10-05 manifest the same evening: five
  projects identical; ftpd and httpd identical once `mbt.lock` was restored
  after `make deps` (v2 rewrites the lock to the new SHA, the tree turns
  "dirty", and `MBT_COMMIT` in every banner gains `-dirty`); rexx370 still
  differed in 56 load modules, all linked against a republished
  `lstring370 1.0.0-dev`. So mbt 3 is accepted against a fresh mbt 2 build on
  the same staged inputs (`acceptance/diff23.py`), not against this manifest.
  For mbt 3 itself: a lock whose SHA no longer matches must be an error unless
  the user asks for an update, and fetched archives are cached by SHA.
- **Archive timestamps** could honour `SOURCE_DATE_EPOCH` in `make package` /
  `make dist`, which would make the archives themselves byte-reproducible — a
  small v2 change, and a requirement for mbt 3.
