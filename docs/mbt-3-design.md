# mbt 3 — Design Proposal

**Status:** draft for community discussion — nothing in here is implemented.
**Date:** 2026-10-02

This document proposes replacing today's mbt — a git submodule plus a Make
include — with a standalone, installable build tool in the spirit of cargo,
gradle or maven. It collects what we think is settled, what we propose, and
what is still open. Section 16 lists the open questions — that is where the
discussion should start.

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
| mbt has never been tagged | There is no version a project could ask for — only a commit SHA. |
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
- An extension mechanism for what mbt does not do (yet).
- Room for more than C and assembler (COBOL via cobc370 is the first candidate).
- An MCP server for mvsMF, so other tools can work with MVS safely.

**Non-goals**

- Changing the target: MVS 3.8j, 24-bit, cc370/as370/ld370 stay as they are.
- Changing how products install: SMP4 `++FUNCTION`, one FMID per release.
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
a project of its own; section 15 describes how we keep it honest.

## 5. Distribution and version pinning — **Proposed**

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
platform) through the same release pipeline it offers everyone else.

The reusable workflows move with it: a consumer pins `uses:` to the same tag
as `[toolchain] mbt`, so workflow and tool can no longer drift apart.

## 6. The project file — **Proposed** (format: **Open**)

### 6.1 Cleanup, independent of the format

- **Name:** `mbt.toml` (or `mbt.lua`, see 6.3), matching `mbt.lock`.
  A `schema = 3` field lets the launcher tell old from new.
- **Tests are discovered, not listed.** Every `test/*.c` is a test. Tests
  link against the project's internal archive by autocall, so a test entry no
  longer lists the sources it links. An entry is only needed for exceptions:
  MVS-only, PARM, fixtures. Today the MVS link of a test already autocalls
  `[internal]` where a project declares it, but the native host-test build
  (`make test-host`) compiles only the listed sources — which is why the lists
  exist. The host build has to get the same archive for this to work.
- **Defaults instead of boilerplate:** `startup = "crt1"`, `system = "Z038"`,
  `prereq = []`, `accept_fmid = true`, the DEV deploy library and the SMP
  library names (`<PROD>.LINKLIB`, `<PROD>.A…`, staging) follow the convention
  unless overridden.
- **The FMID is derived.** The project declares `prefix = "TUFS"`; mbt computes
  `TUFS140` from version 1.4.0 and `delete` from the previous release tag. It
  refuses a release where a version component exceeds 9. An explicit override
  stays possible (gaps such as `TUFS131` are legitimate). Today the id is
  hand-maintained, and forgetting to move it after `make release` is a
  documented trap.
- **The version lives in one place**, not in both `VERSION` and the project
  file.
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
| Editor/tooling support | schema validation, highlighting everywhere | highlighting; validation only by running it |
| One language for config **and** extensions | no — TOML + Lua | yes |
| Risk | — | evaluating a project file runs code; needs a sandbox (no `os`, no `io`) and determinism |

