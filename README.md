# mbt — MVS Build Tool

> **Work in Progress** — Milestones 1–3 implemented. Not yet ready for production use.

Cross-compile, assemble, link, and package [MVS 3.8j](https://en.wikipedia.org/wiki/MVS) projects from a modern host.

mbt is a reusable Git submodule that centralizes the full build pipeline for
[mvslovers](https://github.com/mvslovers) projects targeting MVS 3.8j

---

## Overview

```
C source (.c)
  └─► c2asm370        cross-compile to S/370 assembler (on host)
        └─► IFOX00    assemble on MVS via mvsMF REST API
              └─► IEWL (NCAL)   link to NCALIB
                    └─► IEWL (final)   build load module
                          └─► IEBCOPY   install to target dataset
```

**Key properties:**

- Single `project.toml` per project — no Makefile boilerplate
- Zero external Python dependencies — stdlib only (`tomllib`, `urllib`, `string`)
- All MVS communication via [mvsMF](https://github.com/mvslovers/mvsmf) REST API
- GitHub Releases as package registry — versioned deps, lockfile, header extraction
- Works with any reachable MVS 3.8j system (local Hercules, remote TK4-, MVS/CE)

---

## Requirements

| Tool | Version | Purpose |
|------|---------|---------|
| Python | 3.12+ | Build scripts (stdlib only) |
| GNU Make | any | Orchestration |
| [c2asm370](https://github.com/mvslovers/c2asm370) | any | C → S/370 cross-compiler |
| [mvsMF](https://github.com/mvslovers/mvsmf) | any | MVS REST API (running on Hercules) |

---

## Configuration

### Global — `~/.mbt/config.toml`

```toml
[mvs]
deps_volume = "WORK00"   # volume for RECEIVE of dependency XMIT files

[system.maclibs]
sys1    = "SYS1.MACLIB"
amodgen = "SYS1.AMODGEN"
sys2    = "SYS2.MACLIB"
```

### Per-project — `.env`

```sh
MBT_MVS_HOST=localhost
MBT_MVS_PORT=1080
MBT_MVS_USER=IBMUSER
MBT_MVS_PASS=sys1
MBT_MVS_HLQ=IBMUSER
```

See `.env.example` in each project for the full list.

---

## Project definition — `project.toml`

```toml
[project]
name    = "hello370"
version = "1.0.0"
type    = "application"      # runtime | library | module | application

[build.sources]
asm_dirs = ["asm/"]          # hand-written assembler
# c_dirs = ["src/"]          # C sources (compiled via c2asm370), default

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
recfm   = "FB"
lrecl   = 80
blksize = 3120
space   = ["TRK", 5, 2, 5]

[mvs.build.datasets.syslmod]
suffix  = "LOAD"
dsorg   = "PO"
recfm   = "U"
lrecl   = 0
blksize = 32760
space   = ["TRK", 5, 2, 5]

[mvs.install]
naming = "fixed"             # fixed: HLQ.name | vrm: HLQ.PROJECT.VRM.SUFFIX

[mvs.install.datasets.syslmod]
name = "LOAD"                # installs to IBMUSER.LOAD

[link.module]
name    = "HELLO"
# entry defaults to @@CRT0 for application/module types
# include defaults to ["@@CRT1", name]
options = ["LIST", "XREF", "LET"]

[dependencies]
"mvslovers/crent370" = ">=1.0.0"

[artifacts]
mvs = true

[release]
github        = "mvslovers/hello370"
version_files = ["project.toml"]
```

---

## Build pipeline

From the project directory (where `project.toml` lives):

```sh
# 1. Resolve and provision dependencies on MVS
python3 /path/to/mbt/scripts/mbtbootstrap.py

# 2. Compile C sources + assemble all modules
python3 /path/to/mbt/scripts/mvsasm.py

# 3. Final linkedit → load module
python3 /path/to/mbt/scripts/mvslink.py

# 4. Install to target dataset
python3 /path/to/mbt/scripts/mvsinstall.py
```

Or assemble a single member:

```sh
python3 /path/to/mbt/scripts/mvsasm.py --member HELLO
```

---

## Project types

| Type | NCAL assembly | Final link | Install |
|------|--------------|------------|---------|
| `runtime` | ✓ | — | — |
| `library` | ✓ | — | — |
| `module` | ✓ | ✓ | optional |
| `application` | ✓ | ✓ | optional |

---

## Dependency management

Dependencies are declared in `[dependencies]` with semver constraints and
resolved from GitHub Releases. On bootstrap, mbt:

1. Resolves the best matching version from the GitHub Releases API
2. Downloads the release XMIT archive
3. Uploads and `RECEIVE`s it on MVS into `{deps_hlq}.{DEP}.{VRM}.*`
4. Extracts headers into `contrib/{dep}-{version}/include/`
5. Writes a lockfile to `.mbt/mvs.lock`

The lockfile pins exact versions. Check it in — it is reproducible.

---

## Reference project

[examples/hello370](examples/hello370/) — a minimal C + assembler application
that depends on [crent370](https://github.com/mvslovers/crent370). Demonstrates
the full pipeline end-to-end.

---

## Exit codes

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
