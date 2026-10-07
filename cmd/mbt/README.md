# mbt 3 (in development)

The Go implementation of the mbt 3 proposal (`internals/mbt-3-design.md`).
It lives beside mbt 2 (`mk/`, `scripts/`) until every project has migrated.

```
go build -o mbt ./cmd/mbt
mbt build [--all] [--tests] [-j N] [-v] [NAME...]
mbt deps [--update]
mbt module-data [--all] [--raw]
mbt package
mbt dist
mbt test [--only NAME]... [-v]
mbt deploy [--target NAME] [--linklib DSN] [--module M]... [--dry-run] [-v]
mbt test --mvs [--only NAME]... [--no-deploy] [--target NAME] [--linklib DSN] [-v]
mbt test --tso [--only NAME]... [--target NAME] [-v]
mbt check
mbt migrate [--dry-run]
mbt release VERSION [--next V]
mbt prerelease
mbt run [NAME] [-v] [--dry-run] [-- ARGS...]
mbt clean | distclean
mbt target list | ping [NAME] [--wait SEC] | info [NAME] [--wait SEC] | console [NAME] -- CMD | import .env --name NAME
mbt compiledb
mbt doctor
```

It reads an `mbt.toml` (schema 3, `internals/mbt-3-schema.md`) or an mbt 2
`project.toml`, and builds object decks, archives and load modules with the
cc370 toolchain on PATH. From a `project.toml`, the output of all eight
ported projects is byte-identical to mbt 2's (`internals/acceptance/`).

`mbt migrate` converts `project.toml` into `mbt.toml`, comments included, and
removes `project.toml` and `VERSION`. Before it writes anything it loads the
new file and compares the two models; any difference beyond the intended ones
writes nothing.

`mbt deps --update` stages the same files and writes the same `mbt.lock` as
mbt 2. Without `--update`, mbt 3 never moves a pin: a version outside its
range or an archive whose SHA differs from the lock is an error, a
republished prerelease included (mbt 2 re-pinned that with a warning).
Archives are cached by SHA, so a locked prerelease stays buildable here
after its tag moved on.

`mbt build` checks the installed libc370 against `[toolchain] libc370` and
runs the module-data check before it links, as mbt 2 does; their output is
mbt 2's. `mbt package` writes the same load XMIT, lib tarball and SMP installation
package (install and allocation jobs, SYSMOD, source library XMITs).
`mbt dist` re-renders the SMP package alone from the load XMIT already in
`dist/`, as `make dist` does.

`mbt test` builds and runs the dual tests on the host, with mbt 2's table
and summary.

