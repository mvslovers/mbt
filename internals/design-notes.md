# mbt Design Notes

Working document for open design questions and planned changes.
Once resolved, decisions move to the spec or implementation guide.

---

## 1. Release Workflow — Maven-Style Next-Dev-Version

### Problem

After `make release VERSION=1.0.0`, the project is in a released state with
no clear signal about what comes next. The developer must manually bump the
version to `1.0.1-dev` before continuing work. If they forget, the next
`make build` runs against the released version's dataset names (`V1R0M0`),
which is confusing.

The current auto-bootstrap after release (recently added) is also wrong:
it allocates `V1R0M0` datasets right before the developer would immediately
want `V1R0M1-dev` datasets. It wastes time and creates misleading local state.

### Use Cases

Two distinct modes:

**Scenario A — Normal dev-to-release flow** *(main case)*
- Current `project.toml` version: `1.0.0-dev` → datasets: `V1R0M0D`
- `make release VERSION=1.0.0`
- Bump `1.0.0-dev` → `1.0.0`, tag, push, bump to `1.0.1-dev`, push
- Datasets for next dev cycle: `V1R0M1D` (allocated via bootstrap)

**Scenario B — Publish current dev as prerelease** *(no version change)*
- Current `project.toml` version: `1.0.0-dev` → datasets: `V1R0M0D`
- `make prerelease`
- No bump, no `project.toml` change, no dataset change
- Force-push tag `v1.0.0-dev` → CI builds prerelease artifact

### Solution

#### Makefile targets

```makefile
make prerelease                                      # Scenario B
make release VERSION=1.0.0                          # Scenario A
make release VERSION=1.0.0 NEXT_VERSION=2.0.0-dev   # Scenario A with explicit next version
```

#### Scenario A — step by step

```
make release VERSION=1.0.0 [NEXT_VERSION=1.1.0-dev]

  Pre-check: verify tag v1.0.0 does not already exist (local and remote).
             If it does → abort with clear message (see Tag Conflict below).

  Step 1: bump 1.0.0-dev → 1.0.0 in all version_files
          git commit "release: 1.0.0"
          git tag v1.0.0
          git push origin HEAD
          git push origin v1.0.0

  Step 2: bump 1.0.0 → 1.0.1-dev in all version_files  (or NEXT_VERSION if set)
          git commit "chore: bump to 1.0.1-dev"
          git push origin HEAD

  Step 3: print message — developer runs bootstrap explicitly
```

**Note on Step 3:** Bootstrap is intentionally not automated. `make release`
is a publishing act; allocating datasets for the next dev cycle is the first
step of that cycle. A clear message is printed instead:

```
Released 1.0.0. Now on 1.0.1-dev.
Run 'make bootstrap' to allocate build datasets.
```

The existing full `make bootstrap` remains available. A new `--datasets-only`
flag in `mbtbootstrap.py` skips dep resolution and XMIT upload — only project
build datasets are allocated. This is sufficient after a version bump because
dependencies have not changed.

```makefile
bootstrap:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml

bootstrap-datasets:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml --datasets-only
```

#### Scenario B — `make prerelease`

- Reads current version from `project.toml` (e.g. `1.0.0-dev`)
- No bump, no `project.toml` modification
- Force-pushes tag `v1.0.0-dev` — overwrites any existing prerelease tag
- CI picks up the tag and builds a prerelease artifact

```makefile
prerelease:
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml --prerelease
```

Force-push is intentional: a prerelease tag is not a stable anchor point.
Overwriting it on repeated calls is correct behaviour.

#### Scenario C — Rebuild

- `project.toml` version already matches `VERSION` — no modification
- Tag is created (or force-pushed if it already exists) and pushed
- No bump, no bootstrap

#### Tag conflict handling (Scenario A)

A partial or previously aborted release run may leave an existing tag
`v1.0.0` behind. The scenario detection would still classify this as
Scenario A (current version is still `1.0.0-dev`), but `git tag v1.0.0`
would fail with a cryptic Git error.

`mvsrelease.py` performs an explicit pre-check before Step 1:

```
mvsrelease: tag v1.0.0 already exists locally.
If this is a leftover from an aborted run, delete it first:
  git tag -d v1.0.0
  git push origin --delete v1.0.0
Then re-run make release VERSION=1.0.0.
```

Both local and remote tags are checked. The process aborts before any
file modification or git commit is made.

#### Next-dev version rule (Scenario A default)

Patch+1 with `-dev` suffix:

```
1.0.0   →  1.0.1-dev
1.2.3   →  1.2.4-dev
2.0.0   →  2.0.1-dev
```

Override via `NEXT_VERSION` when a minor or major bump is planned:

```sh
make release VERSION=1.0.0 NEXT_VERSION=2.0.0-dev
```

