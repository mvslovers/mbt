# mbt 3 (in development)

The Go implementation of the mbt 3 proposal (`internals/mbt-3-design.md`).
It lives beside mbt 2 (`mk/`, `scripts/`) until every project has migrated.

```
go build -o mbt ./cmd/mbt
mbt build [--all] [--tests] [-j N] [-v] [NAME...]
```

Today it reads an mbt 2 `project.toml` and builds object decks, archives and
load modules with the cc370 toolchain on PATH. For all eight ported projects
the output is byte-identical to mbt 2's (`internals/acceptance/`).

Not yet: `deps`, `package`, `dist`, `deploy`, `test` (host and MVS), the
module-data check, the `[toolchain]` libc370 check, `mbt.toml` schema 3.
