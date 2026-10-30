# mbt — Project-Specific Context

This file extends `~/repos/mvs/CLAUDE.md` (root context). Read the root first.
Everything there applies here. This file adds mbt-specific details only.

---

## What Is mbt?

MBT (mbt), the MVS Build Tool, builds, tests, packages, deploys and releases
the mvslovers projects. This repository holds three generations side by side:

| Generation | Code | What it is |
|---|---|---|
| **mbt 3** | `cmd/mbt/`, `internal/` (Go) | one installed program, `mbt`; reads `mbt.toml` (schema 3) or an mbt 2 `project.toml`. Released on `v3.*` tags (`.github/workflows/mbt-release.yml`). Status and command summary: `cmd/mbt/README.md` |
| **mbt 2** | `mk/mbt.mk`, `scripts/` (Python) | git submodule + two-line `Makefile` + `project.toml`; cc370 host build. Kept until every project has migrated (`docs/MIGRATION.md`) |
| **v1 (legacy)** | `mk/legacy/`, `scripts/legacy/` | remote mvsMF/JCL build (assemble and link on MVS); maintenance mode |

`scripts/mbt/` is the Python package shared by mbt 2 and v1.
`templates/jcl/` is used by all of them (mbt 3 embeds it, `embed.go`).
mbt 3's output from a `project.toml` must stay byte-identical to mbt 2's
(`internals/acceptance/`).

---

## Key Documents

| Document | Location | Purpose |
|----------|----------|---------|
| **mbt 3 design** | `internals/mbt-3-design.md` | Design of mbt 3 (the code cites its sections) |
| **mbt.toml schema** | `internals/mbt-3-schema.md` | Every key, default, and its `project.toml` origin |
| **v2 behaviours** | `internals/v2-behaviours.md`, `internals/v2-baseline.md` | What mbt 3 has to keep from mbt 2, and why |
| **Acceptance** | `internals/acceptance/README.md` | The byte-for-byte mbt 2 / mbt 3 comparison |
| **Specification** | `internals/mvs-build-spec-v1.0.0.md` | Normative for v1; exit codes and log format (section 11) still hold for all generations |
| **Implementation Guide** | `internals/mbt-implementation-guide.md` | How the v1 Python code was built, milestone by milestone |
| **User docs** | `docs/MIGRATION.md`, `docs/testing.md`, `docs/books/` | mbt 2 → 3 migration, testing, the ML02 manuals (User's Guide, Reference) |

User-visible behaviour of mbt 3 is documented in the books; change the book
in the PR that changes the behaviour (`~/repos/mvs/.workspace/context/books.md`).

---

## Tech Stack

- **mbt 3: Go** (`go.mod`), dependencies `BurntSushi/toml` and `arnodel/golua`
  (Lua for `mbt/init.lua`, extensions and plugins)
- **mbt 2 / v1: Python 3.12+** — stdlib only, **zero external dependencies**
- **GNU Make** — mbt 2 / v1 orchestration layer
- **mvsMF REST API** — mainframe communication (z/OSMF-compatible endpoints)
- **string.Template** — JCL template engine (not Jinja2, not m4)
- **GitHub Releases** — package registry

### Python: stdlib only

**No pip install. No requirements.txt. No venv.** If you find yourself
reaching for an external package, stop and find a stdlib solution.

`subprocess` has exactly two sanctioned uses — anything else needs a reason
in the PR:

- **git**, for build provenance and release automation (`mvsrelease.py`,
  `mbt/buildstamp.py`)
- **invoking the cc370 toolchain**, which is not a library: `mbtdeploy.py`
  runs `ld370 --pack`, `mbtdist.py` runs `xmit370 create`

`zipfile` is for `mbtdist.py` only: the SMP installation package ships as
both `.zip` and `.tar.gz`, mirroring what GitHub does with source archives,
because the operators who install these products are as likely to be on
Windows as on Unix.

---

## Building and Testing mbt Itself

```
go build -o mbt ./cmd/mbt        # mbt 3
gofmt -l . ; go vet ./... ; go test ./...   # what CI (go.yml) runs
python3 -m unittest discover tests/          # mbt 2 / v1 unit tests, no MVS
```

- `tests/integration/` runs against a live mvsMF (`MBT_MVS_*`) and skips
  itself when none is reachable; your own throwaway container (root) serves.
- `internals/acceptance/diff23.py` needs cc370 and runs locally only (not in CI).
- **Reference project:** `examples/hello370/` (an mbt 2 `project.toml`).
- `build.yml` / `release.yml` (mbt 2), `build3.yml` / `release3.yml`
  (mbt 3) and the `-legacy` ones are reusable workflows **for consumer
  projects**; a change there reaches every project that calls them.

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

### MVS communication

All MVS communication goes through the mvsMF client
(`scripts/mbt/mvsmf.py`, `internal/mvsmf/`), with progressive backoff for
job polling (1s → 2s → 3s → 5s).

---

## Configuration

**mbt 3:** MVS systems come from `~/.mbt/targets.toml` (`MBT_HOME` moves it),
one `[target.NAME]` each; CI sets one through `MBT_TARGET_*`. Without
either, the mbt 2 settings (`MBT_MVS_*`, `.env`) still work, with a warning.
The project file is `mbt.toml`.

**mbt 2 / v1:**

```
Priority (highest → lowest):
1. Environment variables (MBT_*)
2. Project-local .env
3. Global ~/.mbt/config.toml
4. Built-in defaults
```

`project.toml` is the single project definition file; `.env` is only for
local overrides (MVS connection, HLQ), not for project structure. See spec
section 3 for the schema.

---

## Read when relevant

| Before you … | read |
|---|---|
| work on the v1/v2 Python code (dataset naming, SYSLIB order, Make integration, JCL templates, version ordering) | `internals/v1-implementation-notes.md` |
| change `[distribution]` / `[smp]` handling or the SMP package | `~/repos/mvs/.workspace/context/smp.md`, `docs/books/ref/ref-smp.typ` |
| change deploy behaviour | `~/repos/mvs/.workspace/context/deploy.md`, `internals/v2-behaviours.md` |
| touch an output mbt 2 also produces | `internals/acceptance/README.md` |