### Changes Required

1. **`mvsrelease.py`**: Implement two-mode logic (A and B). Add pre-check
   for existing tags with actionable error message. Add `--prerelease` flag
   for Scenario B. Remove existing auto-bootstrap call. Print actionable
   message after Scenario A.
2. **`mbtbootstrap.py`**: Add `--datasets-only` flag — skip dep resolution
   and XMIT upload, allocate only project build datasets.
3. **`Makefile`**: Add `prerelease` target. Add `bootstrap-datasets` target.
4. **Spec / README**: Document new targets and two-mode model.

---

## 2. Final Link with Dependencies — Explicit vs. Autocall

### Background

IEWL supports two modes for resolving modules from libraries:

- **Autocall (NCAL)**: IEWL searches the `SYSLIB` DD concatenation to resolve
  unresolved external references automatically. Requires one function per
  PDS member, with the member name matching the function's short name (max
  8 chars). crent370, ufs370 and mqtt370 follow this convention.

- **INCLUDE**: Explicitly pull named members from a DD into the load module
  via `INCLUDE ddname(member)`. Does not require one-function-per-member.
  Required for libs like lua370 where multiple functions are compiled into
  a single C file, producing members that do not map 1:1 to function names.

### Current mbt Behaviour

`mvslink.py` builds a single `SYSLIB` concatenation of all dependency
NCaLIBs and relies on autocall for resolution:

```jcl
//SYSLIB   DD DSN=IBMUSER.EXAPP.B42.NCALIB,DISP=SHR   ← project
//         DD DSN=MBTDEPS.CRENT370.V1R0M0.NCALIB,DISP=SHR
```

This works for autocall-compatible deps. It fails silently for deps like
lua370 where the member naming convention is not followed — IEWL simply
cannot find the required modules via autocall.

### Real-World Link Patterns

Two patterns observed in existing projects illustrate the problem:

**mvsmf** — simple, single DD layout:
- `SYSLIB` = crent370 NCALIB → autocall for crent370 symbols
- `NCALIB` = own modules → explicit `INCLUDE NCALIB(mod)` per module
- No non-autocall dependencies

**httpd** — mixed, two DD layout:
- `SYSLIB` = own NCALIB + crent370 + ufs370 + mqtt370 → autocall resolution;
  `@@CRT1` is explicitly INCLUDEd from here (lives in crent370)
- `NCALIB` = own NCALIB + lua370 NCALIB → explicit `INCLUDE NCALIB(member)`
  for own modules and non-autocall deps
- lua370 appears **only** in `NCALIB`, not in `SYSLIB` — IEWL needs it
  only for the explicit `INCLUDE NCALIB(member)` statements, not for
  autocall resolution

### Solution

#### Ownership of the member list

The dependency itself is the authority on which members it provides.
`mvspackage.py` reads the NCALIB on MVS at packaging time and writes the
member list into `package.toml`. No manual maintenance is required from
either the dep maintainer or the consumer.

The dep maintainer only needs to declare one flag in `project.toml` when
the lib is not autocall-compatible:

```toml
# project.toml of lua370
[link]
autocall = false    # default: true (omit for autocall-compatible libs)
```

`mvspackage.py` reads this flag and, when `autocall = false`, enumerates
the NCALIB members on MVS and writes them into the generated `package.toml`:

```toml
# package.toml of lua370 (generated by mvspackage.py)
[link]
autocall = false
exports  = ["LAPI", "LAUXLIB", "LCODE", "LCOROLIB", "LDBLIB",
            "LDEBUG", "LDO", "LDUMP", "LFUNC", "LGC", "LINIT",
            "LIOLIB", "LLEX", "LMATHLIB", "LMEM", "LOADLIB",
            "LOBJECT", "LOPCODES", "LOSLIB", "LPARSER", "LSTATE",
            "LSTRING", "LSTRLIB", "LTABLE", "LTABLIB", "LTM",
            "LUNDUMP", "LUTF8LIB", "LVM", "LZIO"]
```

Autocall-compatible deps (crent370, ufs370, mqtt370) require no `exports`
list — IEWL resolves them from `SYSLIB` automatically as before.

#### Consumer configuration

The consumer declares which members to pull from non-autocall deps. The
`dep_includes` table in `[link.module]` maps dependency keys to member
selections:

```toml
[link.module]
name    = "HTTPD"
entry   = "@@CRT0"
options = ["LET", "LIST", "XREF", "RENT"]

# Own members to explicitly include
include = ["@@CRT1", "HTTPSTRT", "HTTPD"]

# Explicit member selection for non-autocall dependencies
[link.module.dep_includes]
"mvslovers/lua370" = "*"                    # all exported members
# or an explicit subset:
# "mvslovers/lua370" = ["LAPI", "LAUXLIB"]
```

