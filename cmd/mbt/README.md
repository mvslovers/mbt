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
mbt compiledb
mbt doctor
```

Today it reads an mbt 2 `project.toml` and builds object decks, archives and
load modules with the cc370 toolchain on PATH. For all eight ported projects
the output is byte-identical to mbt 2's (`internals/acceptance/`).

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

Not yet: project steps outside mbt (httpd's `make webroot`: needs tasks),
`release`,
`mbt.toml` schema 3, the launcher.