A middle path exists too: **declarative TOML, plus an optional `mbt.lua`**
that may add modules, tests and tasks programmatically (comparable to cargo's
`build.rs` or gradle's build scripts). Simple projects never see Lua; rexx370
can generate its 64 tests in five lines.

**Not considered further:**
- **JSON** — no comments. rexx370 has 683 comment lines in its project file,
  and many of them record *why* something is the way it is. Losing that is
  not acceptable. JSON remains the **output** format (`mbt metadata --json`,
  in the spirit of `cargo metadata`) for scripts and the MCP server.
- **YAML** — its implicit typing collides with exactly our data: `NO` and
  `ON` become booleans, `1.10` becomes the number `1.1`, `0750` is read as
  octal. FMIDs, versions and MVS names would be at risk all the time.
- **KDL** and similar — pleasant, but too little tooling and familiarity.

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

## 8. Toolchain and sysroot — **Open** (needs the cc370 and libc370 teams)

Goal: the toolchain is pinned per project and identical locally and in CI.

```
~/.mbt/toolchains/cc370/<version>/
~/.mbt/sysroots/libc370/<version>/
```

A project declares what it builds with; `mbt.lock` records the SHA-256 of what
was used. CI downloads binaries instead of building cc370 and libc370 from
source on every run.

**Why this is open:** cc370 and libc370 depend on each other (the compiler's
startup objects and runtime conventions on one side, the library built by that
compiler on the other). Whether they can be pinned independently, or only as a
matched pair, is for the two teams to decide. Questions for them:

1. Can cc370 publish binary releases (linux/darwin × amd64/arm64) with semver?
2. Can cc370 take an explicit sysroot (`--sysroot=DIR`, or reliable
   `-nostdinc`/`-isystem`/`-L`), so the compiler no longer finds libc370
   relative to its own binary?
3. Can libc370 publish a self-contained sysroot tarball per release
   (headers, `libc.a`, `crt*.o`, macros)?
4. How is compatibility expressed — a cc370 range declared by libc370, a
   matched-pair "toolchain release", or something else?
5. Is a nightly channel feasible? Today's PR builds deliberately float on
   `main` as an early warning; `mbt build --toolchain nightly` in CI would
   keep that while releases stay pinned.
6. For the toolchain developers themselves: `mbt toolchain link dev <path>`
   to build projects against a local cc370/libc370 checkout.

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
    local ufs = ctx.tool("mvslovers/ufsd-utils", "1.0.1")   -- fetched and cached
    ctx.exec { ufs, "create", ctx.out[1], "--size", "1M", "--blksize", "4096" }
    ctx.exec { ufs, "cp", "-r", "static/", ctx.out[1] .. ":/" }
  end,
}
```

- `ctx.exec` takes an argv list, no shell — the class of bug where zsh does
  not word-split `$VAR` cannot happen.
- `ctx.tool` fetches a pinned tool; `ctx.mvs` reaches mvsMF with the same
  per-target permissions as everything else; `ctx.project` is read-only.
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

## 11. Backends: cross and native — **Open**

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

### 11.2 Toolchain profiles

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

### 11.3 What it buys

- **Users of the established toolchains** (JCC, GCC/MVS, IFOX00, IKFCBL00)
  get everything mbt does around the compiler — dependencies, lockfile, tests
  with a result matrix, targets, SMP packaging, releases, CI — without
  switching compilers. That is most of what mbt is.
- **Comparisons against the original come almost for free.**
  `mbt build --backend asm=ifox00` plus a diff of the object decks shows
  whether as370 produces what IFOX00 produces — exactly the measurement
  section 10 asks for cobc370. The same works for cobc370 against IKFCBL00.

### 11.4 Bring your own build system

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

### 11.5 What it costs

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

- **Workspaces** (**Open**, see section 16). A workspace file (for example in
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

## 14. CI and code quality — **Proposed**

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

## 15. Migration and acceptance — **Proposed**

- **Differential acceptance.** Old and new mbt build every active project;
  object decks, load modules, rendered JCL and SMP packages must be identical
  except for timestamps. That is a measurable definition of "done" for the
  rewrite.
- **Behaviour to carry over, each with a test:** a failed recipe leaves no
  output behind (as370 writes a deck even at RC 8, and a later build would
  link it); header dependency tracking; the build stamp only rewritten when
  it changes; the sysroot version check; host flags not leaking into the
  cross build.
- **One project at a time.** A project still on the submodule keeps building
  unchanged; `mbt migrate` converts the project file; the Makefile and the
  submodule are removed in the same PR.
- **Spec revision.** `docs/mvs-build-spec-v1.0.0.md` describes the submodule
  (sections 1, 2, 13 and the CI flows). mbt 3 gets a new spec rather than a
  patched one.

## 16. Open questions

1. **Project file format:** TOML (B), Lua (C), or TOML plus optional `mbt.lua`?
2. **Lua version for extensions:** 5.1 (gopher-lua) or 5.4 (cgo or pure Go)?
3. **Toolchain pinning:** cc370 and libc370 separately, or as a matched pair?
   Versioning and release format — with the cc370 and libc370 teams.
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
7. **Name of the project file:** `mbt.toml` / `mbt.lua`, or keep `project.toml`?
8. **Static analysis:** keep SonarQube Cloud's Automatic Analysis with a
   generated configuration, or move to CI-based analysis fed by
   `compile_commands.json`? Which rules do we tune ecosystem-wide?
9. **Backends:** is "toolchain profile plus per-language backend" the right
   model? Which native backends come first (IFOX00/IEWL — the v1 templates
   still exist)?
10. **Foreign build systems:** what is the contract — inputs, and whether
    mbt gets object decks or finished load modules back?

## 17. Phasing

1. Agree on toolchain versioning with the cc370 and libc370 teams — everything
   else builds on it, and it depends on others.
2. Settle the project file (format and schema 3) and the launcher — the two
   decisions that are hardest to undo.
3. Go core: build engine, dependencies, package/dist, deploy, tests, targets.
   Accepted by the differential comparison.
4. Toolchain management, `mbt migrate`, then migrate the projects one by one.
5. Extensions, MCP server, workspaces, `lint`, `size`, `smp verify`, languages,
   native backends and foreign build systems.
