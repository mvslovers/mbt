# mbt — MVS Build Tool

Cross-compile, assemble, link, and package [MVS 3.8j](https://en.wikipedia.org/wiki/MVS) projects from a modern host.

mbt is a reusable Git submodule that centralizes the full build pipeline for
[mvslovers](https://github.com/mvslovers) projects targeting MVS 3.8j on Hercules.

---

## Overview

```
C source (.c)
  └─► c2asm370        cross-compile to S/370 assembler (on host)
        └─► IFOX00    assemble on MVS via mvsMF REST API
              └─► IEWL (NCAL)   link to NCALIB
                    └─► IEWL (final)   build load module
                          └─► IEBCOPY   install to target dataset
                                └─► TRANSMIT   package as XMIT for release
```

**Key properties:**

- Single `project.toml` per project — no Makefile boilerplate
- 2-line Makefile integrates the full pipeline via `make`
- Zero external Python dependencies — stdlib only
- All MVS communication via [mvsMF](https://github.com/mvslovers/mvsmf) REST API
- GitHub Releases as package registry — versioned deps with lockfile
- Works with any reachable MVS 3.8j system (local Hercules, remote TK4-, MVS/CE)

---

## Requirements

| Tool | Version | Purpose |
|------|---------|---------|
| Python | 3.12+ | Build scripts (stdlib only) |
| GNU Make | any | Orchestration |
| [c2asm370](https://github.com/mvslovers/c2asm370) | any | C to S/370 cross-compiler |
| [mvsMF](https://github.com/mvslovers/mvsmf) | any | MVS REST API (running on target system) |

---

## Quick Start

### 1. Add mbt as submodule

```sh
cd your-project
git submodule add https://github.com/mvslovers/mbt.git mbt
```

### 2. Create your Makefile

```makefile
MBT_ROOT := mbt
include $(MBT_ROOT)/mk/core.mk
```

That's it. All build targets are now available.

### 3. Configure MVS connection

Create `~/.mbt/config.toml`:

```toml
[mvs]
host        = "myhost.lan"
port        = 1080
user        = "IBMUSER"
pass        = "SYS1"
hlq         = "IBMUSER"
deps_hlq    = "IBMUSER.DEPS"
deps_volume = "WORK00"

[jes]
jobclass = "A"
msgclass = "H"
```

`SYS1.MACLIB` and `SYS1.AMODGEN` are always included in the SYSLIB
concatenation — no configuration required. If your project needs
additional system macro libraries (e.g. `SYS2.MACLIB`), declare them
in `project.toml`:

```toml
[system]
maclibs = ["SYS2.MACLIB"]
```

### 4. Create project.toml

```toml
[project]
name    = "myapp"
version = "1.0.0"
type    = "application"

[mvs.build.datasets.punch]
suffix  = "OBJECT"
dsorg   = "PO"
recfm   = "FB"
lrecl   = 80
blksize = 3120
space   = ["TRK", 5, 2, 5]

[mvs.build.datasets.ncalib]
suffix  = "NCALIB"
dsorg   = "PO"
recfm   = "U"
lrecl   = 0
blksize = 32760
space   = ["TRK", 5, 2, 5]

[mvs.build.datasets.syslmod]
suffix  = "LOAD"
dsorg   = "PO"
recfm   = "U"
lrecl   = 0
blksize = 32760
space   = ["TRK", 5, 2, 5]

[dependencies]
"mvslovers/crent370" = ">=1.0.0"

[link.module]
name    = "MYAPP"
options = ["LIST", "XREF", "LET"]

[artifacts]
mvs = true
```

### 5. Build

```sh
make doctor         # verify environment
make bootstrap      # resolve deps, provision datasets on MVS
make build          # cross-compile C + assemble on MVS
make link           # final linkedit
make package        # create release artifacts in dist/
```

---

## Make Targets

| Target | Description |
|--------|-------------|
| `make doctor` | Check environment (Python, c2asm370, MVS connectivity) |
| `make bootstrap` | Resolve dependencies, upload to MVS, allocate datasets |
| `make build` | Cross-compile C sources and assemble all modules on MVS |
| `make link` | Final linkedit to produce load module |
| `make install` | Copy build datasets to install datasets (IEBCOPY) |
| `make package` | Create release artifacts in `dist/` |
| `make release VERSION=1.2.0` | Bump version, tag, push |
| `make graph` | Display dependency tree |
| `make datasets` | List all project datasets with status |
| `make clean` | Remove local build artifacts |
| `make distclean` | Full cleanup (contrib/, .mbt/, asm/*.s) |

---

## Configuration

Configuration is resolved in priority order:

1. **Environment variables** (`MBT_MVS_HOST`, `MBT_MVS_PORT`, etc.)
2. **Project-local `.env`** file
3. **Global `~/.mbt/config.toml`**
4. **Built-in defaults**

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MBT_MVS_HOST` | `localhost` | MVS hostname or IP |
| `MBT_MVS_PORT` | `1080` | mvsMF API port |
| `MBT_MVS_USER` | `IBMUSER` | MVS userid |
| `MBT_MVS_PASS` | | MVS password |
| `MBT_MVS_HLQ` | `IBMUSER` | High-level qualifier for datasets |
| `MBT_MVS_DEPS_HLQ` | `{HLQ}.DEPS` | HLQ for dependency datasets |
| `MBT_MVS_DEPS_VOLUME` | | Volume for RECEIVE (required if no PUBLIC volume) |
| `MBT_JES_JOBCLASS` | `A` | JES job class |
| `MBT_JES_MSGCLASS` | `H` | JES message class |
| `MBT_BUILD_ID` | | CI build number (enables CI dataset naming) |

---

## Project Types

| Type | Description | NCAL Link | Final Link | Artifacts |
|------|-------------|-----------|------------|-----------|
| `runtime` | C runtime library (e.g. crent370) | yes | no | ncalib, maclib |
| `library` | Reusable library (e.g. ufs370) | yes | no | ncalib, maclib |
| `module` | Loadable module | yes | yes | syslmod |
| `application` | Standalone program (e.g. httpd) | yes | yes | syslmod |

---

## Dependency Management

Dependencies are declared in `[dependencies]` with semver constraints:

```toml
[dependencies]
"mvslovers/crent370" = ">=1.0.0"
"mvslovers/ufs370"   = ">=0.9.0,<2.0.0"
```

On `make bootstrap`, mbt:

1. Resolves the best matching version from GitHub Releases
2. Downloads release XMIT archives to `~/.mbt/cache/`
3. Uploads and RECEIVEs them on MVS (`{DEPS_HLQ}.{DEP}.{VRM}.*`)
4. Extracts headers to `contrib/{dep}-{version}/include/`
5. Writes a lockfile to `.mbt/mvs.lock`

The lockfile pins exact versions for reproducible builds. Commit it.

Use `make bootstrap ARGS=--update` to re-resolve all dependencies.

### Dependency tree

```sh
$ make graph
httpd v3.3.1
 ├─ crent370 v1.0.0
 ├─ ufs370 v1.0.0
 │   └─ crent370 v1.0.0
 └─ mqtt370 v1.0.0
     ├─ crent370 v1.0.0
     └─ lua370 v1.0.0
```

---

## Dataset Naming

| Category | Pattern | Example |
|----------|---------|---------|
| Build | `{HLQ}.{PROJECT}.{VRM}.{SUFFIX}` | `IBMUSER.HTTPD.V3R3M1.NCALIB` |
| CI Build | `{HLQ}.{PROJECT}.B{ID}.{SUFFIX}` | `IBMUSER.HTTPD.B42.NCALIB` |
| Dependency | `{DEPS_HLQ}.{DEP}.{VRM}.{SUFFIX}` | `IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB` |
| Install (fixed) | `{HLQ}.{name}` | `IBMUSER.HTTPD.LOAD` |

---

## Packaging

`make package` creates release artifacts in `dist/`:

| File | Contents |
|------|----------|
| `package.toml` | Auto-generated manifest with metadata, deps, dataset defs |
| `{name}-{ver}-mvs.tar.gz` | XMIT files for MVS datasets |
| `{name}-{ver}-headers.tar.gz` | Public headers (libraries only, if `artifacts.headers = true`) |

Which datasets are packaged depends on the project type:

- **application / module**: `syslmod` (the load module)
- **library / runtime**: `ncalib` + `maclib`

Override with `[artifacts] mvs_datasets = ["syslmod", "ncalib"]`.

---

## CI/CD

mbt provides reusable GitHub Actions workflows:

### Build workflow

```yaml
# .github/workflows/build.yml
on: [push, pull_request]
jobs:
  build:
    uses: mvslovers/mbt/.github/workflows/build.yml@main
    secrets:
      MVS_HOST: ${{ secrets.MVS_HOST }}
      MVS_PORT: ${{ secrets.MVS_PORT }}
      MVS_USER: ${{ secrets.MVS_USER }}
      MVS_PASS: ${{ secrets.MVS_PASS }}
```

### Release workflow

```yaml
# .github/workflows/release.yml
on:
  push:
    tags: ["v*"]
jobs:
  release:
    uses: mvslovers/mbt/.github/workflows/release.yml@main
    secrets:
      MVS_HOST: ${{ secrets.MVS_HOST }}
      MVS_PORT: ${{ secrets.MVS_PORT }}
      MVS_USER: ${{ secrets.MVS_USER }}
      MVS_PASS: ${{ secrets.MVS_PASS }}
```

The release workflow validates that the tag matches `project.toml` version,
runs the full pipeline, and creates a GitHub Release with `dist/*` as assets.

---

## Releasing a New Version

```sh
make release VERSION=1.2.0
```

This updates the version in all files listed in `[release] version_files`,
commits, tags `v1.2.0`, and pushes. CI then builds and publishes the release.

---

## Directory Structure

```
your-project/
├── project.toml          # project definition
├── Makefile              # 2 lines: MBT_ROOT + include
├── .env                  # local MVS connection overrides (gitignored)
├── .mbt/
│   ├── mvs.lock          # pinned dependency versions (committed)
│   └── logs/             # JES spool logs on failure
├── src/                  # C sources
├── asm/                  # assembler sources + generated .s files
├── include/              # public headers (libraries)
├── contrib/              # extracted dependency headers (gitignored)
├── dist/                 # release artifacts (gitignored)
└── mbt/                  # this repo (git submodule)
```

---

## Reference Project

[examples/hello370](examples/hello370/) — a minimal C + assembler application
that depends on crent370. Demonstrates the full pipeline.

---

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Build failure (RC > max_rc, link error) |
| 2 | Configuration error |
| 3 | Dependency error |
| 4 | Mainframe communication error |
| 5 | Dataset error |
| 99 | Internal error |

---

## License

MIT
