# Migrating to mbt v2 (cc370 host build)

mbt **v2** replaces the v1 *remote* build (cross-compile on the host, then
assemble + link on MVS via JCL/mvsMF) with a *host* build: compile,
assemble, link and package run entirely on the host with the **cc370**
toolchain (`cc370` / `as370` / `ld370` / `ar370`). MVS is only touched by
`make deploy`, which uploads the finished load library and RECEIVEs it.

| | v1 (legacy) | v2 |
|---|---|---|
| Compile | `c2asm370` (`.c`→`.s`) | `cc370` (`.c`→`.o`) |
| Assemble / link | upload to MVS, IFOX00 + IEWL via JCL | `as370` / `ld370` on the host |
| MVS round-trip per build | yes (every module) | no |
| Makefile include | `mk/legacy/core.mk` | `mk/mbt.mk` |
| Config generator | `scripts/legacy/mbtconfig.py` | `scripts/mbtconfig.py` |
| `project.toml` | dataset + `[[link.module]]` blocks | `[[module]]` + glob `sources` |

The shared Python package (`scripts/mbt/`) — config, mvsMF client, JCL,
versioning — is used by both.

---

## 1. Quick start

A v2 project's `Makefile` is two lines:

```make
MBT_ROOT := mbt
include $(MBT_ROOT)/mk/mbt.mk
```

Everything else is described in `project.toml`. Then:

```sh
make                 # build all production modules
make <module>        # build one module (lowercase name, e.g. make ufsd)
make test            # build test modules
make lib             # build the static library
make package         # create release tarballs in dist/
make deploy          # pack modules -> XMIT -> upload -> RECEIVE into the LINKLIB
make doctor          # check toolchain + MVS connectivity
make help            # list targets

VERBOSE=1 make       # echo full cc370/as370/ld370/ar370 commands
```

---

## 2. `project.toml` reference (v2)

A complete example (ufsd):

```toml
[project]
name    = "ufsd"
version = "1.0.0-dev"
type    = "application"          # application | library | runtime

[build]
cflags  = ["-I", "include"]      # extra cc370 flags (appended to -O1)
# asflags = ["..."]              # extra as370 flags (optional)

# ── Load modules ─────────────────────────────────────────
[[module]]
name    = "UFSD"
startup = "crt1"                 # crt0 (default) | crt1 | crtm | false
sources = ["src/ufsd*.c"]        # glob(s), expanded on the host
exclude = ["src/ufsdclnp.c", "src/ufsd#ssi.c"]

[[module]]
name    = "UFSDSSIR"
entry   = "UFSDSSIR"             # non-default entry point
startup = false                  # no C runtime startup (LINK_NOCRT)
sources = ["src/ufsd#ssi.c", "src/ufsd#buf.c"]

[[module]]
name    = "UFSDCLNP"             # all defaults: entry=@@CRT0, startup=crt0
sources = ["src/ufsdclnp.c"]

# ── Tests (built by `make test`) ─────────────────────────
[[test]]
name    = "LIBUFTST"
sources = ["client/libufstst.c", "client/libufs.c"]

# ── Static library (built by `make lib`) ─────────────────
[lib]
name    = "libufs"
sources = ["client/libufs.c"]
headers = ["include/libufs.h", "include/ufsdrc.h"]

# ── Release (used by `make release` / `prerelease`) ──────
[release]
version_files = ["VERSION"]

# ── Deploy (optional) ────────────────────────────────────
# [deploy]
# target = "IBMUSER.UFSD.LINKLIB"   # overrides the default DSN

# ── Dependencies (optional; `make deps`) ─────────────────
# [dependencies]
# "mvslovers/crent370" = ">=1.0.6"

# ── Toolchain (optional; release CI only) ────────────────
# [toolchain]
# libc370 = "1.0.2"                 # bare version -> tag v1.0.2
# cc370   = "main"                  # cc370 has no releases yet
```

### `[project]`

| Key | Required | Meaning |
|-----|----------|---------|
| `name` | yes | Project name; lowercase, used in default DSNs. |
| `version` | yes | SemVer; encoded to MVS VRM for DSNs (`1.0.0-dev` → `V1R0M0D`). |
| `type` | no | `application` (default), `library`, or `runtime`. |

### `[build]`

| Key | Meaning |
|-----|---------|
| `cflags` | List of extra `cc370` flags, appended to the default `-O1`. |
| `asflags` | List of extra `as370` flags (optional). |

