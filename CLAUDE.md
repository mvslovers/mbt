# mbt — Project-Specific Context

This file extends `~/repos/CLAUDE.md` (root context). Read the root first.
Everything there applies here. This file adds mbt-specific details only.

---

## What Is mbt?

mbt (MVS Build Tool) is a Git submodule that replaces the current per-project
build scripts (`mvsasm`, `mvslink`, etc.) and the `contrib/*_sdk` dependency
approach. It implements:

- Unified build pipeline (cross-compile → assemble → link → install)
- Package manager (versioned GitHub Releases as registry)
- Dataset provisioning (allocate, upload, manage MVS datasets)
- Release automation (tag → CI → GitHub Release)

mbt is **Roadmap items #1, #2, #3, and #6** from the root CLAUDE.md — the
unified scripts, standard project structure, package manager, and JCL template
system — all in one coherent package.

---

## Key Documents

Read these before making any changes:

| Document | Location | Purpose |
|----------|----------|---------|
| **Specification** | `docs/mvs-build-spec-v1.0.0.md` | Normative. Defines what mbt does. All behavior must conform. |
| **Implementation Guide** | `docs/mbt-implementation-guide.md` | Blueprint. Defines how and in what order to implement. |

**The spec is authoritative.** If this file and the spec disagree, the spec wins.

---

## Tech Stack

- **Python 3.12+** — stdlib only, **zero external dependencies**
- **GNU Make** — orchestration layer
- **mvsMF REST API** — mainframe communication (z/OSMF-compatible endpoints)
- **string.Template** — JCL template engine (not Jinja2, not m4)
- **GitHub Releases** — package registry

### Allowed Python stdlib modules

`tomllib`, `urllib.request`, `urllib.error`, `string`, `json`, `pathlib`,
`re`, `hashlib`, `base64`, `dataclasses`, `argparse`, `time`, `os`,
`tarfile`, `shutil`, `subprocess` (only for git commands in mvsrelease.py)

**No pip install. No requirements.txt. No venv.** If you find yourself
reaching for an external package, stop and find a stdlib solution.

---

## Module Dependency Order

Implement strictly in this order. Each module depends only on those above it.
The Implementation Guide has full signatures (class/method/parameter/return
types) for every module — read them before implementing.

```
 1. mbt/version.py          Semver parsing + MVS VRM conversion
 2. mbt/project.py          project.toml parser + validator
 3. mbt/config.py           Config merge (env → .env → global → defaults)
 4. mbt/datasets.py         Dataset name computation
 5. mbt/lockfile.py          Lockfile read/write
 6. mbt/output.py            Output formatters (shell, json, doctor)
 7. scripts/mbtconfig.py    CLI: config query for Make integration
 8. scripts/mbtdoctor.py    CLI: environment verification
 9. mbt/mvsmf.py             mvsMF REST client (urllib only)
10. mbt/jcl.py               JCL template rendering (string.Template)
11. mbt/dependencies.py     GitHub Releases resolution + cache
12. scripts/mbtbootstrap.py CLI: dependency resolution + provisioning
13. scripts/mvsasm.py       Executor: assemble on MVS
14. scripts/mvslink.py      Executor: linkedit on MVS
15. scripts/mvsinstall.py   Executor: install (copy build → install ds)
16. scripts/mvspackage.py   Executor: create release artifacts
17. scripts/mbtgraph.py     CLI: dependency tree display
18. scripts/mbtdatasets.py  CLI: dataset listing + management
19. scripts/mvsrelease.py   Executor: version bump + tag + push
20. mk/*.mk                  Make includes (core, targets, rules, defaults)
21. .github/workflows/       CI/CD shared workflows
```

---

## Critical Constraints (mbt-specific)

These are in addition to the root CLAUDE.md constraints.

### Exit Codes (normative — spec section 11.1)

```
0   success
1   build failure (assembly RC > max_rc, link error)
2   configuration error (missing field, invalid TOML)
3   dependency error (resolution failed, download failed)
4   mainframe communication error (mvsMF unreachable)
5   dataset error (allocation failed, not found)
99  internal error (unexpected exception)
```

### Log Format (normative — spec section 11.2)

```
[mbt]      Informational message
[mbt]      WARNING: Non-fatal issue
[mbt]      ERROR: Fatal issue
[mvsasm]   Assembling HTTPD...
[mvsasm]   HTTPD assembled (RC=0)
[mvsasm]   ERROR: HTTPSRV failed (RC=8, max_rc=4)
```

### Log Files

Job failure logs go to `.mbt/logs/{module}-{context}-{jobid}.log`
(e.g. `asm-HTTPD-JOB00456.log`).

### Dataset Naming

All MVS dataset names: uppercase, max 44 chars total, qualifiers max 8 chars.

```
Build:      {HLQ}.{PROJECT}.{VRM}.{SUFFIX}
CI Build:   {HLQ}.{PROJECT}.B{BUILD_ID}.{SUFFIX}
Dependency: {DEPS_HLQ}.{DEP_NAME}.{DEP_VRM}.{SUFFIX}
Install:    {HLQ}.{name}  (fixed)  or  {HLQ}.{PROJECT}.{VRM}.{SUFFIX}  (vrm)
```

### SYSLIB Concatenation Order (fixed, first-match wins in IFOX00)

```
1. Project's own MACLIB
2. Dependency MACLIBs (in declaration order from [dependencies])
3. System MACLIBs (from [system.maclibs] in config)
```

---

## Configuration Hierarchy

```
Priority (highest → lowest):
1. Environment variables (MBT_*)
2. Project-local .env
3. Global ~/.mbt/config.toml
4. Built-in defaults
```

This replaces the per-project `.env`-only approach from the root CLAUDE.md.
The `.env` file is still supported but is now one layer in a four-layer stack.

---

## project.toml (not .env)

mbt uses `project.toml` as the single project definition file — not the
`.env` approach from the root CLAUDE.md. The `.env` file is now only for
local overrides (MVS connection, HLQ), not for project structure.

See spec section 3 for the complete schema.

---

## Testing

- **Unit tests:** `python -m unittest discover tests/` — no MVS required
- **Integration tests:** `tests/integration/` — requires MVS/CE via Docker
- **Reference project:** `examples/hello370/` — always build this first

Start MVS/CE for integration tests: `make run-mvs`

---

## Notes for Implementation

### Make Integration Pattern

Make calls Python once per target, not per file:

```makefile
BUILD_VARS := $(shell python3 $(MBT_SCRIPTS)/mbtconfig.py \
    --project project.toml --output shell)
$(eval $(BUILD_VARS))
```

All variables are resolved in a single Python invocation. Executors
(mvsasm, mvslink, etc.) read project.toml directly — they don't depend
on Make variables.

### mvsMF Client Pattern

All MVS communication goes through `mbt.mvsmf.MvsMFClient`. No direct
`urllib` calls in executor scripts. The client uses progressive backoff
for job polling (1s → 2s → 3s → 5s).

### JCL Template Pattern

Templates are `.tpl` files in `templates/jcl/` using `$VARIABLE` syntax.
Dynamic sections (SYSLIB concatenation) are pre-rendered by helper
functions in `mbt/jcl.py` and passed as a single `$SYSLIB_CONCAT` variable.

### Version Comparison

Prerelease ordering follows semver: `1.0.0-dev < 1.0.0-rc1 < 1.0.0`.
The dependency resolver must respect this.
