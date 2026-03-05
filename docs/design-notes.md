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

### Proposed Solution

Follow the Maven release plugin pattern:

```
make release VERSION=1.0.0 [NEXT_VERSION=1.1.0-dev]
  Step 1: bump 1.0.0-dev → 1.0.0 in all version_files
          git commit "release: 1.0.0"
          git tag v1.0.0
          git push origin HEAD
          git push origin v1.0.0
  Step 2: bump 1.0.0 → 1.0.1-dev in all version_files  (or NEXT_VERSION if set)
          git commit "chore: bump to 1.0.1-dev"
          git push origin HEAD
  Step 3: bootstrap (allocate new datasets for next-dev version)
```

The developer is immediately on the next dev cycle. CI picks up the tag and
runs the full release pipeline on MVS.

**Next-dev-version rule (default):** patch+1 with `-dev` suffix.
```
1.0.0   →  1.0.1-dev
1.2.3   →  1.2.4-dev
2.0.0   →  2.0.1-dev
```

**NEXT_VERSION override:** When the default patch+1 is not desired (e.g. a
minor or major bump is planned), the developer passes it explicitly:
```sh
make release VERSION=1.0.0 NEXT_VERSION=2.0.0-dev
```

**Bootstrap after next-dev bump (Step 3):** After bumping to `1.0.1-dev`,
the local build datasets (`V1R0M1`) do not yet exist on MVS. Bootstrap must
run to allocate them. The question is scope:

- **Full bootstrap**: re-resolve deps, upload XMITs, allocate datasets.
  Safe and correct, but slow — especially if dep uploads take minutes.
- **Dataset-only bootstrap**: skip dep resolution and upload, only allocate
  project build datasets. Fast, but requires a new `--datasets-only` flag
  in `mbtbootstrap.py`.

Proposed: implement `--datasets-only` and use it here. Dependencies have not
changed — only the project version changed. Uploading XMITs again is wasteful.
Full `make bootstrap` (with dep upload) remains available explicitly.

Makefile change:
```makefile
release:
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml $(VERSION) \
	    $(if $(NEXT_VERSION),--next-version $(NEXT_VERSION),)
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml --datasets-only
```

### Open Questions

- Should the next-dev suffix be configurable (e.g. `-SNAPSHOT` vs `-dev`)?
  Current convention in the ecosystem is `-dev`. Keep it fixed for now.
- What if `VERSION` is already the current version (no-op release)?
  Existing fix in `mvsrelease.py` handles this — skip file update, tag only.
  Step 2 and Step 3 should also be skipped in this case (version unchanged,
  datasets already exist).

---

## 2. Final Link with Dependencies — Explicit vs. Autocall

### Background

IEWL supports two modes for resolving modules from libraries:

- **NCAL / Autocall**: IEWL searches the `SYSLIB` DD concatenation to resolve
  unresolved external references automatically. Requires one function per
  PDS member (member name = function name, max 8 chars).

- **INCLUDE**: Explicitly pull named members from a DD into the load module.
  `INCLUDE ddname(member)`. Does not depend on one-function-per-member.

### Current mbt Behaviour

`mvslink.py` builds a single `SYSLIB` concatenation:

```jcl
//SYSLIB   DD DSN=IBMUSER.EXAPP.B42.NCALIB,DISP=SHR   ← project
//         DD DSN=MBTDEPS.EXLIB.V1R0M0.NCALIB,DISP=SHR ← dep
```

IEWL autocall searches this concat. Explicit INCLUDEs reference `SYSLIB`:

```
 INCLUDE SYSLIB(@@CRT1)
 INCLUDE SYSLIB(EXAPP)
```

`[link.module] include` in project.toml is the flat list that drives these
INCLUDE statements. All entries reference the `SYSLIB` DD.

### The Problem

Dependencies that do **not** follow the one-function-per-member convention
(e.g. lua370 with 31 members in a single compiled C file, or crent370 with
many grouped modules) cannot be resolved via autocall. Their members must be
explicitly INCLUDEd.

The current flat `include` list handles this — the user adds the dep's members
manually. But:

1. The consumer must know the internal member structure of the dependency.
2. A large dep like crent370 or lua370 has many members; listing all is
   verbose and brittle.
3. The consumer should not be responsible for maintaining the dep's member list.

### Proposed Solution

Extend `[link.module]` in `project.toml` with a per-dependency include table:

```toml
[link.module]
name    = "HTTPD"
entry   = "@@CRT0"
options = ["LET", "LIST", "XREF", "RENT"]

# Own members to explicitly pull in (always needed)
include = ["@@CRT1", "HTTPD", "HTTPSTRT"]

# Per-dependency explicit member lists (for non-autocall deps)
# Keys match the [dependencies] keys.
[link.module.dep_includes]
"mvslovers/lua370" = ["LAPI", "LAUXLIB", "LCODE", "LCOROLIB", "LDBLIB",
                      "LDEBUG", "LDO", "LDUMP", "LFUNC", "LGC", "LINIT",
                      "LIOLIB", "LLEX", "LMATHLIB", "LMEM", "LOADLIB",
                      "LOBJECT", "LOPCODES", "LOSLIB", "LPARSER", "LSTATE",
                      "LSTRING", "LSTRLIB", "LTABLE", "LTABLIB", "LTM",
                      "LUNDUMP", "LUTF8LIB", "LVM", "LZIO"]
```

`mvslink.py` merges `include` and all `dep_includes` values into one ordered
list of `INCLUDE SYSLIB(member)` statements. The DD reference is always
`SYSLIB` — all NCaLIBs (project + deps) are concatenated there, so IEWL
finds each member in whichever NCALIB it resides.

**JCL result (unchanged structure):**
```jcl
//SYSLIB   DD DSN=IBMUSER.HTTPD.B42.NCALIB,DISP=SHR
//         DD DSN=MBTDEPS.UFS370.V1R0M0.NCALIB,DISP=SHR
//         DD DSN=MBTDEPS.LUA370.V1R0M0.NCALIB,DISP=SHR
//         DD DSN=MBTDEPS.CRENT370.V1R0M0.NCALIB,DISP=SHR
...
 INCLUDE SYSLIB(@@CRT1)
 INCLUDE SYSLIB(HTTPD)
 INCLUDE SYSLIB(HTTPSTRT)
 INCLUDE SYSLIB(LAPI)
 INCLUDE SYSLIB(LAUXLIB)
 ... (all 31 lua members)
 ENTRY @@CRT0
 NAME HTTPD(R)
```

Deps that follow one-function-per-member (e.g. ufs370, mqtt370) need no
entry in `dep_includes` — IEWL resolves them via autocall from SYSLIB.

### Changes Required

1. **`project.py`**: Parse `[link.module.dep_includes]` into `LinkModule`.
2. **`mvslink.py`**: Merge `mod.include` + `mod.dep_includes` values into the
   INCLUDE statement list. Validate that dep keys in `dep_includes` exist in
   `[dependencies]`.
3. **Spec / README**: Document the new `dep_includes` field.

### Open Questions

- Order of dep_includes vs include in the final INCLUDE list?
  Proposed: `include` first (own members), then `dep_includes` in dependency
  declaration order.
- Should mbt validate that dep keys in `dep_includes` are declared in
  `[dependencies]`? Yes — fail with a clear error if not.
- What about `make link` in CI for incremental PR builds? Currently skipped.
  No change needed here.