Note: `CFLAGS`/`ASFLAGS`/`LDFLAGS` are set with `:=` in `mk/mbt.mk`, so a
host `LDFLAGS`/`CFLAGS` in the environment does **not** leak into the
cross-build. Override on the command line if needed (`make CFLAGS=-O0`).

### Build provenance — `<buildstamp.h>`

Every `make` regenerates `.mbt/buildstamp.h` and puts `.mbt` on the include
path, so a banner can name the exact build it is:

```c
#include <buildstamp.h>

wtof("%s %s (%s) STARTING", MBT_PROJECT, MBT_VERSION, MBT_COMMIT);
if (MBT_COMMIT_DIRTY)
    wtof("BUILT FROM A MODIFIED WORKING TREE");
```

| Macro | Value |
|-------|-------|
| `MBT_PROJECT` | `[project] name` |
| `MBT_VERSION` | `[project] version` |
| `MBT_COMMIT` | Short commit of the **project** checkout, with a `-dirty` suffix when the tree has uncommitted *tracked* changes; `unknown` outside a git checkout (a source tarball). |
| `MBT_COMMIT_DIRTY` | The same signal as `0`/`1`, for code that reports it separately. |

**Use this instead of injecting the commit as a cflag.** A
`-DCOMMIT="$(shell git rev-parse --short HEAD)"` in `[build] cflags` bakes
the hash into whichever object holds the banner, and make recompiles that
object only when its source or a tracked header changes — so a build made
after a commit still prints the *previous* hash. As a header it is a
tracked prerequisite (via `-MMD`), so a new commit recompiles exactly the
translation units that include it.

The file is rewritten only when a value actually changes, so an unchanged
commit recompiles nothing. It deliberately carries no build timestamp —
that would differ on every run and recompile the including TU forever.
`.mbt/` is generated; do not commit it.

### `[[module]]` (production load module, repeatable)

| Key | Default | Meaning |
|-----|---------|---------|
| `name` | — | MVS member name (1–8 chars). |
| `sources` | — | Glob pattern(s), expanded on the host. |
| `exclude` | `[]` | Glob pattern(s) removed from `sources`. |
| `entry` | `@@CRT0` | Entry point symbol. |
| `startup` | `crt0` | C runtime: `crt0`, `crt1`, `crtm`, or `false` (none). |

`startup` selects how the module is linked:

| `startup` | Linker macro | crt object | typical use |
|-----------|--------------|-----------|-------------|
| `crt0` | `LINK_CRT0` | `crt0.o` | normal C program |
| `crt1` | `LINK_CRT1` | `crt1.o` | C program needing threading runtime |
| `crtm` | `LINK_CRTM` | `crtm.o` | minimal runtime |
| `false` | `LINK_NOCRT` | — | self-contained module (e.g. an SSI router); still linked with `-lc` to resolve runtime routines |

### `[[test]]` (repeatable)

Same fields as `[[module]]`. Built only by `make test`, never by `make`,
and never deployed.

### `[lib]`

| Key | Default | Meaning |
|-----|---------|---------|
| `name` | project name | Archive name → `build/<name>.a`. |
| `sources` | — | Glob(s) for the archive members. Omit for a headers-only export. |
| `headers` | `[]` | Public headers shipped in the `-lib` release tarball. |

A `[lib]` with `headers` **but no `sources`** is a *headers-only* export: no
`.a` is built or shipped — the `-lib` tarball carries `include/` alone.
Use this when the public API is reached at **runtime** rather than linked —
e.g. httpd, whose CGI programs call `http_*` through a callback table the
server fills in, so consumers compile against the headers and link nothing.

### `[internal]` — shared-code archive for multi-module projects

Most projects give each `[[module]]` a `sources` glob and let the linker pull
the C runtime from `-lc`. That breaks down when **several modules share a body
of code that cannot be globbed into each** — the classic case is a project
where every module root defines its own `main()`, so globbing all sources into
every module is doubly-defined at link.

`[internal]` solves this. Its `sources` are compiled and archived into
`build/<project>int.a`, and **every module (and test) autocalls that archive**.
Each `[[module]]` then lists only its own root source(s); the linker pulls the
shared rest from the archive **by autocall** — referenced members only, so each
load module stays minimal (important on MVS).

