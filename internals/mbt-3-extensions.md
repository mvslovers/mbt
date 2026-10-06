# mbt 3 — extensions, targets and tests

**A proposal for discussion** (2026-10-06). It belongs with the mbt 3 design
proposal (`internals/mbt-3-design.md`, PR #136) and settles three parts the
design left open or sketched: how projects extend mbt, how mbt reaches an MVS
system, and how tests run against one. Nothing here is built yet. We'd like your
comments before it is.

## In short

- **Extensions are Lua 5.4**, run inside mbt (a pure-Go implementation, so mbt
  stays one binary). A project puts its Lua in `mbt/init.lua`; you put yours in
  `~/.mbt/init.lua`; shared pieces come as **plugins**, pinned like dependencies.
- Lua can **hook** into fixed points (`before_deploy`, `after_test`, …), declare
  **tasks** with inputs and outputs, and add **commands** (`mbt run NAME`). It can
  act, but it never changes the build itself.
- **Targets** — the MVS systems you work with — live in `~/.mbt/targets.toml`,
  with optional Hercules, SSH and TN3270 access next to the mandatory mvsMF. CI
  sets them through environment variables. `.env` goes away.
- mbt talks to MVS through **one mvsMF session** per run, and sends operator
  commands through **mvsMF, falling back to the Hercules web console**.
- Three kinds of test: on the host, in **batch** on MVS, and **interactive** in
  TSO over TN3270 (`mbt test --tso`), with a TN3270 client built into mbt.

## 1. Why

Every mbt project today is a `Makefile` of two lines plus a `project.toml` —
except where it isn't. httpd builds a webroot image with a pinned tool, mvsMF
uploads its desktop with a shell script, two projects carry the same Docker
targets to start an MVS, brexx370 builds an SMP usermod with scripts of its own.
Each of these is a few lines of `make` and shell that nobody else can reuse, that
mbt cannot see into, and that breaks quietly (a `$VAR` that zsh does not split is
the classic). mbt 3 replaces the submodule with a single binary, so these lines
need a new home. That home is a small, safe Lua interface.

## 2. Extending mbt in Lua

### Where the Lua lives

| Level | File | For |
|---|---|---|
| Project | `mbt/init.lua`, more files in `mbt/lua/` (`require("webroot")` loads `mbt/lua/webroot.lua`) | what the project needs: a generated image, an upload, a usermod stream |
| You | `~/.mbt/init.lua` | what only you need: how *your* MVS starts, a notification after a release |
| Plugin | `[plugins]` in `mbt.toml`, then `require("owner/repo")` | what several projects share |

The layout follows Neovim (`init.lua`, `lua/`). `.mbt/` stays what it is today:
gitignored build state. (This replaces the "optional `mbt.lua`" of the design
proposal.)

### Hooks, tasks, commands

**Hooks** run at fixed points, one before and one after each command:

| Point | e.g. |
|---|---|
| `before_build` / `after_build` | generate a source; report module sizes |
| `before_test` / `after_test` | prepare test data; pass results on (`ctx.kind` is `host`, `mvs` or `tso`) |
| `before_package` / `after_package` | build an image that ships in the package |
| `before_dist` / `after_dist` | write an SMP usermod stream |
| `before_deploy` / `after_deploy` | stop a server / start it again |
| `before_release` / `after_release` | refuse a release without a CHANGELOG section / announce it |
| `on_failure` | collect logs from any failed command |

```lua
-- mbt/init.lua (httpd): after a deploy, restart the server that runs it
mbt.hook("after_deploy", function(ctx)
  ctx.console("P HTTPD")
  ctx.console("S HTTPD")
end)
```

**Tasks** are hooks with declared inputs and outputs, so mbt skips them while
nothing changed and removes their outputs when they fail:

```lua
mbt.task {
  name = "webroot", before = { "package", "dist" },
  inputs = { "static/**" }, outputs = { "build/webroot/httpd-webroot.img" },
  run = function(ctx)
    local ufs = ctx.tool("ufsd-utils")          -- pinned in [tools], see below
    ctx.exec { ufs, "create", ctx.out[1], "--size", "1M", "--blksize", "4096" }
    ctx.exec { ufs, "cp", "-r", "static/", ctx.out[1] .. ":/" }
  end,
}
```

**Commands** become `mbt run NAME [-- ARGS]`:

```lua
mbt.command("deploy-desktop", function(ctx)
  ctx.exec { "tools/deploy-desktop.sh", table.unpack(ctx.args) }
end)
```

### What Lua sees

Only `ctx` — no `os`, no `io`, no network of its own:

| | |
|---|---|
| `ctx.project` | read-only: name, version, modules, tests |
| `ctx.target` | the selected system: hosts, ports, user — **never a password** |
| `ctx.console(cmd)` | an operator command; returns the reply lines (section 3) |
| `ctx.mvs` | the mvsMF session: submit, upload, download, and `ctx.mvs.request{…}` for any mvsMF endpoint |
| `ctx.exec{argv}` | run a program — an argv list, **no shell** |
| `ctx.tool(name)` | a host tool from `[tools]`, fetched and SHA-256-pinned in `mbt.lock` |
| `ctx.fs` | files, inside the project only |
| `ctx.out`, `ctx.inputs` | in a task: its declared outputs and the files its inputs matched |
| `ctx.result` | in `after_*`: what the command produced (modules, artifacts, the test matrix, the tag) |
| `ctx.log`, `ctx.warn`, `ctx.args`, `ctx.dry_run` | |

### The rules

- **Hooks act; they never change the build.** They may write files, run
  programs, issue commands and abort. Flags, sources and module attributes change
  only through `mbt.toml`, so a build can always be explained from its data.
- **Order at one point:** plugins (as declared), then the project, then you.
  No priorities.
- **Errors:** a `before_*` error stops the command before it does anything. An
  `after_*` error undoes nothing — a deploy cannot be rolled back — but the
  command fails, so a deploy whose restart failed is not reported as success.
- **`--dry-run`:** hooks run with `ctx.dry_run = true` and change nothing.
- **API version:** a plugin declares `api = 1`; mbt refuses one it cannot serve,
  with a clear message, instead of letting it break quietly.

## 3. Talking to MVS

**Work goes through mvsMF, always.** Jobs, data sets, spool — mbt has no other
way in, and needs none.

**One session per run.** mbt logs on once (`POST /zosmf/services/authenticate`),
uses the token for its own calls and the hooks', and logs off (`DELETE`) on
success, failure, a failed hook or Ctrl-C. Lua never sees the password: it uses
`ctx.mvs.request{…}`, to which mbt attaches the token, or — to hand the session
to an external program such as curl or the Zowe CLI — `ctx.mvs.token`, passed in
the environment, never as an argument. If mbt is killed outright, httpd expires
the session by itself (by default after 30 idle minutes, at most 8 hours). As a
side effect, the logon is checked once per run instead of once per request.

**Operator commands go through a chain:** mvsMF first, the **Hercules web
console** as the fallback — for when HTTPD or mvsMF are down, or are the very
thing being deployed. mbt falls back only when the first channel certainly did
not deliver (no connection, a server error); never when MVS rejected the command
(that is an answer), and never when the answer timed out after sending, because
`S HTTPD` must not run twice. The Hercules console is used only when the target
names it.

## 4. Targets

A target is one MVS system. They live in `~/.mbt/targets.toml`, which mbt
expects to be readable by you alone (mode 600):

```toml
[target.lab]
default  = true
hlq      = "IBMUSER"
volume   = "PUB001"                       # where RECEIVE allocates
jobclass = "A"
msgclass = "H"

[target.lab.mvsmf]                        # required
url      = "http://lab:1080"
user     = "IBMUSER"
password = { keychain = "mbt/lab" }

[target.lab.hercules]                     # optional: console fallback
url      = "http://lab:8038"
user     = "admin"
password = { env = "LAB_HERC_PASS" }

[target.lab.ssh]                          # optional: no passwords, keys or agent
host = "lab"
user = "mike"

[target.lab.tn3270]                       # optional: for mbt test --tso
host     = "lab"
port     = 3270
user     = "TSOTEST"
password = { cmd = ["pass", "show", "lab/tso"] }

[target.lab.console]
order = ["mvsmf", "hercules"]
```

- **Passwords** come from the environment (`{ env = … }`, for CI secrets), the
  system keychain (`{ keychain = … }`) or a helper program (`{ cmd = […] }`, argv,
  no shell). A literal password is accepted only in a file only you can read.
- **CI** sets a target through environment variables named after the file
  (`MBT_TARGET_MVSMF_URL`, `…_USER`, `…_PASSWORD`, …). The CI system starts the
  MVS container itself (GitHub's `services:`); mbt does not start or stop MVS.
- **`mbt target ping [NAME] [--wait SEC]`** asks, without logging on, whether mvsMF,
  the Hercules console, TN3270 and SSH answer. A 401 from mvsMF counts as
  answering. `--wait` waits for mvsMF; in CI it replaces the shell loop that
  waits for an IPL, and needs no secrets.
- **`mbt target info [NAME] [--wait SEC]`** logs on and shows what answers (mvsMF
  version and system, Hercules details). Ping green but info red means the system
  is up and the logon is wrong.
- **`mbt target list`**, **`mbt target import .env --name NAME`** (turns an
  existing `.env` into a target).
- Commands that need MVS **do not wait**: if mvsMF does not answer, they say so
  at once.
- `--target NAME` picks the system on any command. The switch that names a load
  library on `deploy` and `test --mvs` becomes `--linklib DSN`.
- **`.env` goes.** `mbt migrate` removes `.env.example`; until a `targets.toml`
  exists, an `.env` is still read, with a warning. Two of today's eight `MBT_*`
  variables are dead already (`MBT_MVS_DEPS_HLQ`, `MBT_BUILD_ID`), and
  `MBT_MVS_DEPS_VOLUME` lives on as `volume`.

## 5. Tests

| Command | Runs |
|---|---|
| `mbt test` | natively on the host |
| `mbt test --mvs` | in batch on MVS: one job, a batch step and a TSO-in-batch (IKJEFT01) step per test |
| `mbt test --tso` | interactive, in a TSO session over TN3270 |

Results come as one matrix: `BATCH | TSO-BATCH | TSO`.

An interactive test is a Lua file under `test/tso/`, found automatically:

```lua
-- test/tso/rexxsay.lua
return function(t)
  t:logon()                                       -- user and password from the target
  t:type("CALL 'IBMUSER.TESTLIB(TSTSAY)'"):enter()
  t:expect("HELLO FROM REXX", { timeout = 10 })
  t:expect("READY")
end                                               -- mbt logs off, always
```

- The test modules are deployed to the same test library as for `--mvs`.
- A failed `expect` is a failure, and the screen at that moment goes into the log.
- One logon per test by default; `--shared-session` logs on once for the run.
  A TSO userid can be logged on only once, so tests run one after another — and
  the target's TN3270 user should not be one you are logged on with yourself.
- **The TN3270 client is built into mbt** (Go): Telnet negotiation, the 3270 data
  stream into a screen buffer, keys and modified fields back. No Python, no
  s3270 to install. IBM's [tnz](https://github.com/IBM/tnz) (Python; its `zti`
  terminal and `ati` automation module) is the reference we check against; s3270
  is the fallback should the client prove harder than it looks.

## 6. Plugins

```toml
# mbt.toml
[plugins]
"mvslovers/mbt-ufs" = "^1"
```
```lua
-- mbt/init.lua
local ufs = require("mvslovers/mbt-ufs")
ufs.webroot { from = "static", image = "build/webroot/httpd-webroot.img", size = "1M" }
```

- A plugin is a GitHub repository with releases, resolved like a dependency
  (semver range), **SHA-256-pinned in `mbt.lock`**, cached in `~/.mbt`. A new
  version comes only with `mbt deps --update`, visibly, in the diff.
- A plugin **declares the programs it runs** (`exec = { "ufsd-utils" }`); mbt
  refuses any other. With the sandbox (no `os`, no `io`, files inside the
  project, MVS through mvsMF only, no passwords), that keeps a plugin to what it
  says it does.
- To try a plugin before releasing it, point it at a local directory in
  `.mbt/deps.local.toml`, as with dependencies.
- **`mvslovers/mbt-ufs`** — the webroot image httpd builds today — is built
  alongside as the worked example; any project that ships a UFS image can use it.

## 7. Where today's Makefile lines go

| Today | Project | mbt 3 |
|---|---|---|
| webroot UFS image with a pinned `ufsd-utils` | httpd | the `mbt-ufs` plugin, or a task in `mbt/init.lua` |
| desktop upload script | mvsMF | `mbt.command("deploy-desktop", …)` |
| `buildid.h` from the git hash | mvsMF | gone: mbt already writes `buildstamp.h` |
| `run-mvs` / `stop-mvs` (Docker) | mvsMF, rexx370 | gone from the project: CI starts the container; locally a command in your own `~/.mbt/init.lua` if you want one |
| SMP usermod stream (`ZMG…`) | brexx370, rexx370 | a task now; a USERMOD kind in mbt later (design §6.4) |

## 8. What we decided against

- **Tasks as plain TOML.** Built and measured first, then dropped: Lua gives the
  same thing and room for logic, with one model instead of two.
- **Providers for starting and stopping MVS** (`mbt mvs up/down` with Docker,
  SSH or container back ends). CI starts its own containers, and everyone runs
  their own MVS their own way; a command in `~/.mbt/init.lua` covers the rest.
- **Hooks that change the build.** A build must be explainable from its data.
- **Passwords in Lua.** Lua gets a session, never a credential.
- **Python (tnz) or s3270 as a requirement** for interactive tests.
- **A separate "test userid" concept.** The target's TN3270 entry has its own user.

## 9. Still open

- **Spikes before building:** the Lua engine (arnodel/golua, young — sandbox,
  speed, standard library); the TN3270 client against Hercules (logon, expect,
  logoff).
- **Ideas for later:** a password manager as a credential source; targets that a
  project defines (stand descriptions without passwords).

## Questions for you

1. Are the hook points the right ones? Is one missing that your project needs?
2. Would you use `mbt test --tso`, and for what — TSO commands, ISPF panels,
   full-screen programs?
3. Is the plugin trust model (pin plus declared programs) enough for you?
4. What do *your* Makefiles or scripts do today that this does not cover?
