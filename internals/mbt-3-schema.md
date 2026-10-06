# mbt.toml — schema 3

The project file of mbt 3 (`internals/mbt-3-design.md` §6). It replaces mbt
2's `project.toml`; `mbt migrate` converts one into the other. This page is
the reference: every key, its default, and the `project.toml` key it comes
from.

A directory holding both `mbt.toml` and `project.toml` is a configuration
error (exit 2): mbt does not pick one.

## Decisions behind it (maintainer, 2026-10-06)

- **Modules and tests are keyed tables** (`[module.HTTPD]`), and mbt orders
  them **by name** wherever order is visible: the build, the module list of
  the SMP package, the test matrix. The file keeps whatever order its author
  chose; nothing depends on it. Measured before deciding: in 5 of 8 projects
  the v2 arrays were not in name order, so the first build after migrating
  lists them differently once. Load modules and object decks do not change.
- **Tests are discovered** (below). `mbt migrate` is lossless: it keeps each
  test's source list where it is not the discovered default, so test load
  modules stay identical. Shortening the lists is later work, per project.
- **The version lives in `[project] version` only.** `VERSION` is no longer
  read or written; measured: nothing in the 8 projects or their workflows
  reads it (the release workflow compares the tag with the project file).
- **The FMID is derived** from `[smp] prefix` and the version; explicit
  `fmid` / `delete` still override.

## Top level

| key | | |
|---|---|---|
| `schema` | required | `3` |

## `[project]`

| key | default | from v2 |
|---|---|---|
| `name` | required | `project.name` |
| `version` | required | `project.version` |
| `kind` | `"application"` | `project.type` — `application`, `library`, `module` |

## `[toolchain]`

| key | default | from v2 |
|---|---|---|
| `libc370` | — | `toolchain.libc370`; schema 3 needs `>= 2.3.0` (the CRT in `libc.a`) |
| `mbt` | — | new; read by the launcher (design §5), ignored by the core |

## `[dependencies]`

Unchanged: `"owner/repo" = "<semver range>"`.

## `[build]`

| key | default | from v2 |
|---|---|---|
| `include` | `[]` | the `"-I", "<dir>"` pairs of `build.cflags`, in order |
| `cflags` | `[]` | the rest of `build.cflags`, in order |
| `asflags` | `[]` | `build.asflags` |

The compile line is mbt's own flags, then `-I` for each `include`, then
`cflags` — the order v2 produced when the `-I` pairs led its `cflags`.
`mbt migrate` keeps the v2 list in `cflags` unchanged where an `-I` pair
does not lead it, so the command line stays identical.

### `[build.host]` — the native test build

| key | from v2 |
|---|---|
| `cflags` | `host.cflags` |
| `sources` | `host.sources` |
| `replace` | `host.replace` |

## `[lib]` and `[internal]`

Unchanged from v2: `[lib] name, sources, headers`; `[internal] sources,
exclude`.

## `[module.<NAME>]`

`<NAME>` is the load module's member name (quoted where it holds `#`, `@` or
`$`: `[module."IRX#HELO"]`).

