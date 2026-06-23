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
| `sources` | — | Glob(s) for the archive members. |
| `headers` | `[]` | Public headers shipped in the `-lib` release tarball. |

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
| `[artifacts]` (headers/modules/loads) | `[lib] headers = [...]` + `make package` |

The biggest change: you no longer list NCALIB members to `include`; you
list **source globs** and let the host linker pull the C runtime from
`-lc`. Dataset/space/RECFM blocks disappear entirely.

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
version is used as-is and its SHA is re-verified; the SHA — not the
version string — is the real pin, so a re-pushed prerelease (a moving
`-dev` tag) is detected as a mismatch and fails until you accept it:

```sh
make deps                  # use the lock (verify SHA), or resolve if absent
make deps ARGS=--update    # re-resolve the ranges and rewrite the lock
```

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

- `build.yml` — `make deps` + `make` + `make test` + `make lib`.
- `release.yml` — validates the tag against `project.toml` version, runs
  `make deps` + `make package`, and publishes a GitHub Release with
  `dist/*` (prerelease when the tag contains `-`).

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