`mbt deploy` and `mbt test --mvs` send the same requests and JCL as
`make deploy` and `make test-mvs`, and print the same result (measured
against a local stand-in for mvsMF, then run against mvsdev: ftpd deploy
JOB01485, test --mvs JOB01487, 170 PASS as under mbt 2).
`mbt check` is `make check`: the host tests, then the MVS tests.
`mbt compiledb` writes the same `compile_commands.json` (mbt's own include
directory aside: `.mbt/include` instead of the submodule's).
Requests go out with Go's standard HTTP/1.1 client (mbt 2 forced HTTP/1.0).
`mbt doctor` checks what `make doctor` checks; its configuration table
masks `MVS_PASS`, which mbt 2 printed in clear.

`mbt release` and `mbt prerelease` do what `make release` / `make
prerelease` do; in an `mbt.toml` only `[project] version` moves, and a
patch release with a derived FMID is refused. Tested end to end on
mvslovers/mbt-sandbox with `build3.yml` and `release3.yml`, the reusable
workflows for `mbt.toml` projects.

**Launcher.** Every mbt is also the launcher (design §5): when `mbt.toml`
pins `[toolchain] mbt = "3.0"` (any 3.0.x) or `"3.0.2"` and the running mbt
is another, the matching one under `~/.mbt/versions/` runs instead,
downloaded from the mvslovers/mbt releases first if needed and checked
against their SHA256 list. `MBT_NO_SWITCH=1` keeps the running one. mbt
releases itself on a `v3.*` tag (`.github/workflows/mbt-release.yml`): macOS
and Linux on arm64 and amd64, Windows on amd64.

**Extensions.** An `mbt.toml` project may carry Lua 5.5 in `mbt/init.lua`
(modules in `mbt/lua/`); `~/.mbt/init.lua` is yours (`internals/mbt-3-extensions.md`).
`mbt.hook` runs before and after build, test, package, dist, deploy and
release (and on failure); `mbt.task` declares inputs and outputs and runs
before a command, skipped while up to date; `mbt.command` becomes `mbt run
NAME`. Lua sees only `ctx` -- `exec` (argv, no shell), `tool` (`[tools]`,
SHA-256-pinned), `fs` (inside the project), `log` -- under a CPU and a memory
limit; with `--dry-run` nothing is run or written. Measured: httpd's webroot
as a task and mvsMF's desktop upload as a command reproduce `make webroot` and
`make deploy-desktop-dry`.

**Targets.** The MVS systems live in `~/.mbt/targets.toml` (`MBT_HOME`
moves it), one `[target.NAME]` each: mvsMF required, Hercules, SSH and
TN3270 optional, passwords as `{ env = }`, `{ keychain = }`, `{ cmd = [] }`
or -- in a file only its owner can read -- a literal. CI sets one through
`MBT_TARGET_MVSMF_URL` / `_USER` / `_PASSWORD` (and `_HLQ`, `_VOLUME`, ...);
without either, the mbt 2 settings (`MBT_MVS_*`, `.env`) still work, with a
warning. `--target NAME` picks one (`deploy`, `test --mvs`); the switch that
names a load library is `--linklib` now. mbt logs on to mvsMF once per run
(`/zosmf/services/authenticate`), sends the token on every call and logs off
at the end, on a failure and on Ctrl-C. `mbt target ping` asks every access
without logging on (`--wait` for CI); `info` logs on; `import` turns an
mbt 2 `.env` into a target. Lua gets `ctx.target()` (no passwords) and
`ctx.mvs.request{...}` / `ctx.mvs.token()`. Measured on mvsdev: import,
ping, info; a deploy through the session (member list checked) and
`test --mvs` with ftpd, 170 PASS (JOB01551), as before.

**Console.** Operator commands go through the target's console chain
(`[target.X.console] order`, default mvsMF, then Hercules when the target
has it): `mbt target console [NAME] -- CMD` on the command line,
`ctx.console(cmd)` in Lua (reply lines, channel). The next channel is tried
only when the previous one certainly did not deliver (no connection, an
HTTP error); a command sent without an answer may have run and is never
sent again. mvsMF's console API checks no authorization (mvsmf#347): any
user who can log on can issue any command. Measured on mvsdev through
mvsMF (`D T` from the command line and from Lua); the Hercules channel
(`/cgi-bin/api/v1/syslog`, taken from Hercules' source) only against a
stand-in -- mvsdev has no web console reachable.

**Plugins.** `[plugins] "owner/repo" = "^1"` in `mbt.toml`; `mbt deps`
resolves the newest release in range, fetches its asset
`<repo>-<version>-plugin.tar.gz` (`plugin.toml` with `api` and `exec`,
`init.lua`, `lua/`), pins its SHA-256 in `mbt.lock` as `plugin:owner/repo`
and stages it in `.mbt/plugins/`; a build uses only what is staged. Plugins
load before the project's Lua (`require("owner/repo")`,
`require("owner/repo/mod")`), and `ctx.exec` from plugin code runs only the
programs its `plugin.toml` declares. `.mbt/deps.local.toml [override]`
points a plugin at a working copy. Ranges now also take `^1`, `^1.4`,
`^0.3` (cargo's rule).

**Interactive tests.** `mbt test --tso` runs every `test/tso/*.lua` in a TSO
session of its own, over TN3270 (`internal/tn3270`, a client in Go -- no
s3270, no Python) as the target's `[tn3270]` user. A test returns
`function(t)`: `t:logon()`, `t:type(s):enter()`, `t:pf(n)`, `t:pa(n)`,
`t:clear()`, `t:tab()`, `t:expect(text, { timeout = s })`, `t:screen()`,
`t:text()`, `t:cursor()`; `t.testlib` names the test library `mbt test
--mvs` deploys. A failed expect fails the test with the screen it saw, and
the session is logged off whatever happens. Not yet run against a system as
a command: the client passed 18 logon/TIME/logoff cycles on MVS/CE (#173);
the Lua side is tested against a stand-in session.

Not yet: `--shared-session`, deploying the test modules from `--tso` itself
(run `mbt test --mvs` first).
