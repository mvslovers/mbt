# mbt 3 — Design Proposal

**Status:** draft for community discussion — nothing in here is implemented.
**Date:** 2026-10-02

This document proposes replacing today's mbt — a git submodule plus a Make
include — with a standalone, installable build tool in the spirit of cargo,
gradle or maven. It collects what we think is settled, what we propose, and
what is still open. Section 17 lists the open questions — that is where the
discussion should start.

**Focus:** the core is today's host path — cc370, as370 and ld370. mbt 3
has to do that path completely and well first. Everything else in this
document (other languages, native backends, other toolchains, the MCP
server, workspaces, …) is a bonus that builds on it, and none of it may hold
the core back.

Each topic is marked:

- **Decided** — the direction is set; comments on the details are welcome.
- **Proposed** — a concrete suggestion that needs agreement.
- **Open** — we have options, not an answer.

---

## 1. Why

What we have today works: eleven projects (ten actively maintained, plus
brexx370 in maintenance mode) build, test,
package and release through mbt v2. But the way mbt reaches those projects
costs something every day.

| Today | Consequence |
|---|---|
| mbt is a git submodule in every project | Every mbt fix needs a submodule bump in every consumer. On `main` today, 8 of these 11 projects pin the current mbt; httprexx is 5 commits behind, httplua and lua370 are 32 behind. |
| mbt had never been tagged (until v2.0.0 on 2026-10-03) | There was no version a project could ask for — only a commit SHA. |
| Reusable workflows are called `@main`, the scripts come from the submodule | Workflow logic and the scripts it drives come from different mbt revisions. `release.yml` already carries an error message for exactly that case ("submodule predates the resolver"). |
| One `.env` per project, one MVS system per `.env` | Working against a second system (mvsdev, drnmig3a, MVSCE-LAB, …) means editing files or juggling environment variables; credentials sit in plain text in many places. |
| libc370 installs into the cc370 sysroot | One libc370 per machine. A project builds against whatever was installed last. CI rebuilds cc370 and libc370 from source on every run. |
| `project.toml` enumerates everything | Project files range from 31 lines (lstring370) to 2729 (rexx370, 64 tests, 17 modules). Most of the bulk is repetition: every test lists the sources it links, `startup = "crt1"` everywhere, identical SMP boilerplate in every product. |
| Project-specific needs go into the Makefile | Three projects carry near-identical copies of the Docker `run-mvs`/`stop-mvs` targets; httpd fetches a pinned tool and builds a UFS image in Make. |

## 2. Goals and non-goals

**Goals**

- Install mbt once; never vendor it into a project again.
- Every project still builds reproducibly with the mbt version it declares.
- One short, declarative project file; conventions instead of enumeration.
- Named MVS targets, credentials kept out of project directories.
- A pinned, per-project toolchain (cc370 + libc370) — locally and in CI.
- Prebuilt dependencies that say what built them and what they need, so
  transitive dependencies resolve and a toolchain mismatch is caught.
- An extension mechanism for what mbt does not do (yet).
- Room for more than C and assembler (COBOL via cobc370 is the first candidate).
- An MCP server for mvsMF, so other tools can work with MVS safely.

**Non-goals**

- Changing the target: MVS 3.8j, 24-bit, cc370/as370/ld370 stay as they are.
- Changing the installer: products install through SMP4. *How* releases
  map onto SYSMODs does change (section 6.4).
- Building on MVS *by default*. The default build stays on the host; MVS is
  touched by deploy, MVS tests and install verification only. Native
  backends are an option a project can choose (section 11), not the default.

## 3. What it feels like

```
mbt new myproj              # scaffold a project
mbt build                   # build the primary deliverable (incremental, parallel)
mbt build HTTPD             # one module
mbt test                    # host tests          (today: make test-host)
mbt test --mvs              # tests on MVS        (today: make test-mvs)
mbt deps / mbt update       # resolve / refresh mbt.lock
mbt package / mbt dist      # release artifacts / SMP install package
mbt deploy [--target NAME]  # into the DEV library on a named target
mbt release [--pre]
mbt doctor                  # toolchain + every configured target
mbt run <task>              # a project-defined task (section 9)
mbt mvs up / down           # local MVS/CE in Docker
mbt mcp                     # MCP server for mvsMF (section 12)
```

A project contains its project file, `mbt.lock`, and the usual `src/`,
`include/`, `asm/`, `test/`. No `Makefile`, no `mbt/` submodule.

## 4. Implementation language — **Decided: Go**

- A single static binary per platform (linux/darwin × amd64/arm64), no Python
  on the host.
- The standard library covers almost everything mbt does: HTTP (mvsMF, GitHub),
  JSON, tar/gzip/zip, SHA-256, process execution, concurrency for parallel
  compiles, `go:embed` for JCL templates and `mbtcheck.h`.
- Dependencies stay few, vendored and pinned: a TOML parser (if we keep TOML)
  and a Lua implementation (section 9).

Today's mbt is ~7,000 lines of Python with ~5,500 lines of tests. A rewrite is
a project of its own; section 16 describes how we keep it honest.

**Why Go, and what it rules out** (asked in the discussion of this proposal):

| Option | For | Against |
|---|---|---|
| Python (today) | the code exists | an interpreter on every host; no single binary to pin and download; slow start per call |
| C | could be compiled by cc370 and, in principle, run on MVS or CMS | HTTP, TLS, JSON, TOML, tar/gzip/zip, SHA-256 and process handling all written or vendored by hand |
| Rust | single binary, strong types | slower to write for a one-person project; the ecosystem has no Rust experience |
| **Go** | single static binary, cross-compiles trivially, the standard library covers the work | no MVS or CMS port |

**mbt running on MVS or CMS is not a goal** (maintainer, 2026-10-04): mbt is a
tool for *cross*-development — it builds on a modern host and talks to MVS
through mvsMF. Native backends (section 11) run tools *on* MVS through JCL;
mbt itself never runs there. Choosing Go closes that door on purpose. Should an
MVS- or CMS-side component ever be wanted, it would be a small separate program
(C or REXX) behind a defined interface, not a port of mbt.

## 5. Distribution and version pinning — **Decided** (2026-10-05)

The tool installs as a small **launcher**, the way the gradle wrapper or
rustup work:

1. `mbt` reads the version the project asks for:

   ```toml
   [toolchain]
   mbt = "3.0"
   ```

2. It downloads that release once into `~/.mbt/versions/3.0.x/` and runs it.
3. Without a project (or without a pin) it runs the newest installed version.

This keeps the one thing the submodule does well — each project builds with a
known mbt — and drops everything else. A project that is not ready for a new
mbt simply does not bump its pin.

