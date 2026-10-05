# mbt 3 (in development)

The Go implementation of the mbt 3 proposal (`internals/mbt-3-design.md`).
It lives beside mbt 2 (`mk/`, `scripts/`) until every project has migrated.

```
go build -o mbt ./cmd/mbt
mbt build [--all] [--tests] [-j N] [-v] [NAME...]
mbt deps [--update] [--locked]
```

Today it reads an mbt 2 `project.toml` and builds object decks, archives and
load modules with the cc370 toolchain on PATH. For all eight ported projects
the output is byte-identical to mbt 2's (`internals/acceptance/`).

`mbt deps` stages the same files and writes the same `mbt.lock` as mbt 2;
`--locked` makes any change to the lock an error.

Not yet: `package`, `dist`, `deploy`, `test` (host and MVS), the
module-data check, the `[toolchain]` libc370 check, `mbt.toml` schema 3.
