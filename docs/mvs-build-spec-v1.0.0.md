# MVS Build & Package Specification

**Version:** 1.0.0
**Status:** Final
**Phase:** 1 (Make + Python Executors + mvsMF REST API)
**Date:** 2026-03-04

---

## Revision History

| Version   | Date       | Status | Changes                                   |
|-----------|------------|--------|-------------------------------------------|
| 0.1.0     | 2026-03-03 | Draft  | Initial draft (stoml-based, flat schema)  |
| 0.2.0     | 2026-03-04 | Draft  | Rewrite: structured TOML, split files,    |
|           |            |        | Zowe SDK, Jinja2 templates                |
| 0.3.0     | 2026-03-04 | Draft  | Single project.toml, zero ext. deps,      |
|           |            |        | deps_hlq, max_rc, local_dir, mbt commands |
| **1.0.0** | 2026-03-04 | Final  | Resolved all open points, added mvsMF     |
|           |            |        | API contract, error handling, logging      |

---

## 1. Overview

This document defines the normative specification for the MVSLOVERS
unified build and packaging system, referred to as **mbt** (MVS Build Tool).

mbt is designed as a reusable Git submodule that centralizes build logic,
dependency management, dataset provisioning, and release packaging for all
MVSLOVERS projects targeting MVS 3.8j.

Phase 1 is based on:

- GNU Make (or compatible)
- Python 3.12+ (stdlib only — no external dependencies)
- mvsMF REST API (z/OSMF-compatible, mainframe communication)
- GitHub Releases (package registry)

### 1.1 Design Principles

1. **Single file configuration** — one `project.toml` per project
2. **Convention over configuration** — sensible defaults, override when needed
3. **Zero external Python dependencies** — stdlib only (tomllib, urllib, string)
4. **MVS 3.8j compatibility** — PDS only, no PDSE, no SMS
5. **Extensibility** — new executors, templates, and targets without core changes

### 1.2 Normative Keywords

- **MUST** / **SHALL** — absolute requirement
- **MUST NOT** / **SHALL NOT** — absolute prohibition
- **MAY** — optional behavior
- **SHOULD** — recommended behavior

### 1.3 Target Platform

The target mainframe environment is **MVS 3.8j** (Hercules / MVS/CE):

- PDS only (no PDSE, no SMS, no DSNTYPE)
- IFOX00 assembler
- IEWL linkage editor
- TSO TRANSMIT/RECEIVE for XMIT files
- IKJEFT01 for TSO-in-Batch operations

### 1.4 Scope

mbt covers the build lifecycle for projects that produce MVS artifacts
(runtimes, libraries, modules, applications). Host-side tools like
c2asm370 are out of scope and maintain their own build systems.

### 1.5 Prerequisites

The cross-compiler `c2asm370` MUST be installed on the build host and
available on `$PATH`. mbt does not manage its installation or version.

---

## 2. Architecture

### 2.1 Layered Design

```
┌─────────────────────────────────────────────────────┐
│  Layer 3: CI/CD (GitHub Actions Shared Workflows)   │
├─────────────────────────────────────────────────────┤
│  Layer 2: Project Definition (project.toml)         │
├─────────────────────────────────────────────────────┤
│  Layer 1: Build Core (mbt Git Submodule)            │
│           Make includes / Python scripts /           │
│           JCL templates / mvsMF client               │
├─────────────────────────────────────────────────────┤
│  Layer 0: Configuration                             │
│           ~/.mbt/config.toml / .env / ENV vars      │
└─────────────────────────────────────────────────────┘
```

### 2.2 File Overview

| File / Directory         | Purpose                              | Committed? |
|--------------------------|--------------------------------------|------------|
| `project.toml`           | Complete project definition          | Yes        |
| `.mbt/mvs.lock`          | Pinned dependency versions           | Yes        |
| `.mbt/logs/`             | Build logs and JES output            | No         |
| `mbt/`                   | Build system (git submodule)         | Yes        |
| `contrib/`               | Downloaded dependency headers        | No         |
| `.env`                   | Local config overrides               | No         |
| `~/.mbt/config.toml`     | Global user configuration            | No         |
| `~/.mbt/cache/`          | Downloaded dependency artifacts      | No         |

---

## 3. Project Definition (project.toml)

Each project MUST provide a single file named `project.toml` in its
root directory. This file contains all project metadata, build
configuration, dependencies, link settings, install targets, and
release configuration.

### 3.1 [project]

```toml
[project]
name    = "httpd"                    # string, required
version = "3.3.1-dev"                # string, semver, required
type    = "application"              # required, see 3.1.1
```

#### 3.1.1 Project Types

| Type          | Description                        | Produces             |
|---------------|------------------------------------|-----------------------|
| `runtime`     | Shared runtime (e.g. crent370)     | MACLIB + NCALIB       |
| `library`     | Reusable library                   | NCALIB                |
| `module`      | Loadable module (e.g. CGI)         | SYSLMOD               |
| `application` | Standalone application             | SYSLMOD + bundle      |

### 3.2 [build]

```toml
[build]
cflags = ["-fverbose-asm"]           # optional, extends core defaults
```

Project `cflags` MUST extend, not replace, the core default flags.

Core default flags:

```
-S -O1
```

#### 3.2.1 [build.sources] (optional)

Source directories follow convention-based defaults. This section is
only needed when overriding the defaults.

| Field      | Type       | Default      | Description                        |
|------------|------------|--------------|------------------------------------|
| `c_dirs`   | string[]   | `["src/"]`   | Directories with C sources (.c)    |
| `asm_dirs` | string[]   | `[]`         | Directories with ASM sources (.s)  |

**Default behavior** (no `[build.sources]` section):

