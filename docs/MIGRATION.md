# Migrating from mbt 2 to mbt 3

mbt 3 is one program, `mbt`, installed on your machine. It replaces the mbt 2
git submodule, the two-line `Makefile` and the `make` targets. The build does
not change: the same cc370 toolchain runs the same steps and writes the same
object decks, archives and load modules. Measured on every project ported to
mbt 2, the outputs of mbt 3 are byte-identical to mbt 2's.

What changes is everything around the build:

| | mbt 2 | mbt 3 |
|---|---|---|
| The tool | git submodule `mbt/` + `Makefile` | the `mbt` program on your `PATH` |
| Which mbt a project uses | the submodule commit | `[toolchain] mbt` in `mbt.toml` |
| Project file | `project.toml` + `VERSION` | `mbt.toml` (schema 3) |
| Commands | `make <target>` | `mbt <command>` |
| MVS systems | `.env` in each project | `~/.mbt/targets.toml`, one per machine |
| Extra build steps | Makefile rules | Lua in `mbt/init.lua`, or a plugin |
| CI | `build.yml` / `release.yml` | `build3.yml` / `release3.yml` |

This guide takes a project from mbt 2 to mbt 3. The project should build with
mbt 2 first. The v1-to-v2 guide this file used to hold is kept at
[v2.2.0](https://github.com/mvslovers/mbt/blob/v2.2.0/docs/MIGRATION.md).

---

## 1. Install mbt

mbt 3 is published as a GitHub release of
[mvslovers/mbt](https://github.com/mvslovers/mbt/releases), for macOS and
Linux (arm64 and amd64) and Windows (amd64). It is a single binary with no
dependencies. Until 3.0.0 is out, the release is the prerelease
`v3.0.0-dev`:

```sh
gh release download v3.0.0-dev -R mvslovers/mbt \
   -p 'mbt-3.0.0-dev-darwin-arm64.tar.gz' -p 'mbt-3.0.0-dev-sha256.txt'
shasum -a 256 -c --ignore-missing mbt-3.0.0-dev-sha256.txt
tar -xzf mbt-3.0.0-dev-darwin-arm64.tar.gz
install mbt-3.0.0-dev-darwin-arm64/mbt ~/.local/bin/mbt     # any directory on PATH
mbt version
```

Pick the archive for your platform: `darwin-arm64`, `darwin-amd64`,
`linux-amd64`, `linux-arm64` or `windows-amd64` (a `.zip`).

**You install mbt once, not per project.** When a project pins another
version (`[toolchain] mbt`, section 2), the installed mbt fetches that
version into `~/.mbt/versions/`, checks it against the release's SHA-256
list and runs it instead. `MBT_NO_SWITCH=1` keeps the one you started.

The cc370 toolchain (`cc370`, `as370`, `ld370`, `ar370`) and the libc370
sysroot stay what they were: installed on your machine and on your `PATH`.
`mbt doctor` checks both.

---

## 2. Convert the project file

```sh
mbt migrate --dry-run     # print the new mbt.toml, change nothing
mbt migrate               # write mbt.toml, remove project.toml and VERSION
```

`mbt migrate` reads `project.toml` and writes `mbt.toml`. **Your comments come
along**: each comment travels with the key or table it stands above. Before
writing anything, mbt loads the new file and compares it with the old one.
If they differ in anything but the intended changes, nothing is written.

The intended changes are the ones listed below. Beyond those, mbt prints a
`NOTE` (on stderr, so `mbt migrate --dry-run > mbt.toml` stays a valid file)
when it has to drop a comment, for example the comments of a `[release]`
table that held only `version_files = ["VERSION"]`, and when the `Makefile`
does more than include mbt: it names those targets, which have to move
before the Makefile can go (section 6). Dropping `VERSION` itself is one of
the intended changes and gets no `NOTE`.

Then pin the mbt the project is built with:

```toml
[toolchain]
mbt = "3.0"        # any 3.0.x; "3.0.2" for exactly that one
```

### What is different in `mbt.toml`

The full reference is [`internals/mbt-3-schema.md`](../internals/mbt-3-schema.md).
What you will notice after migrating:

- **Modules are keyed tables.** `[[module]] name = "UFSD"` becomes
  `[module.UFSD]`. mbt orders modules by name wherever order is visible: the
  build, the SMP package's module list, the test matrix. In most projects the
  first build after migrating therefore lists them in a different order. The
  load modules themselves do not change.
- **`rent` and `reus` must be stated on every module.** `norent = true` becomes
  `rent = false`.
- **`[build] cflags`** loses its `-I` pairs to a new `include` list. The
  compile line stays the same.
- **Tests are discovered.** Every `test/**/*.c` and `test/**/*.asm` is a
  test, named after its file in upper case (`test/mvs/tstgctx.c` is
  `TSTGCTX`). A `[test.NAME]` entry is needed only where a test differs from
  that: more sources, a `parm`, fixtures. Defaults for all tests go in
  `[tests]`, and files under `test/` that are not tests go in
  `[tests] exclude`. `mbt migrate` keeps every source list that differs from
  the discovered default, so the test load modules stay identical. A source
  file under `test/` that mbt 2 did not build as a test (a host-only test
  program, say) lands in `[tests] exclude`, so the migrated project builds
  exactly what it built before. Remove it from the list once it should become
  a test.
- **The version lives in `[project] version` only.** `VERSION` is gone, and
  nothing reads it any more.
- **The FMID can be derived.** `[smp] prefix = "TUFS"` and version 1.4.0 give
  `TUFS140`, deleting `TUFS130`. An explicit `fmid` / `delete` still wins.
  A patch release (1.4.1) needs an explicit `fmid` until mbt builds PTFs:
  mbt refuses to spend the minor's id a second time.
- **`startup = "crt0"` / `"crt1"` are gone.** The C startup comes out of
  `libc.a` (libc370 >= 2.3.0), and `mbt migrate` drops them.

A directory holding both `mbt.toml` and `project.toml` is an error. mbt does
not pick one.

---

## 3. Remove the submodule and the Makefile

In the same change, remove the Makefile and the submodule in **one**
command:

```sh
git rm Makefile mbt .gitmodules            # mbt was the only submodule
git rm Makefile mbt                        # there are others: keep .gitmodules
```

`git rm mbt` already removes mbt's entry from `.gitmodules` and stages that.
So a second, separate `git rm .gitmodules` fails with "the following file has
changes staged in the index". Then clear what git keeps of the submodule
outside the tree:

```sh
rm -rf .git/modules/mbt
git config --remove-section submodule.mbt
```

`.gitignore` should keep `.mbt/` (staged dependencies, tools, plugins and
build state) and `build/`, `dist/`. In mbt 3 the directory `mbt/` is yours:
it holds the project's Lua (section 6), so do not ignore it.

**Commit `mbt.lock`** as before, once there is one: `mbt deps` writes it
only when the project declares `[dependencies]`, `[tools]` or `[plugins]`.
A project with none of them has no lock file, under mbt 2 as under mbt 3.

---

## 4. Commands

| mbt 2 | mbt 3 |
|---|---|
| `make` | `mbt build` |
| `make all` | `mbt build --all` |
| `make <name>` | `mbt build NAME` |
| `make test` (build the test modules) | `mbt build --tests` |
| `make test-host` | `mbt test` |
| `make test-mvs ARGS="--only X"` | `mbt test --mvs --only X` |
| — | `mbt test --tso` (interactive tests, section 7) |
| `make check` | `mbt check` |
| `make deps` | `mbt deps` (`--update` to move pins) |
| `make package` | `mbt package` |
| `make dist` | `mbt dist` |
| `make deploy` | `mbt deploy` (`--dry-run`, `--module M`) |
| `make doctor` | `mbt doctor` |
| `make compiledb` | `mbt compiledb` |
| `make clean` / `distclean` | `mbt clean` / `mbt distclean` |
| `make release VERSION=1.4.0 NEXT_VERSION=…` | `mbt release 1.4.0 [--next 1.4.1-dev]` |
| `make prerelease` | `mbt prerelease` |
| `VERBOSE=1 make` | `-v` on the command |

`mbt` alone lists every command. Behaviour you may notice:

- **`mbt deps` never moves a pin on its own.** A version outside its range,
  or an archive whose SHA-256 differs from `mbt.lock`, is an error, even for
  a prerelease that was published again. `mbt deps --update` re-resolves and
  rewrites the lock. mbt 2 re-pinned with a warning. Archives are cached by
  SHA, so a locked prerelease stays buildable after its tag has moved.
- **`mbt deploy --linklib DSN`** names a load library to deploy into instead
  of `[deploy] target`. `--target` now names an MVS *system* (section 5).
  The default `[deploy] target` is `<NAME>.DEV.LINKLIB`. A library project
  without load modules has nothing to deploy, and says so.
- **`mbt deploy --dry-run` needs no password**: it packs and reports, and
  logs on to nothing.
- **`mbt doctor` masks the password** in its configuration table.

Developing against an unreleased dependency works as before:
`.mbt/deps.local.toml` with an `[override]` table.

---

## 5. MVS systems: from `.env` to targets

mbt 3 keeps the MVS systems you work with in one file per machine,
`~/.mbt/targets.toml` (`MBT_HOME` moves the directory). **This is not part
of migrating a project.** You do it once per machine, before or after any
migration. Until then mbt reads the mbt 2 settings (`MBT_MVS_*`, a project's
`.env`) and warns on every run. Turn an existing `.env` into a target:

```sh
mbt target import .env --name lab
```

This writes:

```toml
[target.lab]
default  = true            # the first target becomes the default
hlq      = "IBMUSER"
volume   = "PUB000"

[target.lab.mvsmf]
url      = "http://lab:1080"
user     = "IBMUSER"
password = "…"             # from MBT_MVS_PASS
```

`import` takes what the `.env` says. For what it leaves out (HLQ, volume,
job classes, host, port, user) it takes the values mbt 2 actually used: from
the environment (`MBT_MVS_*`, `MBT_JES_*`) or `~/.mbt/config.toml`. It
prints each value it took that way. A password from the environment is not
copied into the file.

A password written into the file is accepted only while nobody else can read
it (`chmod 600`, which `import` sets). Better, name where it comes from:

```toml
password = { env = "LAB_PASSWORD" }
password = { keychain = "mbt/lab" }     # macOS Keychain, or secret-tool on Linux
password = { cmd = ["pass", "show", "mvs/lab"] }
```

Then check the connection:

```sh
mbt target list
mbt target ping lab          # every access, without logging on
mbt target info lab          # logs on to mvsMF
```

`--target NAME` picks a system for `deploy` and `test --mvs`. Without it, mbt
uses the default target, or the only one.

For the project, this means: `.env` stays git-ignored and can be deleted
once a target exists. **Remove `.env.example`**. It documents mbt 2
variables that mbt 3 no longer reads. Point the README at
`mbt target import` instead.

A target can carry more than mvsMF. All of these are optional:

```toml
[target.lab.hercules]      # Hercules' web console: a fallback for operator commands
url = "http://lab:8081"

[target.lab.tn3270]        # for interactive tests (mbt test --tso)
host     = "lab"
user     = "IBMUSER"
password = { keychain = "mbt/lab-tso" }
```

`mbt target console lab -- D T` issues an operator command through mvsMF,
falling back to the Hercules web console when mvsMF cannot be reached.

**mbt logs on to mvsMF once per run** and logs off at the end, on an error and
on Ctrl-C.

---

## 6. Makefile extras: Lua and plugins

A Makefile rule beyond mbt's own targets (building an image, uploading
files) becomes Lua in `mbt/init.lua`. Modules for it go in `mbt/lua/`. Only an
`mbt.toml` project loads Lua. There are three kinds:

```lua
-- a task: declared inputs and outputs, runs before the named commands,
-- skipped while its outputs are newer than its inputs
mbt.task {
  name    = "tables",
  before  = { "build" },
  inputs  = { "tools/tables.txt" },
  outputs = { "src/tables.c" },
  run = function(ctx)
    ctx.exec { "python3", "tools/gen.py", ctx.inputs[1], ctx.out[1] }
  end,
}

-- a hook: before_/after_ build, test, package, dist, deploy, release; on_failure
mbt.hook("after_deploy", function(ctx)
  ctx.log("deployed " .. ctx.result.library)
end)

-- a command of the project's own: mbt run upload
mbt.command("upload", function(ctx)
  ctx.exec { ctx.tool("ufsd-utils"), "upload", "build/web.img", "--dsn", "LAB.WEBROOT" }
end)
```

`mbt run` lists the tasks and commands. `mbt run NAME` runs one, a task
always, even when it is up to date. `--dry-run` runs the Lua but no program
and writes nothing.

Lua runs with limits. `ctx.exec` takes an argument list and starts no shell.
File access stays inside the project. Each call has a limit on CPU and on
memory.

**Host tools** come from GitHub releases and are pinned like dependencies:

```toml
[tools]
ufsd-utils = { repo = "mvslovers/ufsd-utils", version = "1.0.1" }
```

`mbt deps` fetches the tool and records its SHA-256 in `mbt.lock`;
`ctx.tool("ufsd-utils")` returns its path.

**Plugins** are Lua that someone else maintains, declared like dependencies:

```toml
[plugins]
"mvslovers/mbt-ufs" = "^0.1"
```

```lua
local ufs = require("mvslovers/mbt-ufs")
ufs.webroot { from = "static", image = "build/webroot/web.img" }
```

`mbt deps` fetches, pins and stages the plugin. A plugin may start only the
programs its own manifest names. [mbt-ufs](https://github.com/mvslovers/mbt-ufs),
which builds UFS370 disk images, replaces the `make webroot` rule of a web
server project.

---

## 7. Tests

The test sources do not change: `#include <mbtcheck.h>`, a return code of 0
for passed. `mbt test` runs them on the host, `mbt test --mvs` on MVS (batch
and TSO steps per test, as `make test-mvs` did), `mbt check` both.

New are **interactive tests**: `test/tso/*.lua`, each run in a TSO session of
its own over TN3270, as the target's `[tn3270]` user:

```lua
-- test/tso/time.lua
return function(t)
  t:logon()
  t:type("TIME"):enter()
  t:expect("TIME-")
  t:logoff()
end
```

`t:pf(n)`, `t:pa(n)`, `t:clear()`, `t:tab()`, `t:screen()` and `t:text()` are
there too, and `t.testlib` names the library `mbt test --mvs` deploys the test
modules to. A failed `expect` fails the test and shows the screen. The
session is logged off whatever happens.

---

## 8. CI

Replace the two workflow files:

```yaml
# .github/workflows/build.yml
name: Build
on:
  pull_request:
  push:
    branches: [main]
jobs:
  build:
    uses: mvslovers/mbt/.github/workflows/build3.yml@main
    with:
      host_tests: true          # also run mbt test
```

```yaml
# .github/workflows/release.yml
name: Release
on:
  push:
    tags: ["v*"]
jobs:
  release:
    uses: mvslovers/mbt/.github/workflows/release3.yml@main
    permissions:
      contents: write
```

Both install the newest mbt 3 release, which then switches to the version
`[toolchain] mbt` pins. `build3.yml` builds against the tip of cc370 and
libc370, as the mbt 2 workflow did. `release3.yml` builds with the versions
`[toolchain]` names, checks that the tag matches `[project] version`, runs
`mbt package` and publishes `dist/`. Once mbt 3.0.0 is released, pin `uses:`
to the release instead of `@main`.

A workflow that talks to MVS sets its target from the environment:
`MBT_TARGET_MVSMF_URL`, `MBT_TARGET_MVSMF_USER`, `MBT_TARGET_MVSMF_PASSWORD`,
and `MBT_TARGET_HLQ`, `MBT_TARGET_VOLUME` where needed.

---

## 9. Checklist

1. Install mbt 3; `mbt doctor`.
2. `mbt migrate --dry-run`, read it, then `mbt migrate`.
3. Add `[toolchain] mbt = "3.0"`.
4. `git rm Makefile mbt .gitmodules`, keep `.mbt/` in `.gitignore`.
5. Remove `.env.example`, and point the README at targets. Creating a target
   (`mbt target import .env --name <name>`) is per machine and can come
   later.
6. Move Makefile extras to `mbt/init.lua`, a plugin or `[tools]`.
7. Replace `build.yml` / `release.yml` with the mbt 3 workflows.
8. `mbt deps`, `mbt build --all`, `mbt test`, and `mbt deploy --dry-run`
   (which says "nothing to deploy" for a library).
9. Optional, and worth it: compare with mbt 2. Build the same commit with
   mbt 2 in a second checkout and compare `dist/` and `build/`. Object
   decks carry the assembly date, and load modules the link time. Pin both
   on both sides to make the comparison byte for byte:

   ```sh
   export ASMDATE=10/07/26 ASMTIME=12.00 LDDATE=26280 LDTIME=120000
   ```

   Without them, build both on the same day and expect those bytes to
   differ: two per object deck, two per load module. A `.tar.gz` differs
   anyway, because mbt 2 and mbt 3 write the archive differently (owner,
   timestamps, order, no macOS `._*` entries in mbt 3's). Compare what is
   unpacked from it.
10. Rewrite `make …` to `mbt …` in the README and other docs.
11. `mbt deploy`: the first live deploy.