`"*"` expands to the full `exports` list from the dep's cached `package.toml`.

#### JCL generated by `mvslink.py`

The project's own NCALIB appears in **both** `SYSLIB` and `NCALIB`: in
`SYSLIB` for autocall resolution, in `NCALIB` as the source for explicit
`INCLUDE NCALIB(member)` statements for own modules. Non-autocall deps
appear **only in `NCALIB`** — no `SYSLIB` entry is needed or desired for
these deps.

```jcl
//SYSLIB   DD DSN=IBMUSER.HTTPD.B42.NCALIB,DISP=SHR
//         DD DSN=MBTDEPS.CRENT370.V1R0M0.NCALIB,DISP=SHR   ← autocall
//         DD DSN=MBTDEPS.UFS370.V1R0M0.NCALIB,DISP=SHR     ← autocall
//NCALIB   DD DSN=IBMUSER.HTTPD.B42.NCALIB,DISP=SHR
//         DD DSN=MBTDEPS.LUA370.V1R0M0.NCALIB,DISP=SHR     ← explicit INCLUDEs only
//SYSLMOD  DD DSN=IBMUSER.HTTPD.LOAD,DISP=SHR
//SYSUT1   DD UNIT=SYSALLDA,SPACE=(CYL,(2,1))
//SYSLIN   DD DDNAME=SYSIN
//SYSIN    DD *
 INCLUDE SYSLIB(@@CRT1)
 INCLUDE NCALIB(HTTPSTRT)
 INCLUDE NCALIB(HTTPD)
 INCLUDE NCALIB(LAPI)
 INCLUDE NCALIB(LAUXLIB)
 ... (remaining lua370 members)
 ENTRY @@CRT0
 NAME HTTPD(R)
/*
```

#### Inspecting available exports

After `make bootstrap`, the `package.toml` of each dependency is cached
locally. A dedicated script allows the consumer to inspect what a dep
provides without network access:

```sh
python3 $(MBT_SCRIPTS)/mbtexports.py --dep mvslovers/lua370
```

This follows the existing script-per-function pattern (`mbtbootstrap.py`,
`mvsrelease.py`, etc.). No unified `mbt` dispatcher is introduced.

#### Validation

`mvslink.py` validates `dep_includes` before generating any JCL:

1. **Dep key exists in `[dependencies]`** — hard error if not.
2. **`package.toml` is cached locally** — if not, abort with:
   ```
   mvslink: package.toml for mvslovers/lua370 not found.
   Run 'make bootstrap' first.
   ```
3. **Selected members exist in `exports`** — hard error listing the
   unknown member names if any are not found.

### Changes Required

1. **`project.toml` schema**: Add optional `[link] autocall = false` field.
   Default is `true` — no change required for existing projects.
2. **`mvspackage.py`**: When `autocall = false`, enumerate NCALIB members on
   MVS and write `[link] exports` list into `package.toml`.
3. **`package.toml` schema**: Add `[link]` section with `autocall` (bool)
   and optional `exports` (string array) fields.
4. **`project.py`**: Parse `[link.module.dep_includes]` into `LinkModule`.
   Support `"*"` as wildcard expanding to the full exports list from the
   dep's cached `package.toml`.
5. **`mvslink.py`**: For deps with `autocall = false`, add their NCALIB to
   `NCALIB` DD only (not `SYSLIB`). Generate `INCLUDE NCALIB(member)`
   statements from `dep_includes`. Run three-stage validation before JCL
   generation.
6. **`mbtexports.py`**: New script — reads cached `package.toml` and prints
   the exports list for a given dependency.
7. **Spec / README**: Document `autocall`, `exports`, `dep_includes`, and
   `mbtexports.py`.

### Decisions

- **Default**: `autocall = true` — no change required for existing projects.
- **Exports list**: generated automatically by `mvspackage.py` from the
  NCALIB on MVS — not maintained manually in `project.toml`.
- **DD layout**: project's own NCALIB appears in both `SYSLIB` (autocall)
  and `NCALIB` (explicit own INCLUDEs). Autocall deps appear in `SYSLIB`
  only. Non-autocall deps appear in `NCALIB` only, never in `SYSLIB`.
- **Include order**: own `include` members first, then `dep_includes` in
  dependency declaration order.
- **Wildcard**: `"*"` in `dep_includes` expands to the full `exports` list
  from the dep's `package.toml`.
- **Validation**: three-stage — dep key, cache presence, member existence.
  Each stage produces a distinct, actionable error message.
- **`mbtexports.py`**: standalone script, consistent with existing tooling
  pattern. No unified `mbt` dispatcher.
- **`make verify`**: Convention validation (e.g. no `main` in lib sources,
  member name == function short name when `autocall = true`) is a separate
  future feature and not part of this issue.