- `src/` — C sources, cross-compiled via c2asm370
- No native assembler sources (most projects don't have any)

**Override example** (httpd with additional module and native ASM):

```toml
[build.sources]
c_dirs   = ["src/", "credentials/"]
asm_dirs = ["asm/"]
```

Projects with native assembler sources MUST explicitly set `asm_dirs`.

The build process distinguishes the two source types:

```
c_dirs:    *.c  →  c2asm370 (local)  →  generated .s  →  mvsasm  →  IFOX00
asm_dirs:  *.s / *.asm               →  mvsasm direct  →  IFOX00
```

Both paths converge at the same assembler step on the mainframe.

### 3.3 [dependencies]

```toml
[dependencies]
"mvslovers/crent370" = ">=1.0.0"
"mvslovers/ufs370"   = ">=1.0.0"
```

Format: `"<owner>/<repo>" = "<semver-constraint>"`

Supported constraint operators:

| Operator | Meaning                         | Example              |
|----------|----------------------------------|-----------------------|
| `>=`     | Greater than or equal (literal)  | `>=1.0.0`            |
| `<`      | Less than                        | `<2.0.0`             |
| `=`      | Exact match                      | `=1.0.0`             |

Operators MAY be combined with comma separation: `">=1.0.0,<2.0.0"`

The `>=` operator is interpreted literally — `>=1.0.0` includes `2.0.0`,
`3.0.0`, etc. There is no implicit caret or tilde semantics.

In Phase 1, only direct dependencies are resolved. Each project MUST
declare all dependencies it requires, including transitive ones.

### 3.4 [mvs.asm]

```toml
[mvs.asm]
max_rc = 4                           # optional, default: 4
```

Controls the maximum acceptable return code from the assembler. Any
RC above this value causes the build to fail. Default is 4 (warnings
accepted, errors fail).

Some projects MAY set `max_rc = 8` for sources that produce tolerated
warnings at RC=8 level.

### 3.5 [mvs.build.datasets.*]

```toml
[mvs.build.datasets.ncalib]
suffix    = "NCALIB"                 # required
dsorg     = "PO"                     # required: "PO" or "PS"
recfm     = "FB"                     # required
lrecl     = 80                       # required
blksize   = 3120                     # required
space     = ["TRK", 10, 5, 10]      # required, see 3.5.1
unit      = "SYSDA"                  # optional, default: "SYSDA"
volume    = "PUB001"                 # optional, default: none
local_dir = "maclib/"                # optional, see 3.5.2
```

A project MAY define any number of build datasets. Common dataset
keys are: `maclib`, `punch`, `ncalib`, `syslmod`.

#### 3.5.1 Space Array

The `space` field maps directly to JCL SPACE parameter semantics.

For `dsorg = "PO"` (partitioned datasets):

```toml
space = ["TRK", primary, secondary, dirblks]
```

MUST have exactly 4 elements.

For `dsorg = "PS"` (sequential datasets):

```toml
space = ["TRK", primary, secondary]
```

MUST have exactly 3 elements.

Valid allocation units: `"TRK"` (tracks) or `"CYL"` (cylinders).

#### 3.5.2 local_dir (Dataset Provisioning)

When `local_dir` is specified, the bootstrap process MUST upload all
files from this local directory as members into the dataset. This is
used for assets that must exist on the mainframe before assembly
(e.g. project-local macros for MACLIB).

Files in `local_dir` are mainframe assets provisioned during bootstrap,
not build sources.

#### 3.5.3 Dataset Field Reference

| Field       | Type     | Required | Default    | Description                 |
|-------------|----------|----------|------------|-----------------------------|
| `suffix`    | string   | Yes      | —          | Dataset name suffix         |
| `dsorg`     | string   | Yes      | —          | "PO" or "PS"                |
| `recfm`     | string   | Yes      | —          | Record format (FB, VB, U)   |
| `lrecl`     | int      | Yes      | —          | Logical record length       |
| `blksize`   | int      | Yes      | —          | Block size                  |
| `space`     | array    | Yes      | —          | See 3.5.1                   |
| `unit`      | string   | No       | "SYSDA"    | Device type                 |
| `volume`    | string   | No       | (none)     | Volume serial               |
| `local_dir` | string   | No       | (none)     | Local dir to upload         |

### 3.6 [mvs.install]

```toml
[mvs.install]
naming = "fixed"                     # "fixed" or "vrm"

[mvs.install.datasets.ncalib]
name = "HTTPD.NCALIB"

[mvs.install.datasets.syslmod]
name = "HTTPD.LOAD"
```

When `naming = "fixed"`, the `name` field specifies the dataset name
(HLQ prepended from config): `IBMUSER.HTTPD.LOAD`.

When `naming = "vrm"`, dataset names follow the build naming convention
with version component: `IBMUSER.HTTPD.V3R3M1.LOAD`. The `name` field
is ignored.

DCB attributes for install datasets are inherited from the corresponding
build dataset (matched by key name).

For project types without install targets, this section MAY be omitted.

### 3.7 [link]

Projects of type `application` or `module` SHOULD include a `[link]`
section. Projects of type `runtime` or `library` MUST NOT.

```toml
[[link.module]]
name    = "HTTPD"                    # load module name
entry   = "HTTPD"                    # entry point
options = ["RENT", "REUS"]           # IEWL options
include = ["HTTPD", "HTTPSRV"]       # NCALIB members to include
```

Multiple `[[link.module]]` entries are supported.

### 3.8 [artifacts]

```toml
[artifacts]
headers        = true                # produce headers tarball
mvs            = true                # produce MVS dataset tarball
package_bundle = false               # produce full install bundle
```

### 3.9 [system]

Optional. Declares additional system macro libraries to append to the
SYSLIB concatenation after the built-in defaults (`SYS1.MACLIB`,
`SYS1.AMODGEN`).

```toml
[system]
maclibs = ["SYS2.MACLIB"]
```

`SYS1.MACLIB` and `SYS1.AMODGEN` are always present and MUST NOT be
listed here — they are unconditionally included by mbt. Only use this
section for additional libraries required by a specific project (e.g.
a private macro library or a second IBM-supplied library).

### 3.10 [release]

```toml
[release]
github = "mvslovers/httpd"
version_files = [
    "project.toml",
    "src/version.h",
]
```

`make release` (local) MUST only:

1. Update version strings in `version_files`
2. Create Git commit
3. Create Git tag (`v{version}`)
4. Push commit and tag to remote

The CI release pipeline handles the actual build, package, and
GitHub Release creation. This ensures all releases are built from
clean, reproducible CI environments.

---

## 4. Configuration Hierarchy

### 4.1 Priority Order

```
1. Environment variables    (MBT_*)       — highest priority
2. Project-local .env       (.env)
3. Global config            (~/.mbt/config.toml)
4. Built-in defaults                      — lowest priority
```

### 4.2 Environment Variable Mapping

| Config Path     | Environment Variable  | Default      |
|-----------------|-----------------------|--------------|
| `mvs.host`      | `MBT_MVS_HOST`       | `localhost`  |
| `mvs.port`      | `MBT_MVS_PORT`       | `1080`       |
| `mvs.user`      | `MBT_MVS_USER`       | `IBMUSER`    |
| `mvs.pass`      | `MBT_MVS_PASS`       | —            |
| `mvs.hlq`       | `MBT_MVS_HLQ`        | `IBMUSER`    |
| `mvs.deps_hlq`  | `MBT_MVS_DEPS_HLQ`   | `{HLQ}.DEPS` |
| `jes.jobclass`  | `MBT_JES_JOBCLASS`   | `A`          |
| `jes.msgclass`  | `MBT_JES_MSGCLASS`   | `H`          |
| `build.id`      | `MBT_BUILD_ID`       | (none)       |

### 4.3 Global Config (~/.mbt/config.toml)

```toml
[mvs]
host     = "localhost"
port     = 1080
user     = "IBMUSER"
password = "sys1"
hlq      = "IBMUSER"
deps_hlq = "IBMUSER.DEPS"

[jes]
jobclass = "A"
msgclass = "H"
```

This file MUST NOT be committed to any repository.

Only MVS connection and JES parameters belong here. System macro library
configuration belongs in `project.toml` (see section 3.x).

#### 4.3.1 deps_hlq

Controls the HLQ prefix for dependency datasets on the mainframe.
Default: `{HLQ}.DEPS`.

```toml
# Per-user (default):
deps_hlq = "IBMUSER.DEPS"
# → IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB

# Shared across users:
deps_hlq = "SHARED.DEPS"
# → SHARED.DEPS.CRENT370.V1R0M0.MACLIB
```

Setting a common `deps_hlq` on shared mainframes prevents duplicate
dependency installations.

### 4.4 CI/CD Configuration

In CI/CD environments, no `~/.mbt/config.toml` is required:

```yaml
env:
  MBT_MVS_HOST:     ${{ secrets.MVS_HOST }}
  MBT_MVS_PORT:     ${{ secrets.MVS_PORT }}
  MBT_MVS_USER:     ${{ secrets.MVS_USER }}
  MBT_MVS_PASS:     ${{ secrets.MVS_PASSWORD }}
  MBT_MVS_HLQ:      CIUSER
  MBT_MVS_DEPS_HLQ: CIUSER.DEPS
```

### 4.5 Diagnostic Output

`mbt doctor` MUST display resolved configuration with source attribution:

```
[mbt] Configuration:
  MVS_HOST     = mvs-ci.internal     [env]
  MVS_PORT     = 1080                [env]
  MVS_HLQ      = CIUSER             [env]
  DEPS_HLQ     = CIUSER.DEPS        [env]
  JES_JOBCLASS = A                   [default]
  JES_MSGCLASS = H                   [~/.mbt/config.toml]
```

---

## 5. Dataset Naming Rules

### 5.1 Build Dataset Names

```
{HLQ}.{PROJECT}.{VRM}.{SUFFIX}
```

| Component   | Source                           | Example         |
|-------------|----------------------------------|-----------------|
| `HLQ`       | Config: mvs.hlq                  | `IBMUSER`       |
| `PROJECT`   | project.name (uppercased)        | `HTTPD`         |
| `VRM`       | version → VRM conversion         | `V3R3M1`        |
| `SUFFIX`    | dataset suffix from project.toml | `NCALIB`        |

Result: `IBMUSER.HTTPD.V3R3M1.NCALIB`

### 5.2 VRM Conversion

| Semver Input  | VRM Output  | Notes               |
|---------------|-------------|----------------------|
| `1.0.0`       | `V1R0M0`    | Release              |
| `3.3.1`       | `V3R3M1`    | Release              |
| `1.0.0-dev`   | `V1R0M0D`   | Development          |
| `3.3.1-rc1`   | `V3R3M1R1`  | Release candidate    |

Prerelease versions are ordered per semver convention:
`1.0.0-dev < 1.0.0-rc1 < 1.0.0`. The dependency resolver MUST
respect this ordering when selecting the highest matching version.

### 5.3 CI Build Dataset Names

When `MBT_BUILD_ID` is set, build dataset names use the build ID
instead of VRM:

```
{HLQ}.{PROJECT}.B{BUILD_ID}.{SUFFIX}
→ CIUSER.HTTPD.B42.NCALIB
```

Applies only to build datasets. Dependency datasets always use VRM.

### 5.4 Dependency Dataset Names

```
{DEPS_HLQ}.{DEP_NAME}.{DEP_VRM}.{SUFFIX}
→ IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB
```

### 5.5 Install Dataset Names

| `naming` value | Pattern                            | Example                      |
|----------------|------------------------------------|------------------------------|
| `fixed`        | `{HLQ}.{name}`                    | `IBMUSER.HTTPD.LOAD`        |
| `vrm`          | `{HLQ}.{PROJECT}.{VRM}.{SUFFIX}` | `IBMUSER.HTTPD.V3R3M1.LOAD` |

---

## 6. Dependency Resolution

### 6.1 Registry

GitHub Releases SHALL be the only package registry in Phase 1.

### 6.2 Resolution Process

```
1. Read [dependencies] from project.toml
2. If .mbt/mvs.lock exists:
   → Use exact versions from lockfile
3. If no lockfile (or --update flag):
   → Query GitHub Releases API per dependency
   → Select highest version satisfying constraint
   → Verify release contains: package.toml + required assets
   → Write .mbt/mvs.lock
4. Check local cache (~/.mbt/cache/{owner}/{repo}/{version}/)
   → HIT: skip download
   → MISS: download, store in cache
5. Extract into project:
   → Headers → contrib/{dep}-{version}/include/
   → MVS artifacts → staged for mainframe deploy
```

### 6.3 Lockfile

Written to `.mbt/mvs.lock`. MUST be committed for reproducible builds.

```toml
# .mbt/mvs.lock
# AUTO-GENERATED by mbt bootstrap — DO NOT EDIT

[metadata]
generated   = "2026-03-04T10:30:00Z"
mbt_version = "1.0.0"

[dependencies]
"mvslovers/crent370" = "1.0.0"
"mvslovers/ufs370"   = "1.0.0"
```

Force re-resolution: `make bootstrap ARGS="--update"`

### 6.4 Local Cache

```
~/.mbt/cache/{owner}/{repo}/{version}/
  package.toml
  {name}-{version}-headers.tar.gz
  {name}-{version}-mvs.tar.gz
```

Cache cleanup: `mbt cache clean`

### 6.5 Header Integration

Bootstrap extracts to `contrib/{dep}-{version}/include/`.

`mbtconfig` produces:

```
INCLUDES=-Icontrib/crent370-1.0.0/include -Icontrib/ufs370-1.0.0/include
```

---

## 7. Package Specification (package.toml)

Auto-generated by `make package`. MUST be included in every
GitHub Release.

### 7.1 Schema

```toml
[package]
name    = "crent370"
version = "1.0.0"
type    = "runtime"
mbt     = "1.0.0"

[package.dependencies]
# "mvslovers/lua370" = "1.0.0"

[artifacts]
headers = "crent370-1.0.0-headers.tar.gz"
mvs     = "crent370-1.0.0-mvs.tar.gz"

[mvs.provides.datasets.maclib]
suffix    = "MACLIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[mvs.provides.datasets.ncalib]
suffix    = "NCALIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]
```

### 7.2 Required Release Assets

| Asset                              | When Required                      |
|------------------------------------|------------------------------------|
| `package.toml`                     | Always                             |
| `{name}-{version}-headers.tar.gz` | `artifacts.headers = true`         |
| `{name}-{version}-mvs.tar.gz`     | `artifacts.mvs = true`             |
| `{name}-{version}-bundle.tar.gz`  | `artifacts.package_bundle = true`  |

### 7.3 Asset Content Structure

**Headers tarball:**

```
{name}-{version}/include/
```

**MVS tarball:**

```
{name}-{version}/mvs/
  maclib.xmit
  ncalib.xmit
```

**Bundle tarball** (applications only):

```
{name}-{version}/
  mvs/           (XMIT files)
  jobs/           (install JCL)
  content/        (static files, CGI modules)
```

### 7.4 Artifacts by Project Type

| Type          | headers | MACLIB | NCALIB | SYSLMOD | bundle |
|---------------|---------|--------|--------|---------|--------|
| `runtime`     | Yes     | Yes    | Yes    | No      | No     |
| `library`     | Yes     | No     | Yes    | No      | No     |
| `module`      | No      | No     | No     | Yes     | No     |
| `application` | Yes     | No     | Yes    | Yes     | Yes    |

---

## 8. Build Lifecycle

### 8.1 Standard Targets

| Target       | Command              | Description                                |
|--------------|----------------------|--------------------------------------------|
| `doctor`     | `make doctor`        | Verify build environment                   |
| `bootstrap`  | `make bootstrap`     | Resolve deps, download, provision datasets |
| `build`      | `make` / `make build`| Compile + assemble + NCAL link             |
| `link`       | `make link`          | Full linkedit (applications/modules)       |
| `install`    | `make install`       | Copy build → install datasets              |
| `package`    | `make package`       | Create tarballs and package.toml           |
| `release`    | `make release`       | Update version, tag, push (CI does rest)   |
| `clean`      | `make clean`         | Delete members + temp files                |
| `distclean`  | `make distclean`     | Delete build datasets + cache + lockfile   |

### 8.2 Bootstrap Behavior

Steps in order:

1. Validate project.toml
2. Resolve dependencies (lockfile or fresh)
3. Download dependency assets (cache-aware)
4. Extract headers → `contrib/{dep}-{version}/include/`
5. Upload XMIT files to mainframe (via mvsMF)
6. RECEIVE XMIT files (TSO-in-Batch via IKJEFT01)
7. Allocate project build datasets (skip if existing, warn)
8. Upload `local_dir` contents to corresponding datasets

### 8.3 Build Behavior

```
c_dirs:    *.c  →  c2asm370 (local)  →  .s  →  mvsasm  →  IFOX00  →  NCAL
asm_dirs:  *.s / *.asm               →  mvsasm  →  IFOX00  →  NCAL
```

`mvsasm` submits JCL via mvsMF REST API. Each job includes a SYSLIB
concatenation constructed in the following **fixed order**:

1. Project's own MACLIB (if defined)
2. Dependency MACLIBs — in declaration order from `[dependencies]`
3. `SYS1.MACLIB` — always present, unconditional
4. `SYS1.AMODGEN` — always present, unconditional
5. Additional system MACLIBs — from `[system] maclibs` in `project.toml` (optional)

This order ensures project macros can override dependency macros,
and dependency macros can override system macros (first-match wins
in IFOX00 SYSLIB resolution). `SYS1.MACLIB` and `SYS1.AMODGEN` are
never configured — they are always appended automatically.

Build failure is determined by `max_rc` (default: 4). Any assembly
returning RC > `max_rc` is a build failure.

### 8.4 Link Behavior

Reads `[[link.module]]` from project.toml. Submits linkedit JCL for
each module.

For project types without `[link]`: prints info message, exits RC=0.

### 8.5 Install Behavior

Reads `[mvs.install]`. Copies from build to install datasets via
IEBCOPY (replace mode).

For project types without `[mvs.install]`: prints info message, exits RC=0.

### 8.6 Release Behavior (Local)

`make release VERSION=x.y.z` MUST:

1. Update version strings in all `version_files`
2. `git add` the changed files
3. `git commit -m "Release vx.y.z"`
4. `git tag vx.y.z`
5. `git push origin main --tags`

The CI release pipeline (triggered by tag) handles: distclean →
bootstrap → build → link → package → GitHub Release creation.

This separation ensures reproducible releases from clean CI environments.

### 8.7 Clean Behavior

`make clean` MUST:

- Delete all members in build datasets
- Delete local temporary files (generated .s, .mbt/logs/)

MUST NOT delete: dependency datasets, contrib/, lockfile.

### 8.8 Distclean Behavior

`make distclean` MUST:

- Delete all build datasets on the mainframe
- Delete `contrib/` and `.mbt/` (including lockfile)

MUST NOT delete: dependency datasets, install datasets,
`~/.mbt/cache/`, `~/.mbt/config.toml`.

---

## 9. JCL Template System

### 9.1 Template Engine

JCL templates use Python `string.Template` syntax (`$variable`) and
are stored in `mbt/templates/jcl/`.

For SYSLIB DD concatenation (variable number of DDs), a dedicated
Python helper function generates the JCL fragment, inserted as a
pre-rendered `$SYSLIB_CONCAT` variable.

```python
def render_syslib_concat(datasets):
    if not datasets:
        return ""
    lines = [f"//SYSLIB   DD DSN={datasets[0]},DISP=SHR"]
    for dsn in datasets[1:]:
        lines.append(f"//         DD DSN={dsn},DISP=SHR")
    return "\n".join(lines)
```

Zero external dependencies.

### 9.2 Template Inventory

| Template           | Used by        | Purpose                          |
|--------------------|----------------|----------------------------------|
| `alloc.jcl.tpl`    | mbtbootstrap   | Allocate new datasets            |
| `asm.jcl.tpl`      | mvsasm         | Assemble source on MVS           |
| `ncallink.jcl.tpl` | mvsasm         | NCAL linkedit after assembly     |
| `link.jcl.tpl`     | mvslink        | Full linkedit for load modules   |
| `copy.jcl.tpl`     | mvsinstall     | IEBCOPY for install              |
| `delete.jcl.tpl`   | clean/distclean| Delete datasets                  |
| `receive.jcl.tpl`  | mbtbootstrap   | TSO RECEIVE for XMIT files       |

### 9.3 Extensibility

New executors follow the pattern:

1. New script: `scripts/mvs<name>.py`
2. New template: `templates/jcl/<name>.jcl.tpl`
3. New make target (in project Makefile or core)

The script imports `mbt.config.MbtConfig` and uses the standard
template rendering pipeline. No core changes required.

---

## 10. Mainframe Communication (mvsMF REST API)

### 10.1 Architecture

```
┌──────────────────────────┐
│  Executor Scripts        │
│  (mvsasm, mvslink, ...)  │
├──────────────────────────┤
│  mbt.mvsmf.MvsMFClient   │
├──────────────────────────┤
│  Python urllib (stdlib)   │
├──────────────────────────┤
│  mvsMF REST API on MVS   │
│  (z/OSMF compatible)     │
└──────────────────────────┘
```

### 10.2 Base URL

```
http://{MVS_HOST}:{MVS_PORT}/zosmf
```

Authentication: HTTP Basic Auth with `MVS_USER` / `MVS_PASS`.

### 10.3 Endpoints Used by mbt

mbt uses the following z/OSMF-compatible REST endpoints provided
by mvsMF:

#### 10.3.1 Jobs REST Interface

**Submit JCL (inline):**

```
PUT /zosmf/restjobs/jobs
Content-Type: text/plain

//JOBNAME JOB ...
(inline JCL)
```

Response:

```json
{
  "jobid": "JOB00123",
  "jobname": "MBTASM",
  "status": "INPUT",
  "retcode": null
}
```

**Get job status:**

```
GET /zosmf/restjobs/jobs/{jobname}/{jobid}
```

Response:

```json
{
  "jobid": "JOB00123",
  "jobname": "MBTASM",
  "status": "OUTPUT",
  "retcode": "CC 0000"
}
```

Status values: `INPUT`, `ACTIVE`, `OUTPUT`

Retcode values: `CC 0000`, `CC 0004`, `CC 0008`, `ABEND S0C4`,
`JCL ERROR`

**List spool files:**

```
GET /zosmf/restjobs/jobs/{jobname}/{jobid}/files
```

Response:

```json
[
  {"id": 1, "ddname": "JESMSGLG", "stepname": "JES2"},
  {"id": 2, "ddname": "JESJCL",   "stepname": "JES2"},
  {"id": 3, "ddname": "SYSPRINT", "stepname": "ASM"}
]
```

**Get spool file content:**

```
GET /zosmf/restjobs/jobs/{jobname}/{jobid}/files/{id}/records
```

Response: plain text spool content.

#### 10.3.2 Dataset REST Interface

**List datasets by prefix:**

```
GET /zosmf/restfiles/ds?dslevel={filter}
```

Example: `GET /zosmf/restfiles/ds?dslevel=IBMUSER.HTTPD.*`

Response:

```json
{
  "items": [
    {"dsname": "IBMUSER.HTTPD.V3R3M1.NCALIB", "dsorg": "PO"},
    {"dsname": "IBMUSER.HTTPD.V3R3M1.LOAD",   "dsorg": "PO"}
  ]
}
```

**List PDS members:**

```
GET /zosmf/restfiles/ds/{dsname}/member
```

Response:

```json
{
  "items": [
    {"member": "HTTPD"},
    {"member": "HTTPSRV"}
  ]
}
```

**Write to dataset member:**

```
PUT /zosmf/restfiles/ds/{dsname}({member})
Content-Type: text/plain

(member content)
```

**Read dataset member:**

```
GET /zosmf/restfiles/ds/{dsname}({member})
```

**Create dataset:**

```
POST /zosmf/restfiles/ds/{dsname}
Content-Type: application/json

{
  "dsorg": "PO",
  "alcunit": "TRK",
  "primary": 10,
  "secondary": 5,
  "dirblk": 10,
  "recfm": "FB",
  "blksize": 3120,
  "lrecl": 80,
  "unit": "SYSDA"
}
```

**Delete dataset:**

```
DELETE /zosmf/restfiles/ds/{dsname}
```

**Write binary (XMIT upload):**

```
PUT /zosmf/restfiles/ds/{dsname}
Content-Type: application/octet-stream
X-IBM-Data-Type: binary

(binary XMIT content)
```

### 10.4 MvsMFClient Interface

| Method              | Endpoint Used                      | Purpose                    |
|---------------------|------------------------------------|----------------------------|
| `submit_jcl()`      | PUT /restjobs/jobs                 | Submit JCL, wait for RC    |
| `get_job_status()`  | GET /restjobs/jobs/{name}/{id}     | Poll job status            |
| `get_job_output()`  | GET /restjobs/.../files/.../records| Collect spool output       |

`submit_jcl()` polls with progressive backoff (1s → 2s → 3s → 5s)
to reduce REST load, especially in CI environments.
| `dataset_exists()`  | GET /restfiles/ds?dslevel=         | Check dataset existence    |
| `create_dataset()`  | POST /restfiles/ds/{dsn}           | Create dataset             |
| `delete_dataset()`  | DELETE /restfiles/ds/{dsn}         | Delete dataset             |
| `list_datasets()`   | GET /restfiles/ds?dslevel=         | List by prefix             |
| `list_members()`    | GET /restfiles/ds/{dsn}/member     | List PDS members           |
| `upload_member()`   | PUT /restfiles/ds/{dsn}({mbr})     | Write text member          |
| `download_member()` | GET /restfiles/ds/{dsn}({mbr})     | Read text member           |
| `upload_binary()`   | PUT /restfiles/ds/{dsn} (binary)   | Upload XMIT file           |

### 10.5 Custom Extensions

If mvsMF provides endpoints beyond z/OSMF compatibility, they can be
added to `MvsMFClient` without changing executor scripts. The
abstraction layer isolates consumers from API changes.

---

## 11. Error Handling & Logging

### 11.1 Exit Codes

All mbt scripts and executors MUST use consistent exit codes:

| Code | Meaning                                              |
|------|------------------------------------------------------|
| 0    | Success                                              |
| 1    | Build failure (assembly RC > max_rc, link error)     |
| 2    | Configuration error (missing field, invalid TOML)    |
| 3    | Dependency error (resolution failed, download failed)|
| 4    | Mainframe communication error (mvsMF unreachable)    |
| 5    | Dataset error (allocation failed, not found)         |
| 99   | Internal error (unexpected exception)                |

`make` targets MUST propagate executor exit codes. A non-zero exit
from any executor MUST cause the make target to fail.

### 11.2 Log Format

All mbt output MUST use a consistent prefix format:

```
[mbt] Informational message
[mbt] WARNING: Something unexpected but non-fatal
[mbt] ERROR: Something that causes failure
```

Executor scripts use their own prefix:

```
[mvsasm] Assembling HTTPD...
[mvsasm] HTTPD assembled (RC=0)
[mvsasm] ERROR: HTTPSRV failed (RC=8, max_rc=4)
[mvslink] Linking module HTTPD...
[mvslink] HTTPD linked (RC=0)
```

### 11.3 JES Job Log Capture

When a JCL job fails (RC > max_rc or ABEND), the executor MUST:

1. Capture the complete JES job output (all spool files)
2. Write it to `.mbt/logs/{module}-{context}-{jobid}.log`
   (e.g. `asm-HTTPD-JOB00456.log`, `link-HTTPD-JOB00789.log`)
3. Print the SYSPRINT content to stderr
4. Print a summary line with job name, job ID, and RC

Example output on failure:

```
[mvsasm] ERROR: HTTPSRV failed (RC=8, max_rc=4)
[mvsasm] Job: MBTASM / JOB00456
[mvsasm] Log: .mbt/logs/asm-HTTPSRV-JOB00456.log
[mvsasm] --- SYSPRINT ---
** ASMA044E Undefined symbol - XYZ
** ASMA435I Record 42 in HTTPSRV
[mvsasm] --- END SYSPRINT ---
```

### 11.4 Doctor Diagnostics

`mbt doctor` MUST verify:

1. Python version >= 3.12
2. `c2asm370` on PATH and executable
3. `make` on PATH
4. MVS host reachable (HTTP GET to mvsMF base URL)
5. MVS credentials valid (test authentication)
6. project.toml exists and is valid
7. Config source attribution (see section 4.5)

Exit code 0 if all checks pass, exit code 2 if any fail.

```
[mbt] Doctor:
  mbt          1.0.0           ✓
  Python       3.12.1          ✓
  c2asm370     1.2.0           ✓
  make         GNU Make 4.3    ✓
  MVS host     localhost:1080  ✓ (mvsMF v1.0.0)
  MVS auth     IBMUSER         ✓
  project.toml valid           ✓
  All checks passed.
```

---

## 12. CLI Commands

### 12.1 Standard Commands

| Command               | Description                                 |
|-----------------------|---------------------------------------------|
| `mbt doctor`          | Verify environment and configuration        |
| `mbt bootstrap`       | Resolve dependencies, provision datasets    |
| `mbt graph`           | Display dependency graph                    |
| `mbt datasets`        | Show/manage mainframe datasets              |
| `mbt cache clean`     | Clear the local dependency cache            |

### 12.2 mbt graph

```
$ mbt graph

httpd v3.3.1-dev
 ├─ crent370 v1.0.0
 ├─ ufs370 v1.0.0
 │   └─ crent370 v1.0.0
 ├─ lua370 v1.0.0
 │   └─ crent370 v1.0.0
 └─ mqtt370 v1.0.0
     ├─ crent370 v1.0.0
     └─ lua370 v1.0.0
```

Constructed from lockfile + cached `package.toml` files.

### 12.3 mbt datasets

```
$ mbt datasets

Build datasets:
  IBMUSER.HTTPD.V3R3M1.OBJECT         (exists)
  IBMUSER.HTTPD.V3R3M1.NCALIB         (exists)
  IBMUSER.HTTPD.V3R3M1.LOAD           (missing)

Dependency datasets:
  IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB (exists)
  IBMUSER.DEPS.CRENT370.V1R0M0.NCALIB (exists)

Install datasets:
  IBMUSER.HTTPD.NCALIB                 (exists)
  IBMUSER.HTTPD.LOAD                   (missing)
```

| Flag              | Description                             |
|-------------------|-----------------------------------------|
| `--delete-build`  | Delete all build datasets               |
| `--delete-deps`   | Delete all dependency datasets          |
| `--check`         | Exit non-zero if expected ds missing    |

---

## 13. mbt Submodule Structure

```
mbt/
├── bin/
│   └── mbt                          # CLI entrypoint (shell wrapper)
├── scripts/
│   ├── mbtconfig.py                 # Config query & output
│   ├── mbtbootstrap.py              # Dependency resolution & setup
│   ├── mbtdoctor.py                 # Environment verification
│   ├── mbtgraph.py                  # Dependency graph display
│   ├── mbtdatasets.py               # Dataset listing & management
│   ├── mvsasm.py                    # Executor: assemble
│   ├── mvslink.py                   # Executor: linkedit
│   ├── mvsinstall.py                # Executor: install
│   ├── mvspackage.py                # Executor: package
│   ├── mvsrelease.py                # Executor: release
│   └── mbt/                         # Core library package
│       ├── __init__.py
│       ├── config.py                # Config merge logic
│       ├── project.py               # project.toml parser & validator
│       ├── lockfile.py              # Lockfile read/write
│       ├── version.py               # Semver parsing & VRM conversion
│       ├── datasets.py              # Dataset name resolution
│       ├── dependencies.py          # GitHub Release resolution
│       ├── output.py                # Output formatters
│       ├── mvsmf.py                 # Mainframe client (urllib)
│       └── jcl.py                   # JCL template rendering & helpers
├── mk/
│   ├── core.mk                      # Main include
│   ├── targets.mk                   # Standard targets
│   ├── rules.mk                     # Pattern rules
│   └── defaults.mk                  # Default CFLAGS, conventions
├── templates/
│   ├── project.toml                 # Skeleton for new projects
│   └── jcl/
│       ├── alloc.jcl.tpl
│       ├── asm.jcl.tpl
│       ├── ncallink.jcl.tpl
│       ├── link.jcl.tpl
│       ├── copy.jcl.tpl
│       ├── delete.jcl.tpl
│       └── receive.jcl.tpl
├── .github/
│   └── workflows/
│       ├── build.yml                # Reusable build workflow
│       └── release.yml              # Reusable release workflow
├── examples/
│   └── hello370/                    # Reference project (smoke test)
│       ├── project.toml
│       ├── src/hello.c
│       └── Makefile
├── Makefile
├── README.md
└── VERSION
```

Zero external Python dependencies. All stdlib.

---

## 14. Project Layout (Consumer)

```
{project}/
├── project.toml
├── .env                             # gitignored
├── .mbt/
│   ├── mvs.lock                     # committed
│   └── logs/                        # gitignored
├── contrib/                         # gitignored
├── src/                             # C sources (convention)
├── asm/                             # ASM sources (convention)
├── maclib/                          # mainframe macros (if applicable)
├── mbt/                             # git submodule
├── Makefile
└── .github/workflows/
    ├── build.yml
    └── release.yml
```

### 14.1 Minimal Project Makefile

```makefile
MBT_ROOT := mbt
include $(MBT_ROOT)/mk/core.mk
```

### 14.2 Minimal CI Workflow (build)

```yaml
name: Build
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  build:
    uses: mvslovers/mbt/.github/workflows/build.yml@v1
    secrets:
      MVS_HOST: ${{ secrets.MVS_HOST }}
      MVS_PORT: ${{ secrets.MVS_PORT }}
      MVS_USER: ${{ secrets.MVS_USER }}
      MVS_PASS: ${{ secrets.MVS_PASSWORD }}
```

### 14.3 Minimal CI Workflow (release)

```yaml
name: Release
on:
  push:
    tags: ['v*']

jobs:
  release:
    uses: mvslovers/mbt/.github/workflows/release.yml@v1
    secrets:
      MVS_HOST: ${{ secrets.MVS_HOST }}
      MVS_PORT: ${{ secrets.MVS_PORT }}
      MVS_USER: ${{ secrets.MVS_USER }}
      MVS_PASS: ${{ secrets.MVS_PASSWORD }}
      GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

---

## 15. CI/CD Strategy

### 15.1 Build Pipeline (on push to main)

```
1. Checkout (with submodules)
2. Setup Python 3.12+
3. Restore dependency cache (~/.mbt/cache/)
4. make doctor
5. make bootstrap
6. make build
7. make link (if applicable)
8. Upload build logs (always, even on failure)
9. Post: make distclean (cleanup CI datasets)
```

### 15.2 Release Pipeline (on version tag)

```
1. Checkout (with submodules)
2. Setup Python 3.12+
3. Extract version from tag
4. Validate tag matches project.toml version
5. make distclean
6. make bootstrap
7. make build
8. make link (if applicable)
9. make package
10. Create GitHub Release with assets
11. Post: make distclean (cleanup CI datasets)
```

### 15.3 CI Dataset Isolation

```yaml
env:
  MBT_BUILD_ID: ${{ github.run_number }}
```

### 15.4 Dependency Caching

```yaml
- uses: actions/cache@v4
  with:
    path: ~/.mbt/cache
    key: mbt-deps-${{ hashFiles('.mbt/mvs.lock') }}
    restore-keys: mbt-deps-
```

---

## 16. Example Projects

### 16.1 crent370 (runtime)

```toml
[project]
name    = "crent370"
version = "1.0.0"
type    = "runtime"

[mvs.build.datasets.maclib]
suffix    = "MACLIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]
local_dir = "maclib/"

[mvs.build.datasets.punch]
suffix    = "OBJECT"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[mvs.build.datasets.ncalib]
suffix    = "NCALIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[artifacts]
headers = true
mvs     = true

[release]
github = "mvslovers/crent370"
version_files = ["project.toml"]
```

### 16.2 httpd (application)

```toml
[project]
name    = "httpd"
version = "3.3.1-dev"
type    = "application"

[build]
cflags = ["-fverbose-asm"]

[build.sources]
c_dirs = ["src/", "credentials/"]

[dependencies]
"mvslovers/crent370" = ">=1.0.0"
"mvslovers/ufs370"   = ">=1.0.0"
"mvslovers/lua370"   = ">=1.0.0"
"mvslovers/mqtt370"  = ">=1.0.0"

[mvs.asm]
max_rc = 4

[mvs.build.datasets.punch]
suffix    = "OBJECT"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[mvs.build.datasets.ncalib]
suffix    = "NCALIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[mvs.build.datasets.syslmod]
suffix    = "LOAD"
dsorg     = "PO"
recfm     = "U"
lrecl     = 0
blksize   = 32760
space     = ["TRK", 10, 5, 10]

[mvs.install]
naming = "fixed"

[mvs.install.datasets.ncalib]
name = "HTTPD.NCALIB"

[mvs.install.datasets.syslmod]
name = "HTTPD.LOAD"

[[link.module]]
name    = "HTTPD"
entry   = "HTTPD"
options = ["RENT", "REUS"]
include = ["HTTPD", "HTTPSRV", "HTTPCGI"]

[artifacts]
headers        = true
mvs            = true
package_bundle = true

[release]
github = "mvslovers/httpd"
version_files = ["project.toml", "src/version.h"]
```

### 16.3 mvsmf (module)

```toml
[project]
name    = "mvsmf"
version = "1.0.0-dev"
type    = "module"

[dependencies]
"mvslovers/crent370" = ">=1.0.0"
"mvslovers/ufs370"   = ">=1.0.0"
"mvslovers/httpd"    = ">=3.3.1"

[mvs.build.datasets.punch]
suffix    = "OBJECT"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[mvs.build.datasets.ncalib]
suffix    = "NCALIB"
dsorg     = "PO"
recfm     = "FB"
lrecl     = 80
blksize   = 3120
space     = ["TRK", 10, 5, 10]

[mvs.build.datasets.syslmod]
suffix    = "LOAD"
dsorg     = "PO"
recfm     = "U"
lrecl     = 0
blksize   = 32760
space     = ["TRK", 10, 5, 10]

[[link.module]]
name    = "MVSMF"
entry   = "MVSMF"
options = ["RENT", "REUS"]
include = ["MVSMF"]

[artifacts]
mvs = true

[release]
github = "mvslovers/mvsmf"
version_files = ["project.toml"]
```

---

## 17. Dependency Graph

```
crent370 (runtime)  ─── (none)

ufs370 (library)    ─── crent370
lua370 (library)    ─── crent370

mqtt370 (library)   ─── crent370, lua370

httpd (application) ─── crent370, ufs370, lua370, mqtt370

mvsmf (module)      ─── crent370, ufs370, httpd
```

Phase 1: no transitive resolution. All dependencies declared explicitly.

---

## Appendix A: Phase 2 Features

1. **Transitive dependency resolution** with conflict detection
2. **Bulk assembly (`make bulk`)** — single JCL job for all sources
3. **DCB attribute inheritance** — install inherits from build by key
4. **Parallel builds** — concurrent mvsasm jobs
5. **Incremental builds** — only assemble changed sources;
   `.mbt/state.json` reserved for source hash tracking
6. **Plugin system** — formalized executor/template/target extension
7. **mbt init** — scaffold new project from templates
8. **Cross-project build** — build multiple projects in dep order
9. **Checksum verification** — SHA256 in package.toml
10. **Dep version consistency** — cross-project conflict detection
11. **Bootstrap DCB validation** — verify existing dataset DCB
    attributes match project.toml; error on mismatch instead of
    warn-and-skip (requires mvsMF endpoint for dataset attributes)

## Appendix B: Open Design Questions

1. **Project-local JCL overrides**: Should projects override templates?
   Trade-off: flexibility vs. maintenance.

2. **string.Template limits**: If templates need more complex logic,
   reconsider Jinja2 or custom engine. Current: stdlib sufficient.

3. **Zowe profile integration**: Support `~/.zowe/` as additional
   config source for existing Zowe users?

4. **XMIT RECEIVE validation**: TSO RECEIVE via IKJEFT01 needs
   testing on MVS/CE with binary-uploaded XMIT files.

---

*End of Specification v1.0.0*
