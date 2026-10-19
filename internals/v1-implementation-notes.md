# mbt v1/v2 Python implementation notes

These sections stood in `CLAUDE.md` until 2026-10-19. They describe the
Python implementation as it was planned against `mvs-build-spec-v1.0.0.md`
and `mbt-implementation-guide.md` (milestones 1-3): the remote mvsMF/JCL
build that now lives in `mk/legacy/` and `scripts/legacy/`, and the shared
Python package `scripts/mbt/` that mbt 2 still uses. Paths are given as
they were planned; the package now sits at `scripts/mbt/`, and several of
the listed scripts moved to `scripts/legacy/`.

---

## Allowed Python stdlib modules (as planned)

`tomllib`, `urllib.request`, `urllib.error`, `string`, `json`, `pathlib`,
`re`, `hashlib`, `base64`, `dataclasses`, `argparse`, `time`, `os`,
`tarfile`, `zipfile`, `shutil`, `subprocess`

(As of 2026-10-19 the scripts also import `sys`, `glob`, `http.client`,
`shlex`, `math`, `datetime` and `urllib.parse` -- all stdlib. The rule that
holds is "stdlib only", see `CLAUDE.md`.)

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

## Log Files

Job failure logs go to `.mbt/logs/{module}-{context}-{jobid}.log`
(e.g. `asm-HTTPD-JOB00456.log`).

## Dataset Naming

All MVS dataset names: uppercase, max 44 chars total, qualifiers max 8 chars.

```
Build:      {HLQ}.{PROJECT}.{VRM}.{SUFFIX}
CI Build:   {HLQ}.{PROJECT}.B{BUILD_ID}.{SUFFIX}
Dependency: {DEPS_HLQ}.{DEP_NAME}.{DEP_VRM}.{SUFFIX}
Install:    {HLQ}.{name}  (fixed)  or  {HLQ}.{PROJECT}.{VRM}.{SUFFIX}  (vrm)
```

## SYSLIB Concatenation Order (fixed, first-match wins in IFOX00)

```
1. Project's own MACLIB
2. Dependency MACLIBs (in declaration order from [dependencies])
3. System MACLIBs (from [system.maclibs] in config)
```

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
