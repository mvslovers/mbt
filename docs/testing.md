# Testing an mbt project

mbt builds and runs tests on the real MVS target (and, for portable logic,
natively on the host). This document is the convention a project follows so the
`test-mvs` runner can execute and report its tests.

## The one hard contract: the return code

The runner's only requirement of a test is its **return code**:

> A `[[test]]` program returns **0** when every check passed and **nonzero**
> when any check failed.

- A C test returns it from `main()` (`return failed ? 1 : 0;`) — it becomes the
  job step's COND CODE.
- An assembler test sets R15.

Everything else below is *recommended convention*, not a requirement. The runner
gates on the RC, never on parsing a test's output, so a test may print whatever
it likes as long as the RC is honest.

## Declaring tests

Each test translation unit is one `[[test]]` in `project.toml` and builds a
standalone load module:

```toml
[[test]]
name = "TSTWIDG"                 # MVS member name, <= 8 chars
startup = "crt0"                # crt0 (default) / crt1 (threaded) / false (asm)
sources = ["test/mvs/tstwidg.c", "src/widget.c", "src/util.c"]
```

Conventions:

- **One test = one translation unit with one `main()`** (the counters in
  `mbtcheck.h` are per-TU).
- Source layout: `test/mvs/` for tests that run on MVS (and may also run on the
  host); `test/host/` for host-only tests (internals, perf, stress).
- Name `tst*`, stem <= 8 chars (→ a valid PDS member `TST*`).
- List the production sources the test needs in `sources` (the same faithful
  set the module would link).

## Recommended: `mbtcheck.h`

mbt ships a ~30-line header on the include path (`#include <mbtcheck.h>`) that
produces the RC and the uniform `PASS:`/`FAIL:` lines the runner tallies:

```c
#include <mbtcheck.h>

int main(void)
{
    printf("=== MYPROJ widget tests ===\n");
    CHECK(widget_init() == 0, "widget_init returns 0");
    CHECK_EQ(widget_count(), 3, "three widgets");
    return mbt_test_summary("TSTWIDG");   /* RC 0 ok / 1 failed */
}
```

- `CHECK(cond, msg)` / `CHECK_EQ(got, want, msg)` — record one assertion, print
  one `  PASS:` / `  FAIL:` line.
- `mbt_test_summary(name)` — print the standard summary and return the RC.
- Portable C89: the same source compiles with cc370 (MVS load module) **and** a
  host compiler (native unit test) — a `test/mvs/*.c` test is dual-target.

Using `mbtcheck.h` is optional (the RC contract stands on its own), but it makes
the per-assertion count work and keeps test output uniform across the ecosystem.

## Running on MVS

```sh
make test          # build the test load modules (no MVS)
make deploy        # production LINKLIB must exist: tests LOAD data modules from it
make test-mvs      # build (if needed) + deploy tests to a TESTLIB + run + report
make check         # every available suite (currently test-mvs)
```

`make test-mvs`:

1. packs the built `[[test]]` modules into `{HLQ}.{PROJECT}.{VRM}.TESTLIB`
   (separate from the production LINKLIB — tests are never shipped),
2. generates `build/test-runner.jcl`: per test a **batch** step (`EXEC PGM=`)
   and a **TSO** step (`IKJEFT01` + `CALL`), `COND=EVEN`, with
   `STEPLIB = TESTLIB + production LINKLIB` so each test's runtime `LOAD` of the
   data modules resolves,
3. submits it and prints a per-test matrix (test × {batch, tso} → RC) plus an
   aggregate `PASS:`/`FAIL:` count.

Both legs run because some behaviour differs under TSO (e.g. the TSO vs batch
environment/anchor path). A test passes only when its step RC is 0.

> Note: MVS 3.8j does not treat `REGION=0M` on a step as unlimited (it falls
> back to ~512K → S878); the runner uses a concrete region.

## How evaluation works

- **Gate:** each test is one job step; the runner parses the step's RC from the
  spool (`IEF142I … COND CODE nnnn`, or `IEF450I … ABEND` → fail). RC 0 = pass.
- **Count (informational):** the runner counts `PASS:` / `FAIL:` lines across
  the spool — uniform because every test emits them via `mbtcheck.h` (the
  per-test summary line formats vary, so they are not used for the count).

The RC is the contract; the output is for humans.