| Key | Default | Meaning |
|-----|---------|---------|
| `sources` | — | Glob(s) compiled into the internal archive. |
| `exclude` | `[]` | Glob(s) removed from `sources`. |

```toml
[internal]
sources = ["src/*.c", "credentials/src/*.c"]   # -> build/<project>int.a

[[module]]
name    = "HTTPJES2"
startup = "crt1"
sources = ["src/cgistart.c", "src/httpjes2.c"] # roots only; rest via autocall
```

This is the v2 re-expression of v1's NCALIB autocall (in v1, `c_dirs` fed the
NCALIB, which *was* the autocall library). Unlike `[lib]` — a public
deliverable shipped in the release tarball — the internal archive is never
packaged or shipped; it exists only to compose this project's own modules.
A project may have both: `[internal]` for module composition and a curated
`[lib]` for external consumers.

A module root listed in `sources` is also a member of the internal archive
(its glob covers the whole tree). That is harmless: the explicit object
satisfies the symbol, and autocall skips the archive copy — no doubly-defined.

#### Why the roots must be explicit (and which root)

It is tempting to drop the explicit root and let autocall pull *everything*
from the archive. That does **not** work, and the failure is silent. cc370
compiles each translation unit to an unnamed **Private Code** section that
exports only a few `LD` labels; a TU with `main()` exports `@@START`, the C
entry the CRT (`crt0`/`crt1`) references **strongly**. In a project where
several TUs have `main()` (a server plus N CGI programs, say), the internal
archive contains **multiple** `@@START` definitions. Autocall then satisfies
the CRT's `@@START` reference from the *first* archive member that defines it
— which may not be the root you intended. The link succeeds (RC=0, no
unresolved references), but the load module carries the **wrong entry** and
faults at run time.

