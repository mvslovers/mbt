# mbt v2 output baseline — the yardstick for mbt 3

*Measured 2026-10-03. The design proposal (`docs/mbt-3-design.md`, section
"Migration and acceptance") accepts mbt 3 when it produces, for every project,
what mbt v2 produces. This page fixes what "what v2 produces" means, how to
reproduce it, and where it is not reproducible.*

## Result

**mbt v2 builds are reproducible byte for byte once four clock values are
pinned.** Two complete builds of eight projects — different directories, more
than a minute apart — produced **645 identical outputs** (object decks, load
modules, archives, XMIT files, JCL). The only remaining difference inside a
package is one project-specific file (httpd's web root image, below).

The same holds across the v2 line itself: building ufsd, brexx370 and crypto370
with mbt `37b889b` instead of their pinned `f87bf0c` gave the identical outputs
(219 of 219), so the baseline is not tied to one v2 commit.

## The inputs

| Input | Value |
|---|---|
| Projects | the commits in `internals/baseline/v2-manifest.tsv` (column 2) |
| mbt | each project's pinned submodule (`f87bf0c` or `37b889b`; identical output, see above) |
| Toolchain | cc370 1.1.1 (`98d9ab0`), libc370 2.1.0, from the releases |
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
one line per member of each `dist/` archive (`archive!member`). 703 entries.
Exception: the `httpd-webroot.img` member hash is from one build and does not
reproduce (above).

An mbt 3 build of the same inputs, with the same clock pinned, must reproduce
every hash. A difference is either a bug in mbt 3 or a deliberate change, and a
deliberate change is named in the PR that makes it.

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
- **Archive timestamps** could honour `SOURCE_DATE_EPOCH` in `make package` /
  `make dist`, which would make the archives themselves byte-reproducible — a
  small v2 change, and a requirement for mbt 3.
