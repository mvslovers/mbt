# mbt 3 (in development)

The Go implementation of the mbt 3 proposal (`internals/mbt-3-design.md`).
It lives beside mbt 2 (`mk/`, `scripts/`) until every project has migrated.

```
go build -o mbt ./cmd/mbt
mbt build [--all] [--tests] [-j N] [-v] [NAME...]
mbt deps [--update] [--locked]
mbt module-data [--all] [--raw]
mbt package
mbt test [--only NAME]... [-v]
mbt deploy [--target DSN] [--module M]... [--dry-run] [-v]
mbt test --mvs [--only NAME]... [--no-deploy] [--target DSN] [-v]
mbt compiledb
```

Today it reads an mbt 2 `project.toml` and builds object decks, archives and
load modules with the cc370 toolchain on PATH. For all eight ported projects
the output is byte-identical to mbt 2's (`internals/acceptance/`).

`mbt deps` stages the same files and writes the same `mbt.lock` as mbt 2;
`--locked` makes any change to the lock an error.

`mbt build` checks the installed libc370 against `[toolchain] libc370` and
runs the module-data check before it links, as mbt 2 does; their output is
mbt 2's. `mbt package` writes the same load XMIT, lib tarball and SMP installation
package (install and allocation jobs, SYSMOD, source library XMITs).

`mbt test` builds and runs the dual tests on the host, with mbt 2's table
and summary.

`mbt deploy` and `mbt test --mvs` send the same requests and JCL as
`make deploy` and `make test-mvs`, and print the same result (measured
against a local stand-in for mvsMF; not yet run against a real system).
`mbt compiledb` writes the same `compile_commands.json` (mbt's own include
directory aside: `.mbt/include` instead of the submodule's).
Requests go out as HTTP/1.0, as mbt 2 forces them.

Not yet: project steps outside mbt (httpd's `make webroot`: needs tasks),
`doctor`, `release`,
`mbt.toml` schema 3, the launcher.