Prerequisite: **mbt starts releasing itself** (tags, release artifacts per
platform) through the same release pipeline it offers everyone else. Begun
with [v2.0.0](https://github.com/mvslovers/mbt/releases/tag/v2.0.0)
(2026-10-03): a tag and a release, no artifacts yet; consumers can already pin
the reusable workflows to it.

The reusable workflows move with it: a consumer pins `uses:` to the same tag
as `[toolchain] mbt`, so workflow and tool can no longer drift apart.

## 6. The project file — **Decided: TOML in `mbt.toml`, plus an optional `mbt.lua`** (2026-10-05)

### 6.1 Cleanup, independent of the format

- **Name:** `mbt.toml`, matching `mbt.lock`; logic, where a project needs
  any, goes into an optional `mbt.lua` beside it (6.3). A `schema = 3` field
  lets the launcher tell old from new, and the new name shows at a glance
  whether a project has been migrated.
- **Tests are discovered, not listed.** Every `test/*.c` is a test. Tests
  link against the project's internal archive by autocall, so a test entry no
  longer lists the sources it links. An entry is only needed for exceptions:
  MVS-only, PARM, fixtures. Today the MVS link of a test already autocalls
  `[internal]` where a project declares it, but the native host-test build
  (`make test-host`) compiles only the listed sources — which is why the lists
  exist. The host build has to get the same archive for this to work.
- **Defaults instead of boilerplate:** a C program needs no `startup` (the
  CRT comes out of `libc.a` since libc370 2.3.0, mbt#158), `system = "Z038"`,
  `prereq = []`, `accept_fmid = true`, the DEV deploy library and the SMP
  library names (`<PROD>.LINKLIB`, `<PROD>.A…`, staging) follow the convention
  unless overridden.
- **The FMID is derived.** The project declares `prefix = "TUFS"`; mbt computes
  `TUFS140` from version 1.4.x and `delete` from the previous minor's tag
  (section 6.4: one FMID per minor, patches are PTFs). It refuses a release
  where the major or minor component exceeds 9. An explicit override
  stays possible (gaps such as `TUFS131` are legitimate). Today the id is
  hand-maintained, and forgetting to move it after `make release` is a
  documented trap.
- **The version lives in one place**, not in both `VERSION` and the project
  file.
- **Load module attributes are declared, never inherited.** `rent`, `reus`,
  `refr` per module, and a module without a declaration is an error in schema
  3. v2.1.0 introduced the keys (cc370#100): ld370's default had made 25 of 27
  ecosystem modules RENT without anyone deciding it, and a RENT C module with a
  writable static is one copy shared by concurrent tasks (measured, CDUSE=3 in
  httpd). `rent = true` is checked: no writable data in the module.
- **`mbt migrate`** rewrites a v2 `project.toml` into the new schema. With
  eleven projects to convert this is not optional.

### 6.2 About `[[double brackets]]`

`[[module]]` in TOML is an *array of tables*: each `[[module]]` header appends
one more element to the array `module`. It reads like a section header but
behaves like a list append — and a nested `[[test.fixture]]` attaches to
whichever `[[test]]` happened to come last above it. That is the part that
trips people up.

Since every module and test already has a unique name, TOML offers a clearer
spelling: **keyed tables**. The name becomes the key, and there is nothing to
"append to":

```toml
[module.HTTPD]
sources = ["src/httpstrt.c", "src/httpd.c", "src/httpprm.c"]
```

Keyed tables have no order. mbt would sort by name wherever order is visible
(build output, SMP package), which makes output deterministic as a bonus.

### 6.3 TOML vs Lua — the same project three ways

The example is httpd's real configuration, trimmed, plus one rexx370 test with
PARMs and a fixture (the case where `[[test.fixture]]` is hardest to read).

**A — today (mbt v2, TOML with arrays of tables)**

```toml
[project]
name = "httpd"
version = "4.2.0-dev"
type = "application"

[toolchain]
libc370 = "2.0.0"

[build]
cflags = ["-I", "include", "-I", "credentials/include", "-Wall", "-Werror"]

[host]
cflags = ["-Wno-error"]

[dependencies]
"mvslovers/ufsd" = ">=1.4.0-dev"
"mvslovers/crypto370" = ">=1.0.1"

[internal]
sources = ["src/*.c", "credentials/src/*.c"]
exclude = ["src/cgistart.c", "src/httpstrt.c"]

[[module]]
name = "HTTPD"
startup = "crt1"
sources = ["src/httpstrt.c", "src/httpd.c", "src/httpprm.c"]
ac = 1

[[module]]
name = "HTTPDM"
startup = "crt1"
sources = ["src/cgistart.c", "src/httpdm.c"]

[[module]]
name = "HTTPDMTT"
startup = "crt1"
sources = ["src/cgistart.c", "src/httpdmtt.c"]

[[module]]
name = "HTTPDSRV"
startup = "crt1"
sources = ["src/cgistart.c", "src/httpdsrv.c"]

[[test]]
name = "TSTSAFE"
sources = ["test/tstsafe.c", "src/httpesc.c", "src/httpsrdr.c"]

[[test]]
name = "TSTGCTX"
sources = ["test/mvs/tstgctx.c"]
host = false

# (from rexx370)
[[test]]
name = "TSTLDTSO"
startup = "crt1"
parm_batch = "0"
parm_tso = "1"
sources = [
  "test/tstldtso.c",
  "src/irx#load.c",
  "src/irx#ldqs.c",
  "src/irx#init.c",
  "src/irx#term.c",
  "src/irx#stor.c",
  # ... and more
]
[[test.fixture]]
dd = "SYSEXEC"
members = ["test/fixtures/tldtso/TLDTSOX", "test/fixtures/tldtso/TLDNUM"]

[deploy]
target = "HTTPD.DEV.LINKLIB"

[distribution]
readme = "docs/installation.md"
extra = ["build/webroot/httpd-webroot.img"]

[distribution.smp]
fmid = "THTP420"
delete = ["THTP410"]
system = "Z038"
prereq = []
accept_fmid = true
lklib = "HTTPD.HTTPLOAD"
target = "HTTPD.LINKLIB"
distlib = "HTTPD.AHTTPLOD"

[release]
version_files = ["VERSION"]
```

**B — proposed TOML (keyed tables, conventions)**

```toml
schema = 3

[project]
name    = "httpd"
version = "4.2.0-dev"
kind    = "application"

[toolchain]
mbt     = "3.0"
libc370 = "2.0.0"

[dependencies]
"mvslovers/ufsd"      = ">=1.4.0-dev"
"mvslovers/crypto370" = ">=1.0.1"

[build]
include = ["include", "credentials/include"]
cflags  = ["-Wall", "-Werror"]
host.cflags = ["-Wno-error"]

[internal]
sources = ["src/*.c", "credentials/src/*.c"]
exclude = ["src/cgistart.c", "src/httpstrt.c"]

[module.HTTPD]
sources = ["src/httpstrt.c", "src/httpd.c", "src/httpprm.c"]
ac = 1

[module.HTTPDM]
sources = ["src/cgistart.c", "src/httpdm.c"]

[module.HTTPDMTT]
sources = ["src/cgistart.c", "src/httpdmtt.c"]

[module.HTTPDSRV]
sources = ["src/cgistart.c", "src/httpdsrv.c"]

# test/*.c are found automatically; only exceptions are listed
[test.TSTGCTX]
host = false

[test.TSTLDTSO]
parm     = { batch = "0", tso = "1" }
fixtures = { SYSEXEC = ["test/fixtures/tldtso/TLDTSOX", "test/fixtures/tldtso/TLDNUM"] }

[distribution]
readme = "docs/installation.md"
extra  = ["build/webroot/httpd-webroot.img"]

[smp]
prefix = "THTP"            # → THTP420, delete THTP410, libraries by convention
```

**C — Lua as configuration**

```lua
schema(3)

project { name = "httpd", version = "4.2.0-dev", kind = "application" }

toolchain { mbt = "3.0", libc370 = "2.0.0" }

dependencies {
  ["mvslovers/ufsd"]      = ">=1.4.0-dev",
  ["mvslovers/crypto370"] = ">=1.0.1",
}

build {
  include = { "include", "credentials/include" },
  cflags  = { "-Wall", "-Werror" },
  host    = { cflags = { "-Wno-error" } },
}

internal {
  sources = { "src/*.c", "credentials/src/*.c" },
  exclude = { "src/cgistart.c", "src/httpstrt.c" },
}

module "HTTPD" { sources = { "src/httpstrt.c", "src/httpd.c", "src/httpprm.c" }, ac = 1 }

-- every CGI display module is the launcher plus one source file
for _, name in ipairs { "HTTPDM", "HTTPDMTT", "HTTPDSRV" } do
  module(name) { sources = { "src/cgistart.c", "src/" .. name:lower() .. ".c" } }
end

test "TSTGCTX" { host = false }

test "TSTLDTSO" {
  parm     = { batch = "0", tso = "1" },
  fixtures = { SYSEXEC = { "test/fixtures/tldtso/TLDTSOX", "test/fixtures/tldtso/TLDNUM" } },
}

distribution { readme = "docs/installation.md", extra = { "build/webroot/httpd-webroot.img" } }

smp { prefix = "THTP" }
```

**Trade-offs**

| | TOML (B) | Lua (C) |
|---|---|---|
| Readability for newcomers | high; looks like an INI file | high for simple files; loops and functions need Lua knowledge |
| Repetition (CGI modules, rexx370's 64 tests) | conventions only | loops and helper functions |
| mbt can **rewrite** the file (`migrate`, version bump, FMID) | yes, if the TOML library keeps comments — or by targeted line edits | not reliably: the file is a program, not data |
| What a reader sees is what mbt gets | yes | only after evaluation |
| Editor support for the project file | a published JSON schema gives completion and validation through Taplo, the TOML language server (section 14) | type annotations for lua-language-server — possible, but ours to write and maintain |
| One language for config **and** extensions | no — TOML + Lua | yes |
| Risk | — | evaluating a project file runs code; needs a sandbox (no `os`, no `io`) and determinism |

A middle path exists too: **declarative TOML, plus an optional `mbt.lua`**
that may add modules, tests and tasks programmatically (comparable to cargo's
`build.rs` or gradle's build scripts). Simple projects never see Lua; rexx370
can generate its 64 tests in five lines.

**Decided (2026-10-05): the middle path.** The project file is declarative
TOML (B) in `mbt.toml`; an optional `mbt.lua` beside it registers modules,
tests, tasks and rules through the extension context (sections 9 and 10).
What mbt reads from `mbt.toml` stays data, so `mbt migrate`, version bumps and
the editor schema keep working; only a project that ships an `mbt.lua` has
code in its build definition.

**Not considered further:**
- **JSON** — no comments. rexx370 has 683 comment lines in its project file,
  and many of them record *why* something is the way it is. Losing that is
  not acceptable. JSON remains the **output** format (`mbt metadata --json`,
  in the spirit of `cargo metadata`) for scripts and the MCP server.
- **YAML** — its implicit typing collides with exactly our data: `NO` and
  `ON` become booleans, `1.10` becomes the number `1.1`, `0750` is read as
  octal. FMIDs, versions and MVS names would be at risk all the time.
- **KDL** and similar — pleasant, but too little tooling and familiarity.

### 6.4 Releases on MVS: a function level per minor, a PTF per patch — **Proposed**

mbt v2 emits `++FUNCTION` only: every release, a patch included, spends a new
FMID that deletes its predecessor (#93). That costs three things. The id space
is small — no version component may exceed 9, because a 7-character id has
room for one digit each. Every upgrade leaves a tombstone (`DELBY`) in both
zones that a removal job has to name. And a one-line fix ships, and installs,
as a whole new product level.

mbt 3 maps the release kind onto the SYSMOD type:

| Release | SYSMOD | When |
|---|---|---|
| major or minor (`x.y.0`) | `++FUNCTION`, a new FMID that deletes the previous minor's | new function; a module, alias or library added, removed or renamed; a changed dataset name or JCLIN |
| patch (`x.y.z`, z > 0) | `++PTF` against the FMID of `x.y.0` | fixes only: the same modules, aliases and libraries as `x.y.0` |

- **One FMID per minor.** ufsd 1.4.0 to 1.4.9 all live under `TUFS140`; the
  limit of 9 binds only major and minor. That is the rule the ecosystem
  already had before patches started spending FMIDs.
- **PTFs are cumulative.** Each PTF carries every module changed since `x.y.0`
  and supersedes (`SUP`) the PTFs before it, so an operator applies exactly
  one PTF and never assembles a `PRE` chain.
- **A fresh install of a patch level** ships the `++FUNCTION` of `x.y.0` *and*
  the current PTF in one package and one job. A spent FMID never changes its
  content, so `x.y.z` cannot be a re-cut function.
- **mbt decides, the project does not.** `mbt release` knows whether it cuts
  a patch, and `mbt dist` compares the build with the `x.y.0` release
  artifacts to find the changed modules — possible because the build is
  reproducible with the clock pinned (`internals/v2-baseline.md`). A patch
  whose diff shows an added, removed or renamed module, alias or library is
  refused: that is a minor release.
- **Unchanged:** the element-ownership wall, `TALIAS` for aliases
  (#112–#115), the DDNAME rule of DELETE, and verifying an install by the
  member list, not the condition codes (all in the ecosystem's root context).

**To be measured before this is built** (on MVS/CE and TK5, with throwaway
FMIDs *and* throwaway module names, as for `TTST…`):

1. A `++PTF` with `++MOD` replaces a load module that its FMID installed
   through JCLIN `COPY` — in the target library and, on ACCEPT, the DLIB — and
   keeps `TALIAS`, `AC` and the attributes.
2. `SUP` of an earlier PTF behaves as described under SMP4 (APPLY over an
   applied predecessor, ACCEPT, `LIST` afterwards), and `RESTORE` of a
   not-accepted PTF puts the function level's module back.
3. The upgrade from `x.y.z` (function + PTF) to `x.(y+1).0`: does the new
   FUNCTION's `DELETE` take the PTF along, or does it leave a tombstone a
   removal job has to name?
4. Which prefix the PTF ids use (question 12).

#### USERMODs to IBM elements — **Proposed** (2026-10-06)

The third SYSMOD type the ecosystem ships. A project that patches IBM
elements -- brexx370's TSO integration `ZMG0001` (IKJCT430 and IKJCT437 in
`SYS1.CMDLIB(EXEC)`, FMID `EBB1102`), rexx370's `ZMG0002`/`ZMG0003` -- ships a
`++USERMOD` with `++VER … FMID(<the owning IBM FMID>)`, never a FUNCTION of
its own (root context: prefix `ZMG`, object decks only, KB `MVS-SMP-0004`).
Today each project builds the stream with its own scripts and attaches it to
the GitHub release by hand, where a re-pushed prerelease tag deletes it again.

| | FUNCTION / PTF | USERMOD |
|---|---|---|
| owner of the elements | the product's own FMID | an IBM FMID, named by the project |
| id | derived from the version | assigned by hand (`ZMG…`), one per change set |
| elements | load modules, bound by mbt | object decks, **not** linked: SMP APPLY links them against the installed module |
| shipped with | the SMP install package | its own stream (`<id>.smp`, EBCDIC FB80 for SMPPTFIN) plus its install and remove jobs |

What mbt does:

```toml
[usermod.ZMG0001]
fmid  = "EBB1102"                                   # the IBM FMID that owns the elements
mcs   = "tso/usermod/ZMG0001.mcs"                   # ++USERMOD, ++VER, one ++MOD per deck
decks = ["tso/IKJCT430.ASM", "tso/IKJCT437.ASM"]    # assembled, never linked
jobs  = ["tso/jcl/ZMG01*.jcl"]                      # check, backup, receive, apply, restore
```

- **Assemble** the decks with the clock pinned, so the same source gives the
  same deck. Their IBM macros (mvs38src, about 59 MB) are an input the
  project names; committed decks with a rebuild check are the fallback where
  CI cannot have them.
- **Write the stream** and check it before it is shipped -- the checks
  brexx370's `usermod.py` runs today become mbt's: MCS lines at most 72
  columns and encodable in CP037, every deck whole 80-byte cards ending in
  `END`, no card starting with `++`, every `++MOD` followed by its deck,
  exactly one `++USERMOD` and one `++VER`.
- **Publish** the stream and a zip of the jobs into `dist/`, so the release
  workflow carries them like every other artifact and a re-pushed
  prerelease keeps them.
- **Unchanged and still true:** `RESTORE` clears the inventory but leaves
  CSECTs the usermod added in the load module (KB `MVS-SMP-0005`).

Acceptance: `ZMG0001.smp` as built by brexx370's scripts today is the golden
file; mbt must write it byte for byte (brexx370 files the issue with the
layout and the file). Built with the project tasks of phasing step 4: until
then a project builds the stream in a task of its own and puts it into
`dist/`.

## 7. Targets instead of `.env` — **Proposed**

MVS systems get names and live in a per-user configuration that is never
checked in:

```toml
# ~/.mbt/config.toml
default_target = "mvsdev"

[targets.mvsdev]
host   = "10.0.0.5"
port   = 1080
user   = "MIKE"
password_cmd = "security find-generic-password -s mbt-mvsdev -w"
hlq    = "MIKE"
volume = "PUB001"
write  = true

[targets.drnmig3a]
host  = "…"
write = false                # read-only: no deploy, no submit

[targets.mvsce-lab]
docker = "ghcr.io/mvslovers/mvsce-builder:2026.09"   # `mbt mvs up` starts it
```

The project names a default target, never a credential:

```toml
[deploy]
target = "mvsdev"
```

- Selection: `--target NAME` or `MBT_TARGET=NAME`; `mbt test --mvs --target all`
  prints the pass/fail matrix per target.
- `mbt target list | ping | add`; `mbt doctor` checks every target.
- Passwords come from `password_cmd` (keychain, `pass`, a password manager)
  or, in CI, from `MBT_TARGET_<NAME>_PASSWORD`. Never plain text in a file.
- The SMP-owned `<PROD>.LINKLIB` is never a deploy destination; `mbt deploy`
  writes only to the DEV library.
- Every MVS-facing output names the **target and the job id**, which is what
  our own rule "name the stands, with job numbers, every time" asks for.

## 8. Toolchain, sysroot and artifact metadata — **largely done** (toolchain side)

Goal: the toolchain is pinned per project and identical locally and in CI,
and every prebuilt artifact says what it was built with.

### 8.0 Status (2026-10-03)

The toolchain half of this section has been carried out in cc370 and libc370;
what is left is mbt's own half.

| Item | State |
|---|---|
| cc370 releases with SemVer, one version for all tools | **done** — v1.0.0, v1.1.0, v1.1.1; `cc370 --version` prints `cc370 <v> (<sha>), based on GCC 3.4.6` (cc370#523, #700) |
| `__CC370__` numeric version | **done** — cc370#704, from 1.1.0 |
| Compiler helpers out of libc370 (`libcc370rt.a`), prologue macros to cc370 | **done** — cc370#687, #688 (1.1.0), libc370#313 (2.1.0); verified on MVS (cc370#687: JOB01185; brexx370 126/126 with the helpers from `libcc370rt.a`) |
| libc370 sysroot tarball + `metadata.json` with the cc370 range | **done** — libc370 2.1.0 (libc370#326); every header checks `__CC370__` (libc370#315) |
| Packages and channels | **done** — tarballs, `.deb`/`.rpm`, `install.sh`, Homebrew tap (cc370#699), a CI pair test of both packages (libc370 `pair.yml`) |
| The rules between the two | **done** — cc370 `docs/releasing.md`, libc370 `doc/releasing.md` |
| A second sysroot (libc370 outside cc370's own files) | **done** in cc370 `main` (cc370#726, for 1.2.0): cc370 also searches `cc370/libc370/{include,lib,macros}` |
| mbt links the runtime | **done** — mbt#137 (`-lcc370rt` before `-lc`), in v2.0.0 |
| `[toolchain] cc370 = …` resolvable | **possible** — cc370 has tags; mbt v2 resolves them for release builds |
| Artifact metadata for *project* archives, transitive dependencies | **open** — mbt's part (8.4); libc370's `metadata.json` is the template |
| libc370 at `-Os` | **done** on libc370 `main` (libc370#344), not yet released |

### 8.1 Before (as of 2026-10-02)

- **cc370** has no tags and no releases. The version is fixed in the Makefile
  (`VERSION ?= 1.0.0`) and `cc370 --version` prints a build date, not a
  version (mvslovers/cc370#523). Every consumer builds it from `main`.
- **libc370** has SemVer, tags and GitHub releases since its decision D6, but
  no release assets. It is built with whatever cc370 is on `PATH` and installs
  into cc370's sysroot (`<prefix>/cc370/{include,lib,macros}`) — including
  the assembler macros (`sysmac/` = SYS1.MACLIB, plus libc370's own), which
  as370 finds through `<exedir>/../macros`.
- **The two are coupled for a specific reason:** the compiler emits calls to
  runtime helper routines (`@@FXUNSF` and others), and those routines live in
  libc370. When the compiler renamed them (libc370#190), libc370 1.0.7 needed
  cc370 at `f3f7e21` or later.
- **Prebuilt dependency archives carry no provenance.**
  `ufsd-1.4.0-dev-lib.tar.gz` holds `lib/libufs.a` and two headers — nothing
  says which cc370 or libc370 built it.

### 8.2 How others do it

| Model | Who | How |
|---|---|---|
| Separate projects, the sysroot as interface | GCC + glibc | own versions and release cycles; the compiler finds the libc through `--sysroot`; glibc names a minimum GCC; distributions, crosstool-NG, Buildroot or Yocto assemble matching sets |
| One bundle, one version | ARM GNU Toolchain, Android NDK, rustup | compiler, tools and libc/std ship together; Rust accepts only the std built by that exact rustc |
| Compiler and SDK separate, with a compatible range | clang + Apple SDKs | clang is a cross compiler by design (`--target`); the libc comes as a versioned SDK (a sysroot), and one compiler supports a range of SDKs |
| libc built from source on demand, cached | `zig cc` | ships libc sources and builds the libc for a target on first use |

The difference that matters most: in GCC and clang, the helper routines the
compiler itself calls **belong to the compiler** — libgcc (`__udivdi3` and
friends) and compiler-rt ship with it. The libc only provides the C library.
So a compiler can change its helpers without a libc release.

For prebuilt dependencies there are two common answers: cargo always builds
dependencies from source with the consumer's compiler, so binary mismatches
cannot occur; Conan, the C/C++ package manager, tags every binary package with
the compiler, its version, the libc and other settings, and builds from source
when no binary matches.

### 8.3 Proposal

1. **Move the compiler's helper routines from libc370 into cc370** — a
   compiler runtime library in the role of libgcc. This removes the coupling
   at its cause instead of managing it. First step: an inventory of exactly
   which symbols the compiler emits calls to. The startup objects (`crt0`,
   `crt1`, `crtm`) stay in libc370, as `crt1.o` does in glibc.
2. **cc370 releases** as proposed in mvslovers/cc370#523: SemVer, one version
   for all tools, binaries for linux/darwin × amd64/arm64. The installation is
   already relocatable — cc370 finds everything relative to its own binary.
3. **libc370 ships a sysroot tarball** per release (headers, `libc.a`,
   `crt*.o`, macros), built with a named cc370 and declaring the cc370 range
   it needs, e.g. `cc370 >=1.2 <2`. That is the clang + SDK model; after
   step 1 the range is wide and rarely moves.
4. **mbt pins both separately and checks the range.** As a fallback, in the
   spirit of zig, mbt builds libc370 itself at the pinned tag with the pinned
   cc370 and caches the result, keyed by both versions. `sdk/mklibc.py` says
   regenerating all 712 assembler files takes about 7 seconds; the full build
   is not measured yet. If it stays that small, the tarball is an
   acceleration, not a requirement.
5. **Every published artifact carries metadata** (8.4), and the resolver uses
   it: it only picks releases built against a compatible toolchain, and can
   fall back to building a dependency from its tag.

```
~/.mbt/toolchains/cc370/<version>/
~/.mbt/sysroots/libc370/<version>+cc370-<version>/
```

### 8.4 Artifact metadata

mbt v1 had this: a `package.toml` published next to every release
(`internals/mvs-build-spec-v1.0.0.md`, §7), with the mbt version, the package's
own dependencies, its artifacts, the datasets it provides and its link
exports. It was dropped when v1 became legacy, and two things went with it:

- **No provenance** — nothing says which toolchain built an archive.
- **No transitive dependencies.** mvsmf needs httpd, and httpd needs ufsd and
  crypto370 — yet mvsmf has to list all three itself, because no artifact
  says what it depends on.

**An interchange format, not a storage format.** Whatever is written gets
readers: `install.sh` already reads libc370's `metadata.json`, and package
tooling, scanners or a Homebrew automation will follow. So:

- the schema is **documented** (a schema file in the mbt repository) and
  **versioned** (`"schema"`), with a compatibility promise: fields are added,
  never renamed or repurposed within a schema version;
- it carries nothing mbt-internal — only what describes the artifact;
- for the provenance and content parts, existing standards are checked before
  we invent our own: SPDX or CycloneDX (bill of materials), SLSA / in-toto
  (build provenance). Adopting one in full is not required, but not adopting
  one is a decision to write down.

**Proposed format** — JSON:

```json
{
  "schema": 1,
  "name": "ufsd",
  "version": "1.4.0",
  "commit": "abc1234",
  "built_with": { "mbt": "3.0.0", "cc370": "1.2.0", "libc370": "2.0.0" },
  "dependencies": { "mvslovers/crypto370": "1.0.1" },
  "provides": {
    "headers": ["include/libufs.h", "include/ufsdrc.h"],
    "libs": ["lib/libufs.a"],
    "modules": ["UFSD", "UFSDSSIR", "UFSDCLNP", "UFSFMT"]
  },
  "files": { "lib/libufs.a": "sha256:…", "include/libufs.h": "sha256:…" }
}
```

(ufsd has no dependencies today; the entry only shows the shape.)

**Where it lives** — the same content twice:

- **Inside the archive:** `.mbt/metadata.json`. `.mbt/` is a namespace
  reserved for mbt, with room for what may come later (an SBOM, signatures,
  licence data, build flags). It travels with the bits, and the SHA-256 in
  the consumer's `mbt.lock` covers it.
- **As a release asset:** `<project>-<version>-metadata.json`, e.g.
  `ufsd-1.4.0-metadata.json`. The resolver reads candidates' metadata from
  here without downloading their archives.

**What the resolver does with it:**

- **Transitive resolution:** mvsmf declares httpd; ufsd and crypto370 follow
  from httpd's metadata.
- **Toolchain compatibility:** a dependency must have been built against the
  same libc370 major version as the consumer. Example: httprexx pins libc370
  1.0.8 and requires `ufsd >= 1.2.2`. Today the resolver takes the highest
  matching release — once ufsd 1.4.0 (built against libc370 2.0) is out, that
  is what httprexx would link, and nothing would notice. With metadata, mbt
  skips 1.4.0 and takes the highest compatible release, or fails with
  "ufsd >= 1.2.2: no release built against libc370 1.x" — or builds ufsd from
  its tag with httprexx's own toolchain (step 4). Cargo's resolver works the
  same way when it skips versions that need a newer Rust than the project
  declares.
- **Releases without metadata** are treated as "unknown": accepted with a
  warning, so nothing breaks during the transition.

### 8.5 Still open

- ~~Does the driver support `--sysroot`?~~ Answered differently: cc370#726
  adds a fixed second sysroot, `cc370/libc370/`, searched after cc370's own
  tree (headers, `crt0.o` and `-lc`, macros). **Open for mbt:** that still
  ties a libc370 to one cc370 installation. Pinning libc370 per project with
  a shared cc370 needs either one cc370 tree per (cc370, libc370) pair under
  `~/.mbt/toolchains/`, or an explicit sysroot option in the driver.
- A nightly channel: today's PR builds float on `main` as an early warning;
  `mbt build --toolchain nightly` in CI would keep that while releases stay
  pinned.
- `mbt toolchain link dev <path>` to build projects against a local
  cc370/libc370 checkout.
- ~~The compatibility rule in detail.~~ Decided and documented in both
  `releasing.md` files: libc370 declares its cc370 range; a cc370 major is an
  incompatible change to generated code or the ABI. Prebuilt *project*
  archives still need mbt's metadata (8.4) to be checked against it.

## 9. Extensions — **Decided: Lua** (version: **Open**)

Some projects need things mbt does not do. Instead of a Makefile next to the
project file, an extension is a Lua script that works through a context mbt
provides — never through a raw shell — so mbt knows its inputs and outputs and
can build incrementally and report honestly.

```lua
-- tasks/webroot.lua
mbt.task {
  name    = "webroot",
  inputs  = { "static/**" },
  outputs = { "build/webroot/httpd-webroot.img" },
  before  = { "package", "dist" },
  run = function(ctx)
    local ufs = ctx.tool("ufsd-utils")        -- declared in [tools], pinned in mbt.lock
    ctx.exec { ufs, "create", ctx.out[1], "--size", "1M", "--blksize", "4096" }
    ctx.exec { ufs, "cp", "-r", "static/", ctx.out[1] .. ":/" }
  end,
}
```

- `ctx.exec` takes an argv list, no shell — the class of bug where zsh does
  not word-split `$VAR` cannot happen.
- `ctx.tool(name)` resolves a tool **declared in the project file**, never a
  repository and version written into the script:

  ```toml
  [tools]
  ufsd-utils = { repo = "mvslovers/ufsd-utils", version = "1.0.1" }
  ```

  One place for every dependency — libraries, toolchain, tools — all pinned
  with their SHA-256 in `mbt.lock`, all reported by `mbt outdated` (suggested
  in the discussion of this proposal).
- `ctx.mvs` reaches mvsMF with the same per-target permissions as everything
  else; `ctx.project` is read-only.
- `mbt.hook("pre-release", fn)` hooks into existing phases;
  `mbt.command("deploy-desktop", fn)` becomes `mbt run deploy-desktop`.
- Extensions can be packages: `[plugins] "mvslovers/mbt-ufs" = "^1"`, resolved
  like dependencies, so httpd and httplua share one webroot plugin.

What today's Makefiles do and where it would go:

| Today | Where | Proposed |
|---|---|---|
| Docker `run-mvs` / `stop-mvs` | mvsmf, rexx370, nsf370 (near-identical copies) | built in: `mbt mvs up/down` |
| `buildid.h` from the git hash | mvsmf | dropped — mbt already generates `buildstamp.h` |
| Webroot UFS image with pinned ufsd-utils | httpd | Lua task |
| Desktop upload into the UFS | mvsmf | Lua command |

**Open: Lua 5.1 or 5.4.**

| Option | For | Against |
|---|---|---|
| gopher-lua (pure Go, Lua 5.1) | mature; trivial cross-compilation | a different Lua than lua370 on MVS |
| Lua 5.4 reference implementation via cgo | the same Lua as lua370 | CI needs native runners per platform instead of `GOOS=… go build` |
| A pure-Go Lua 5.4 | both of the above | maturity unknown — needs evaluation |

## 10. Languages — **Proposed**

The build engine treats languages as rules, not as a special case for C:

```
prog.cbl ──cobc370──▶ prog.asm ──as370──▶ prog.o ──ld370──▶ load module
foo.c    ──cc370─────────────────────────▶ foo.o ──┘
bar.asm  ──as370─────────────────────────▶ bar.o ──┘
```

A language is: file extensions, a pinned tool, what it emits (`.asm` or `.o`),
how dependencies are discovered, and flags. C and assembler are built in.

**Rules are data, and a project can override or extend them** — the lesson of
the original Unix `make`, raised in the discussion of this proposal. They come
in two kinds, and the split is what keeps the outcome predictable when patterns
overlap:

- A **rule** says *how* a file is built: which tool, what it emits. Rules are
  keyed by extension only, so exactly one rule applies to a file.
- **File settings** say *with what*: flags and the like. They take paths and
  globs, more than one may match a file, and they layer.

```toml
# a rule: replace the built-in one for an extension
[rule."*.asm"]
backend = "ifox00"                 # native, section 11

# a rule: a new extension -- a generated source, then the ordinary C rule
[rule."*.msg"]
run    = ["tools/msgc", "{in}", "-o", "{out}"]
output = "{stem}.c"

# file settings: a directory, then one file
[files."src/vsam/*.c"]
cflags = ["-O1", "-DVSAM_TRACE"]

[files."src/vsam/hot#loop.c"]
cflags = ["-O2"]
```

**Rules cannot overlap by construction.** A rule key is `*.` followed by an
extension and nothing else; `[rule."src/*.asm"]` is a configuration error. The
built-in rules — `*.c` → cc370 → `.o`, `*.asm` → as370 → `.o` — are defaults a
project rule of the same extension replaces. Where extensions nest, the longest
one wins (`*.tar.gz` before `*.gz`): a fixed order, not a judgement. A file
that needs a different tool than its extension's is renamed, or built by a Lua
extension (section 9) — the place for logic.

**File settings may overlap, and resolve in a fixed order.** For each key, from
lowest to highest:

1. the rule's defaults,
2. the project (`[build]`),
3. the module (`[module.X]`),
4. every `[files."<glob>"]` that matches,
5. a `[files."<path>"]` naming the file exactly.

A higher layer replaces a key; it does not append to it. **Two globs that match
the same file and set the same key are an error** (exit 2) naming both entries
and the file — mbt does not guess which one was meant, and declaration order is
deliberately not a tie-breaker, since TOML does not promise one. Globs that
match the same file but set different keys combine without conflict.

**`mbt explain <file>`** prints the result for one file: the rule that builds
it, every setting with the layer it came from, and the resulting command line.
`compile_commands.json` (section 14) is written from the same resolution, so
the editor sees the flags the build uses.

- A file's rule and its resolved settings are inputs of that file: changing them
  rebuilds exactly the files they reach (the v2 gap mbt#65).
- A rule that needs logic is a Lua extension (section 9) registering a rule,
  through the same context and the same input/output tracking.

**COBOL via [cobc370](https://github.com/brazilofmux/cobc370)** — a COBOL-74
compiler that also builds as a host cross-compiler and emits S/370 assembler
with a self-contained runtime — is the first candidate. Further languages
could be defined as Lua extensions.

```toml
[toolchain]
cobc370 = "1.0.0"

[module.GLMONTH]
sources = ["cobol/glmonth.cbl", "src/vsamshim.c"]   # the extension decides the language

[lang.cobol]
copybooks = ["copy/"]
```

To be measured before we commit (questions for the cobc370 maintainer, and
experiments):

- Does **as370** accept cobc370's output? cobc370 targets IFOX00. Compare
  object decks from both assemblers on cobc370's own test suite.
- Can cobc370 write a **dependency file** for `COPY` members (like `-MMD`)?
  Otherwise mbt has to scan `COPY` statements itself.
- **Mixed COBOL and C** in one load module: calling conventions both ways, and
  whether two COBOL parts with an embedded runtime collide on symbols.

Non-compiled artifacts become first-class as well: copybooks, macros, JCL,
PROCs, REXX execs, CLISTs. They land as FB80 members in a PDS and travel in the
SMP package as `++MAC` / `++SRC`.

## 11. Backends: cross and native — **Open** (bonus)

The default and the focus stay the cc370/as370/ld370 host path. This section
is about what could come on top of it.

Today `[project] type` says **what** a project produces: `library`, `module`
or `application`. **How** it is built is fixed:

- **v2 (today):** everything on the host — cc370 and as370 compile and
  assemble, ld370 links.
- **v1 (legacy):** a hybrid — c2asm370 on the host, IFOX00 and IEWL on MVS
  through JCL over mvsMF (`scripts/legacy/mvsasm.py`, `mvslink.py`,
  `build-legacy.yml` with MVS/CE in a container). No project uses it any more,
  but its JCL templates (`asm-step.jcl.tpl`, `link.jcl.tpl`) are still in
  `templates/jcl/`.

So the native half existed once and was dropped with v2.

### 11.1 Two axes instead of one project type

A single type per project ("COBOL, cross") is too coarse: mixed projects are
the rule — ufsd is C plus assembler, a COBOL program often needs an assembler
shim. The proposal separates two things:

1. **What is produced** — library, module, application. Unchanged.
2. **How and where each language is built** — a *backend* per language,
   running either **cross** (on the host) or **native** (on a target, as JCL
   over mvsMF):

| Language | Cross (host) | Native (MVS) |
|---|---|---|
| Assembler | as370 | IFOX00 |
| C | cc370, JCC, GCC/MVS (gccmvs) | JCC, GCC/MVS (gccmvs) |
| COBOL | cobc370 | cobc370, IKFCBL00 |
| Link | ld370 | IEWL |

A backend declares what it emits — assembler source (an assembler backend
follows), object decks, or load modules — so the build graph stays the same
whichever backend is chosen.

### 11.2 One interface, one implementation per target system

The CMS port of GCC has a lesson for a later VM/CMS target (raised in the
discussion of this proposal): the interface between generated code and the
runtime is defined **at the macro level** — the prologue/epilogue macros and
the named helper routines — and implemented separately for PDPCLIB (MVS) and
GCCLIB (CMS). It does not cover every case: the compiler calls the stack
manager directly, but by name, through a V-constant.

cc370 has taken the first steps (cc370#687: the helpers by name in
`libcc370rt.a`; cc370#688: the prologue macros belong to the compiler;
cc370#689: public macros for hand-written assembler). A CMS target then means a
second implementation of the same macros and helper names, chosen by the
target — not a second compiler. For mbt that is a **toolchain profile** (next
section) per target system. The design work belongs to cc370's plan
(`docs/runtime-and-release-plan.md`, Phase 3).

### 11.3 Toolchain profiles

The freedom has one hard limit: **the runtime**. Objects from JCC and from
cc370 cannot be mixed at will, because each compiler brings its own C library.
So a project picks a **toolchain profile** that fixes the runtime and the
default backends, and may override single languages:

```toml
[toolchain]
profile = "cc370"          # or "jcc", "gccmvs" — fixes runtime and default backends

[lang.asm]
backend = "ifox00"         # this project assembles natively
```

### 11.4 What it buys

- **Users of the established toolchains** (JCC, GCC/MVS, IFOX00, IKFCBL00)
  get everything mbt does around the compiler — dependencies, lockfile, tests
  with a result matrix, targets, SMP packaging, releases, CI — without
  switching compilers. That is most of what mbt is.
- **Comparisons against the original come almost for free.**
  `mbt build --backend asm=ifox00` plus a diff of the object decks shows
  whether as370 produces what IFOX00 produces — exactly the measurement
  section 10 asks for cobc370. The same works for cobc370 against IKFCBL00.

### 11.5 Bring your own build system

A separate category: mbt calls a foreign build system — Make, a Lua
extension, the `build/*` scripts of BREXX 2.5.3 — and takes over from there.
The design question is the **contract**:

- **What mbt passes in:** paths of the resolved dependencies, the target, the
  version, the output directory — for example as environment variables.
- **What mbt expects back** — and that decides how much mbt can still do:
  - **object decks:** mbt links, checks attributes, measures sizes, packages;
  - **finished load modules or an XMIT:** mbt can only package, deploy, test
    and release.

```toml
[build]
external = { run = ["make", "-C", "build"], produces = "objects", outputs = ["build/obj/*.o"] }
```

"make" would be just a preset; Lua is the general vehicle, because an
extension can declare what it returns.

### 11.6 What it costs

- Native backends need a target with write permission, and builds get much
  slower. In CI that means MVS/CE in a container, as `build-legacy.yml` did.
- Macro libraries live in two places — on the host for as370, as a SYSLIB
  PDS on MVS for IFOX00. mbt has to feed both from one source.
- Incremental builds keep working: the dependency graph stays on the host;
  only the execution of a step moves to MVS.

## 12. MCP server for mvsMF — **Proposed**

`mbt mcp` starts an MCP server over stdio. It builds on what mbt already has to
talk to mvsMF (submit, spool, datasets, members, binary upload).

**Two layers of tools**

- **mvsMF access:** datasets, members, jobs, spool — works outside a project.
- **Project-aware:** `build`, `deploy`, `test_mvs` (pass/fail matrix),
  `job_log`, `smp_list(fmid)`, `fmid_check`.

**It enforces our hard-won rules instead of documenting them:**

| Rule today (prose) | What the server does |
|---|---|
| `<PROD>.LINKLIB` belongs to SMP | refuses to write to it |
| `^` does not survive the mvsMF REST API | rejects JCL containing `^` before submitting, pointing at `NE` |
| Verify an install by the member list, not by the return code | install tools return `HMA2380` lines and the target library's member list, not just a condition code |
| A deleted FMID answers `LIST` with RC 00 and is not free | `fmid_check` reads the stanza (`DELBY`) instead of the return code |
| Name the stands, with job numbers | every answer carries target and job id; `fmid_check` runs against all targets |

Read-only by default; write and submit are enabled per target (`write = true`).

## 13. More tooling — **Proposed**

- **Workspaces** (**Open**, see section 17). A workspace file (for example in
  the directory that holds all project checkouts) lets libc370, crypto370,
  ufsd, httpd and mvsmf build together against their local states, replacing
  `.mbt/deps.local.toml`. Unlike cargo, our members live in separate
  repositories, which raises its own questions.
- **`mbt lint`** — static checks for what our rules forbid:
  - assembler: name field columns 1–8, continuation in column 72, nothing past
    column 71, sequence field 73–80, no tabs, labels ≤ 8 characters;
  - JCL: column 71/72 rules, names ≤ 8 characters, `^`, lower case outside
    instream data;
  - names: DSN ≤ 44, qualifiers ≤ 8 and starting alphabetic/national, member
    names ≤ 8, FMID components ≤ 9;
  - encoding: valid UTF-8, characters with no CP037 mapping in C literals,
    the bytes that do not round-trip through IBM-1047;
  - C: hard-coded ASCII codes, VLAs, Unix paths, POSIX calls, external names
    that collide once truncated to 8 upper-case characters (to check whether
    cc370 already catches this);
  - line length for everything that ends up as FB80.
- **`mbt size`** — size per module and CSECT from the linker map, the
  difference to the last release, optional `max_size` per module that fails
  CI. Memory is our first priority and today nothing makes it visible.
- **Load module attribute check** after linking: AMODE/RMODE, RENT/REUS, AC
  against what the project file declares.
- **`mbt smp verify --target NAME`** — an automated install test with a
  throwaway FMID *and* throwaway module names: RECEIVE, APPLY, ACCEPT, then
  check `HMA2380` and the member list, then clean up every spent id by UCLIN.
  This is what was done by hand several times in September 2026.
- **Golden-output tests** next to return-code tests: compare SYSOUT with an
  `.expected` file (the way cobc370 tests itself).
- **Pinned MVS/CE image** per target, so the test system is reproducible too.
- `mbt outdated`, `mbt tree`, `mbt self update`, shell completion.

## 14. IDE integration — **Proposed** (core)

The editors in use are nvim and VS Code, both with clangd, and JetBrains CLion.
All three have to load a project cleanly and build it.

### 14.1 What exists

The common denominator is `compile_commands.json`: clangd reads it directly,
and CLion opens it as a *compilation database project*. mbt already writes one
(`make compiledb`), deliberately as a **clang** database rather than a cc370
one:

- `--target=powerpc-unknown-eabi` gives clang cc370's data model — ILP32,
  big-endian, unsigned `char`, `size_t` as `unsigned long` (#119);
- `arguments[0]` is `clang`, because CLion actually runs it to query
  built-in macros, and cc370 rejects the clang flags (#118).

### 14.2 What is missing

1. **The database goes stale.** Today it is a separate step; a new `.c` file
   is unknown to clangd until someone runs `make compiledb` again. mbt 3
   writes it on every `mbt build`, the way CMake does with
   `CMAKE_EXPORT_COMPILE_COMMANDS`.
2. **Building from the IDE.** With the database the project is *loaded*, not
   yet *buildable*:
   - **nvim:** `makeprg=mbt build`, errors in the quickfix list. mbt reports
     errors as `file:line: error: …`; cc370 is GCC 3.4.6 and prints no
     column, so the `errorformat` has to match that.
   - **VS Code:** `.vscode/tasks.json` with `mbt build`, `mbt test` and
     `mbt deploy` as tasks, plus a problem matcher for the same format.
   - **CLion:** a compilation database project supports *custom build
     targets*; build and clean are `mbt build` and `mbt clean`. They live
     under `.idea/` — the exact files are still to be checked.

   `mbt ide nvim|vscode|clion` writes these files once, and a `.clangd` where
   wanted. Today mvsmf and brexx370 carry hand-written `.clangd` files that
   differ from each other.
3. **Debugging host tests.** MVS load modules cannot be debugged in any of
   the three IDEs, but host tests (`mbt test`, today `make test-host`) are
   native programs. mbt can generate run/debug configurations for them
   (`launch.json`, a CLion run configuration). One catch: host tests are
   compiled with different flags, and clangd reads one database — the editor
   view stays that of the cross build.

### 14.3 The project file in the editor

No IDE knows `mbt.toml` by itself. mbt publishes a **JSON schema** for it;
Taplo, the TOML language server, uses it for completion and validation in
nvim and VS Code. How CLion's TOML support handles schemas is still to be
checked. For a Lua project file the equivalent would be type annotations for
lua-language-server (section 6.3).

## 15. CI and code quality — **Proposed**

The reusable workflows stay, and get simpler: checkout, install the launcher,
`mbt deps && mbt package`. Their `uses:` tag matches `[toolchain] mbt`
(section 5), and the toolchain comes as binaries (section 8).

**Static analysis is part of what mbt sets up**, not something each project
discovers on its own. brexx370 shows why: it is analysed by SonarQube Cloud
(org-wide GitHub App, Automatic Analysis), and without help the analyzer
assumes a 64-bit host and reports every pointer/`int` cast as a truncation.
brexx370 fixes that by hand in `.sonarcloud.properties`: a 32-bit big-endian
target with unsigned `char` (`powerpc-unknown-eabi`, the same triple
`mbt compiledb` already uses) and `#define __MVS__ 1`. Every other project in
the organisation would need the same settings.

- `mbt new` / `mbt migrate` write the analyzer configuration with the cc370
  target model, so every project gets correct results from day one.
- `mbt compiledb` already writes `compile_commands.json`. SonarQube's C/C++
  analyzer can read such a compilation database in a **CI-based** analysis
  (to be confirmed for our setup), which would see the real include
  paths, defines and dependency headers instead of guessing — at the cost of
  running the scanner in our workflow instead of the zero-setup Automatic
  Analysis. A switch in the reusable workflow, not a per-project job.
- `mbt lint --sarif` uploads its findings to GitHub code scanning, so the
  MVS-specific rules (section 13) show up as PR annotations next to Sonar's.
- Rule tuning that is right for the whole ecosystem (for example c:S1172,
  which does not accept `__attribute__((unused))` and duplicates `-Wextra`)
  is decided once and shipped by mbt, not per repository.

Generalised: the reusable workflows get named, opt-in **quality steps**
(Sonar, lint, size budget, MVS tests on a container) that a project switches
on in its project file instead of copying workflow YAML.

## 16. Migration and acceptance — **Proposed**

- **Differential acceptance.** Old and new mbt build every active project
  **side by side**: in the same checkout, on the same staged dependencies,
  with the clock pinned. Object decks, load modules, rendered JCL and SMP
  packages must be identical. Not against the recorded manifest: once a
  `-dev` dependency is republished its old archive is gone and the manifest
  cannot be reproduced (`internals/v2-baseline.md`, measured 2026-10-05).
  Commands that touch MVS are compared against a local stand-in for mvsMF
  (requests, submitted JCL, console), then run once on a real system. The
  scripts are `internals/acceptance/` (#165, merged).
- **Behaviour to carry over, each with a test:** a failed recipe leaves no
  output behind (as370 writes a deck even at RC 8, and a later build would
  link it); header dependency tracking; the build stamp only rewritten when
  it changes; the sysroot version check; host flags not leaking into the
  cross build.
- **One project at a time.** A project still on the submodule keeps building
  unchanged; `mbt migrate` converts the project file; the Makefile and the
  submodule are removed in the same PR.
- **Spec revision.** `internals/mvs-build-spec-v1.0.0.md` describes the submodule
  (sections 1, 2, 13 and the CI flows). mbt 3 gets a new spec rather than a
  patched one.

## 17. Open questions

1. ~~**Project file format**~~ — decided 2026-10-05: TOML, plus an optional
   `mbt.lua` (section 6.3).
2. **Lua version for extensions:** 5.1 (gopher-lua) or 5.4 (cgo or pure Go)?
3. **Toolchain and artifacts** (section 8.5): per-project libc370 pinning
   with a shared cc370 (one tree per pair, or a driver option), and a nightly
   channel. The sysroot and the compatibility rule are settled (8.0).
4. **Workspaces:**
   - Where does the workspace file live — loose in the checkout directory, or
     in its own repository everyone clones?
   - When does a local member win over a release — always, or only when listed?
   - How do we guarantee a workspace path never reaches `mbt.lock` or a
     released artifact?
   - Is the toolchain (libc370) a workspace member?
5. **Lint scope** for the first version.
6. **COBOL:** as370 compatibility, dependency files, mixed-language linking —
   with the cobc370 maintainer.
7. ~~**Name of the project file**~~ — decided 2026-10-05: `mbt.toml` (section 6.1).
8. **Static analysis:** keep SonarQube Cloud's Automatic Analysis with a
   generated configuration, or move to CI-based analysis fed by
   `compile_commands.json`? Which rules do we tune ecosystem-wide?
9. **Backends:** is "toolchain profile plus per-language backend" the right
   model? Which native backends come first (IFOX00/IEWL — the v1 templates
   still exist)?
10. **Foreign build systems:** what is the contract — inputs, and whether
    mbt gets object decks or finished load modules back?
11. **Artifact metadata standards** (section 8.4): adopt SPDX/CycloneDX or
   SLSA/in-toto for the provenance and content parts, or document why not.
12. **PTF ids** (section 6.4): #93 planned `UUFS001…`, but `U` is IBM's
    service prefix. Candidates: `P` + the three product letters + the version
    (`PUFS141` for ufsd 1.4.1), or a serial. Checked free on MVS/CE and TK5
    with `LIST`, as for the FMIDs, before the first one is spent.

## 18. Phasing

1. ~~Toolchain groundwork~~ — **done on the toolchain side** (2026-10-03,
   section 8.0); mbt's artifact metadata moves into step 3.
2. ~~Settle the project file and the launcher~~ — **decided** (2026-10-05):
   TOML in `mbt.toml` plus an optional `mbt.lua` (section 6), and the
   launcher with `[toolchain] mbt` (section 5). The schema 3 details are
   settled (2026-10-06): `internals/mbt-3-schema.md`.
3. Go core for the cc370/as370/ld370 host path: build engine, dependencies,
   package/dist, deploy, tests, targets, IDE integration.
   Accepted by the differential comparison (core: #165, merged 2026-10-06).
   Before that comparison the v2 baseline is recorded again: it was taken
   with cc370 1.1.1 and libc370 2.1.0, and since then cc370 1.3.0 changed the
   eyecatcher in front of every `main`, libc370 2.3.0 moved the C startup
   into `libc.a`, and mbt 2.2.0 (#158) links it from there.
   Schema 3 and `mbt migrate` come in this step rather than step 4: building
   a migrated project against its own `project.toml` build is the acceptance
   of the schema.
4. Toolchain management, **project tasks** (section 9: `[tools]` and a task
   a project runs as part of its build), then migrate the projects one by
   one with `mbt migrate` (step 3). Tasks come before the migration because a
   project needs them to migrate at all: httpd builds its webroot image with a
   pinned `ufsd-utils` in its own Makefile (`make webroot`), which mbt 3
   cannot run otherwise (found in #165). PTF packaging (section 6.4) comes
   here too, after its measurements: step 3 ports v2's `++FUNCTION` path
   unchanged, because the differential comparison accepts exactly that. So
   do USERMODs (section 6.4), which need nothing from the PTF measurements.
5. Bonus, once the core stands: Lua extensions beyond tasks, MCP server,
   workspaces, `lint`, `size`, `smp verify`, languages, native backends and
   foreign build systems.

## Appendix A — open mbt v2 issues, sorted (2026-10-03)

Most open issues of mbt v2 are not loose bugs but **requirements** a new
implementation has to meet from the start. Sorted, so the tracker becomes the
checklist for mbt 3. "Fix in v2" marks the ones worth fixing now, because
they hurt today and the fix is small.

**Every input of a build step is a tracked prerequisite** — the build engine
(section 16 lists the behaviours to carry over):

| Issue | Input that is not tracked | Fix in v2 |
|---|---|---|
| #65 | compile flags | — |
| #103 | the libraries a module links (libc370, dependency archives) | yes — libc370 changes now happen |
| #117 | the object list (a source removed from `sources`) | — |
| #92, #66 | `deploy` neither builds first nor stamps the current commit | — |
| #134 | a failed compile leaves an unescaped `.d`, every later `make` fails | yes — blocks all work until the file is deleted |

**The mvsMF client** reports what happened, and speaks TLS: #56 (TLS), #61
(`doctor` passes on 4xx/5xx), #89 (unreachable server read as a long job), #90
(unreadable spool read as empty), #108 (read timeout escapes as a bare
exception — fix in v2: a crash in the middle of a run).

**Deploy without deleting the target library** — merge members instead (also
named in the ecosystem's root context): #105, #57.

**SMP/distribution** — the logic is ported, and these are fixed where it
lives: #84 (uninstall job), #93 (`make ptf` — section 6.4), #96 (alloc job run twice), #99
(re-run skips APPLY CHECK), #100 (DELETE across renamed libraries), #102
(`UNIT=SYSDA` vs. the APF volser), #104 (the FMID does not move — section 6.1
derives it), #115 (dropped aliases), #132 (per-library RECFM/LRECL).

**Tests on MVS:** #95 (an EXEC card past column 71 — what `mbt lint`, section
13, checks), #111 (an empty fixture line is dropped).

**Features that already have a place in this proposal:** #131 (a load map per
module → `mbt size`, section 13), #91 (the module-data check — **done in v2.1.0**,
`mbtmoddata.py`, generalised from AC(1) to RENT), #62 (startup resolution → also libc370#159),
#133 (`-Wall -Wextra -Werror` by default — fix in v2: the ecosystem rule is
already strict).

**Changed by the toolchain work:** #54 (a prebuilt toolchain container for CI)
— cc370 and libc370 now ship binaries and packages, so CI can install them
instead of building from source; what remains is the nightly question (8.5)
for the PR builds that deliberately float on `main`.

**Housekeeping, fix in v2:** #60 (docs still list the removed `runtime` type),
#85 (`prerelease` deletes a tag without checking it belongs to the project).