Listing the intended root as an explicit `sources` object pins *that* TU's
`@@START` (and its other symbols) **before** autocall runs, so the module
gets the entry you meant. This is exactly what v1's `[[link.module]] include`
list did. It also means the seeded root must be the one that actually defines
the entry: seed the wrong sibling and you get the silent wrong-entry module
above. (See mvslovers/cc370#8 for a proposed linker diagnostic.)

If a TU belongs in the shared archive but must never be autocalled as an
entry — e.g. a CGI launcher that also defines `@@START` but is only ever a
per-module root — `exclude` it from `[internal]` so its `@@START` can never
shadow another module's:

```toml
[internal]
sources = ["src/*.c"]
exclude = ["src/cgistart.c"]   # a per-module root + shipped in [lib], not an autocall member
```

### `[release]`

| Key | Meaning |
|-----|---------|
| `version_files` | Files whose version string is bumped on release (e.g. `VERSION`). |

### `[deploy]` (optional)

| Key | Default | Meaning |
|-----|---------|---------|
| `target` | `{HLQ}.{NAME}.{VRM}.LINKLIB` | Target load library DSN. |

The default for ufsd 1.0.0-dev is `IBMUSER.UFSD.V1R0M0D.LINKLIB`
(`HLQ` from `.env`/`MBT_MVS_HLQ`, default `IBMUSER`). Override here, or
per run with `make deploy ARGS="--target ..."`.

### `[dependencies]` (optional)

`"owner/repo" = ">=x.y.z"`. Resolved/downloaded by `make deps` (the v2
dependency fetcher; not yet implemented — see roadmap).

### `[toolchain]` (optional)

Which cc370 / libc370 a **release** is built with.

| Key | Default | Meaning |
|-----|---------|---------|
| `cc370` | `main` | git ref for the compiler + tools. |
| `libc370` | `main` | git ref for the sysroot (headers, `crt*.o`, `libc.a`). |

A bare semver names a release and resolves to its tag — `libc370 = "1.0.2"`
checks out `v1.0.2`. Any other value is already a git ref and is used as
given: a branch (`main`), a `v`-prefixed tag (`v1.0.2`), or a commit SHA.
An unknown key is a hard error, so a typo cannot quietly leave the release
floating on `main`.

Why it exists: libc370 stamps its own version into every module it is linked
into (`src/clib/@@ver.c`), so a release built against the tip of libc370
`main` announces a `-dev` C runtime at STC startup — and nothing records
which toolchain produced the published artifact, although `mbt.lock` pins
every `[dependencies]` entry by version *and* SHA256.

**Only `release.yml` honours this.** `build.yml` stays on `main` on purpose:
a PR built against the tip of the toolchain is what catches a cc370/libc370
regression before it reaches a consumer. The cost of the asymmetry is that a
release exercises a toolchain combination no PR build did — keep the pin
reasonably current.

`cc370` has no releases yet, so leave it at `main` (or omit it) until it does.

**Bump the `mbt` submodule before you declare the section.** The workflow is
resolved from `mbt@main` but the submodule is pinned per project, so the
resolver may be missing from an older checkout. Declaring `[toolchain]` with a
submodule that predates it fails the release with a message saying so, rather
than publishing an artifact that quietly ignored the pin. A project that
declares nothing is unaffected either way.

---

## 3. v1 → v2 `project.toml` mapping

| v1 | v2 |
|----|----|
| `[build] cflags = ["-std=gnu99", "-I./include"]` | `[build] cflags = ["-I", "include"]` (no host C-standard flags) |
| `[build.sources] c_dirs = [...]` | per-module `sources` globs (dirs are derived) |
| `[mvs.build.datasets.*]` (SOURCE/OBJECT/NCALIB/LOAD) | **removed** — no MVS datasets at build time |
| `[mvs.install.*]` | `[deploy] target` (optional) |
| `[link] autocall = false` | **removed** — `ld370` links with `-lc` |
| `[[link.module]] include = ["@@CRT1", ...]` | `[[module]] sources = [...]` + `startup` |
| `[[link.module]] entry = "@@CRT0"` | `[[module]] entry = "@@CRT0"` (same; default) |
| `[[link.module]] options = ["RENT", ...]` | **removed** — handled by the toolchain |
| test as a `[[link.module]]` | `[[test]]` |
| `[artifacts] headers = true, header_files = [...]` | `[lib] headers = [...]` (headers-only, no `sources`) + `make package` |

The biggest change: you no longer list NCALIB members to `include`; you
list **source globs** and let the host linker pull the C runtime from
`-lc`. Dataset/space/RECFM blocks disappear entirely.

**Multi-module projects that shared an NCALIB** (every `[[link.module]]`
`include`-d a couple of roots and autocalled the rest from the project's own
NCALIB) map the NCALIB to an `[internal]` archive: put the shared source globs
in `[internal] sources`, and give each `[[module]]` only its root source(s).
The autocall semantics carry over unchanged — see [`[internal]`](#internal--shared-code-archive-for-multi-module-projects).

---

## 4. Deploy

`make deploy` packs the **built** modules and RECEIVEs them into one
LINKLIB. The module set follows what is in `build/`:

```sh
make ufsd && make deploy     # LINKLIB with just UFSD
make && make deploy          # LINKLIB with all modules
make deploy ARGS="--dry-run" # pack locally, touch no MVS
```

Mechanics: each module is linked to a per-module **IEBCOPY unload**
(`build/NAME.iebcopy`) that carries its PDS2 directory (entry point +
module length). `ld370 --pack` combines the unloads into one LINKLIB
**XMIT**; deploy uploads it, **deletes** the target LINKLIB (TSO RECEIVE
will not merge into an existing dataset), and RECEIVEs the new one.

---

## 5. Dependencies

Declare dependencies on other mvslovers projects in `[dependencies]`,
keyed `owner/repo` with a semver range:

```toml
[dependencies]
"mvslovers/ufsd" = ">=1.0.0-dev"
```

`make deps` resolves each range against the dependency's GitHub Releases,
downloads its `{repo}-{version}-lib.tar.gz` asset, and stages it under
`.mbt/deps/{repo}/` (`include/` + `lib/`). The build wires these in
automatically — `-I .mbt/deps/*/include` on compile, `.mbt/deps/*/lib/*.a`
on link — so no path config is needed in `project.toml`.

`make deps` also writes **`mbt.lock`** (version + SHA256 per dep) at the
project root. **Commit it** — it is source-of-record, not a build
artifact: `project.toml` holds the *range* (`>=…`), the lock holds the
*resolved* version and the exact content hash. Keeping `.mbt/` ignored
is correct; the lock sits at the root next to `project.toml`, so `make
clean`/`distclean` never disturb it. On the next `make deps` the locked
version is used as-is and its SHA is re-verified. How a drifted SHA is
handled depends on the resolved version:

- **stable** (`X.Y.Z`) — the asset is immutable, so a changed SHA is a
  hard error (`make deps` fails); re-pin deliberately with `--update`.
- **prerelease** (`-dev` / `-rcN`) — the tag legitimately moves, so a
  changed SHA is expected: `make deps` accepts it with a **WARNING** and
  rewrites the lock to the current SHA automatically (no `--update`
  needed). Re-pin with a stable release when you need reproducibility.

```sh
make deps                  # use the lock (verify SHA), or resolve if absent
make deps ARGS=--update    # re-resolve the ranges and rewrite the lock
```

This encodes the resolver's intent: **`-dev` is rolling, stable is
pinned.** It also keeps a whole ecosystem of rolling `-dev` prereleases
building green without a lock-churn commit every time an upstream
re-pushes (see issue #52).

A range that names a prerelease bound (`>=1.0.0-dev`) opts that
dependency into prereleases; a plain range (`>=1.0.0`) ignores them.

### Local override (working against an unreleased dependency)

To build against a local working copy of a dependency instead of a
GitHub release — e.g. while developing both projects in lockstep —
create **`.mbt/deps.local.toml`** (gitignored, never committed):

```toml
[override]
"mvslovers/ufsd" = { path = "../ufsd" }
```

`make deps` then stages that dep from its own `build/<lib>.a` and the
headers in its `[lib]` section — run `make lib` in the override path
first. GitHub and the SHA lock are skipped for that dep; the committed
`mbt.lock` keeps its release pin, so removing the override file
restores the locked release with no further changes.

---

## 6. CI (GitHub Actions)

mbt ships reusable workflows. A v2 project's CI is **host-only** (no MVS).

`.github/workflows/build.yml`:

```yaml
on:
  pull_request:
  push:
    branches: [main]
jobs:
  build:
    uses: mvslovers/mbt/.github/workflows/build.yml@main
```

`.github/workflows/release.yml`:

```yaml
on:
  push:
    tags: ["v*"]
jobs:
  release:
    uses: mvslovers/mbt/.github/workflows/release.yml@main
```

The v2 workflows clone + `make install` the cc370 toolchain (cached per
cc370 commit), run `make deps` (so dependency libraries are staged before
the build), then run the host build:

- `build.yml` — `make deps` + `make` + `make test` + `make lib`. Always
  builds against the tip of cc370/libc370, so a toolchain regression shows
  up on a PR rather than in a consumer's release.
- `release.yml` — validates the tag against `project.toml` version, checks
  out the toolchain declared in `[toolchain]` (section 2; default `main`),
  runs `make deps` + `make package`, and publishes a GitHub Release with
  `dist/*` (prerelease when the tag contains `-`). The resolved refs are
  echoed into the log as `[mbt] cc370 @ …` / `[mbt] libc370 @ …`.

`release.yml` also takes `cc370_ref` / `libc370_ref` inputs, which override
`[toolchain]` for a one-off run; leave them unset to use the declaration.

Pin a tag (`@vX.Y.Z`) instead of `@main` for reproducibility. Legacy (v1)
projects keep using `build-legacy.yml` / `release-legacy.yml` (MVS/CE in
Docker).

---

## 7. Migrating an existing project

1. Update `mbt` (the submodule) to a v2 commit.
2. Replace `Makefile` with the two-line v2 include (`mk/mbt.mk`).
3. Rewrite `project.toml` per section 2 (use the mapping in section 3).
4. Declare any dependencies in `[dependencies]` (section 5); commit
   `mbt.lock` after a first `make deps`.
   If `[build] cflags` injects a version or commit stamp
   (`-DVERSION=…`, `-DCOMMIT=…`), drop those flags and switch the banner to
   `<buildstamp.h>` (section 2) — a stamp in a cflag goes stale on the next
   commit.
5. Point `.github/workflows/*.yml` at the v2 reusable workflows (section 6).
6. `make doctor` — verify the cc370 toolchain (and MVS, for deploy).
7. `make deps` then `make` then `make deploy ARGS="--dry-run"`.
8. `make deploy` — first live deploy (writes to MVS).

---

## 8. Legacy (v1)

The v1 remote build is preserved under `mk/legacy/` and
`scripts/legacy/`. Projects not yet migrated keep their old `Makefile`:

```make
MBT_ROOT := mbt
include $(MBT_ROOT)/mk/legacy/core.mk
```

v1 is in maintenance mode; new work targets v2. `legacy/` will be removed
once all ecosystem projects have migrated.