| key | default | from v2 |
|---|---|---|
| `sources` | required | same |
| `exclude` | `[]` | same |
| `rent`, `reus` | **required** | same — declared, never inherited (cc370#100) |
| `refr` | `false` | same |
| `entry` | `"@@CRT0"` | same |
| `startup` | a C program | `false` (own entry) or `"crtm"`. `"crt0"`/`"crt1"` are an error: the CRT comes out of `libc.a`; `mbt migrate` drops them |
| `dep_startup` | — | same |
| `ac` | `0` | same |
| `aliases` | `[]` | same |

`norent` / `noreus` are gone; `mbt migrate` writes `rent = false` /
`reus = false`.

## Tests

### Discovery

Every `test/**/*.c` and `test/**/*.asm` is a test, named after its file in
upper case (`test/mvs/tstgctx.c` → `TSTGCTX`), with that file as its only
source — unless

- it is listed in `[tests] exclude`, or
- a `[test.<NAME>]` entry names it in its `sources` (a helper of another
  test, or a test under another name).

A discovered name that is not a valid member name is an error naming the
file; exclude it or give it an entry.

### `[tests]` — defaults for every test

| key | default | |
|---|---|---|
| `rent`, `reus` | required once there is a test | for every test without its own |
| `refr` | `false` | |
| `exclude` | `[]` | files under `test/` that are not tests |

### `[test.<NAME>]` — only where a test differs

| key | default | from v2 |
|---|---|---|
| `sources` | the discovered file `<name>.c`/`.asm` | `test.sources` |
| `exclude`, `entry`, `startup`, `dep_startup`, `ac`, `rent`, `reus`, `refr` | as for a module, `[tests]` for the attributes | same |
| `host` | `true` | same: `false` runs it on MVS only |
| `mvs` | `true` | same: `false` runs it on the host only |
| `parm` | — | `test.parm` (a string, both steps), or `{ batch = "…", tso = "…" }` from `parm_batch` / `parm_tso` |
| `fixtures` | — | `[[test.fixture]]`: `{ <DD> = ["member file", …] }`, in DD-name order |

An entry `[test.<NAME>]` without `sources` needs a discovered file of that
name.

## `[deploy]`

| key | default | from v2 |
|---|---|---|
| `target` | `<NAME>.DEV.LINKLIB` | `deploy.target` |
| `test_target` | — | `test_deploy.target` |

## `[distribution]`

| key | default | from v2 |
|---|---|---|
| `readme` | — | same |
| `extra` | `[]` | same |
| `[distribution.library.<dir>]` | | `[[distribution.library]]`, keyed by its `dir` |
| ↳ `target` | `<NAME>.<DIR>` | same |

## `[smp]`

Needed when there is a `[distribution]`. `<NAME>` is the project name in
upper case, `<P4>` its first four characters.

| key | default | from v2 |
|---|---|---|
| `prefix` | required unless `fmid` is set | new: `T` + three product letters |
| `fmid` | `prefix` + major + minor + `0` | `distribution.smp.fmid` |
| `delete` | `prefix` + major + (minor − 1) + `0` | `distribution.smp.delete`; required for a `x.0.0` (`[]` for a first level) |
| `system` | `"Z038"` | same |
| `prereq` | `[]` | same |
| `accept_fmid` | `true` | same |
| `lklib` | `<NAME>.<P4>LOAD` | `distribution.smp.lklib` |
| `target` | `<NAME>.LINKLIB` | same |
| `distlib` | `<NAME>.A<P4>LOD` | same |

The derivation gives one FMID per minor (design §6.4): a patch release is a
PTF, which mbt does not build yet. Until it does, packaging a version with a
patch component other than 0 needs an explicit `fmid` — mbt refuses rather
than spend the minor's id a second time. No version component may exceed 9.

`mbt migrate` writes `prefix` and leaves out what the derivation and the
defaults produce; it writes every value that differs (mvsMF's `lklib` and
`distlib` do: `MVSMF.MVSMFLOD`, `MVSMF.AMVSMFLD`). The dataset names of a
shipped product never change by migrating — renaming one is a trap of its
own (root `CLAUDE.md`, "A DELETE never touches the predecessor's data set").

## `[release]`

| key | default | from v2 |
|---|---|---|
| `version_files` | `[]` | same, without `VERSION` |

## Reserved, not implemented yet

`[rule."*.ext"]`, `[files."<glob>"]`, `[lang.*]` (design §10), `[tools]` and
tasks (design §9, step 4).

## `mbt migrate`

Reads `project.toml`, writes `mbt.toml` beside it, and removes `project.toml`
and `VERSION`. `--dry-run` prints the new file instead. Comments travel with
the key or table they stand above; multi-line values are copied verbatim, so
comments inside a source list survive. The Makefile and the `mbt` submodule
are left to the migrating PR (design §16).
