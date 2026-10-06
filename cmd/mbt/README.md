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
mbt deploy [--target DSN] [--module M]... [--dry-run] [-v]
mbt test --mvs [--only NAME]... [--no-deploy] [--target DSN] [-v]
mbt check
mbt migrate [--dry-run]
mbt release VERSION [--next V]
mbt prerelease
mbt run NAME [-v] [-- ARGS...]
mbt mvs up | down
mbt clean | distclean
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

**Tasks and tools.** `[task.NAME]` in `mbt.toml` declares a project step
mbt does not do itself -- argv lists, no shell -- that runs before a phase
(`before = ["package"]`) or through `mbt run NAME`; `[tools]` pins the host
tools it uses (GitHub release assets, SHA-256 in `mbt.lock`). Measured on
httpd: the webroot task (was `make webroot`) builds an image with the same
contents as mbt 2's (listing and file content; only timestamps differ). Of
the 13 package entries, 9 are identical and the other 4 differ for known reasons:
the image, the install JCL (modules in name order, schema 3) and the load
XMIT twice -- httpd.o and httpcons.o carry the build stamp, and a migrated
copy is dirty; on
mvsMF, `mbt run deploy-desktop -- --dry-run` prints what `make
deploy-desktop-dry` printed. `mbt mvs up` / `down` replace the `run-mvs` /
`stop-mvs` Makefile targets (docker; tested against a stand-in, not a real
docker). `mbt clean` / `distclean` as `make clean` / `distclean`.
