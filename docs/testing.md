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

`name` is a rule, not a convention: it becomes a PDS member and the `PGM=` of
the test's job step, so it must be 1..8 characters from `A-Z 0-9 @ # $` and may
not start with a digit. `make` rejects anything else while reading
`project.toml` — the rule holds for `mvs = false` tests too, since the name is
also the `--only` key and the host binary. Without that check a 9-character
name built and deployed happily and only failed on MVS, as a JCL error that
discards the whole job (`IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD`),
leaving a matrix in which every test reads FAIL and none of them ran.

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
make test-host     # build + run the portable tests natively (fast inner loop)
make test          # build the test load modules (no MVS)
make deploy        # production LINKLIB must exist: tests LOAD data modules from it
make test-mvs      # build (if needed) + deploy tests to a TESTLIB + run + report
make check         # every available suite (host + MVS)
```

## Running on the host (`test-host`)

Tests written as portable C (`int main`, `mbtcheck.h`) are **dual-target**: the
same source compiles for MVS *and* runs natively. `make test-host` compiles each
`[[test]]` with the host compiler and runs it, gating on the exit code -- a
fast inner loop with no MVS round-trip. A test carrying hand-written `.asm`/`.s`
is MVS-only and skipped.

A pure-C test can also be MVS-only when it uses runtime services that have no
host equivalent (e.g. `__linkds`/LINK, `wtof`) — a LINK-based integration test
that drives the deployed load modules can't build on the host. Mark it with
`host = false` to skip it on `test-host` (it still runs under `test-mvs`):

```toml
[[test]]
name = "TREXXVL"
startup = "crt1"
host = false          # MVS-only (uses __linkds/LINK); skipped by test-host
sources = ["test/trexxvl.c"]
```

The mirror image: a pure-C test can also be **host-only** when its fixtures
only resolve on the host (e.g. a corpus loaded from a host-relative path like
`test/cfg/`, never staged as MVS datasets). Mark it with `mvs = false` to drop
it from the MVS build entirely -- it is never cross-compiled/linked, so it
never appears in `make test`/`test-mvs` (it still runs under `test-host`):

```toml
[[test]]
name = "TSTCFG"
startup = "crt1"
mvs = false            # host-only (test/cfg/ fixture path); skipped by test-mvs
sources = ["test/tstcfg.c", "src/nsfcfg.c"]
```

When a symbol resolves differently per environment (e.g. `is_tso()` -> an asm
CSECT on MVS, a `#ifndef __MVS__` stub on the host), and when a dependency must
be linked from source on the host (the staged `.mbt/deps` `.a` is the cross
build), declare it under `[host]`:

```toml
[host]
cflags  = ["-Wall", "-Wextra"]
sources = ["../lstring370/src/lstr#*.c"]          # extra host link sources
replace = { "asm/istso.asm" = "src/irx#env.c" }   # per-env source swap
```

The host build reuses the project's `build.cflags` + the dependency include dirs
+ `mbt/include`, with the native compiler (`[host].cc`, default `cc`).

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

### When the job itself does not run

If the job fails as a whole — JES rejects it for a JCL error, or the poll
expires — not one step reaches an RC. There is nothing to say about the tests
in that case, so the runner reports the job instead of printing a matrix:

```
[mbt] ERROR: runner job MBTTEST JOB01028 was rejected -- no test ran
[mbt]        IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD    (STMT 26)
[mbt]        the generated JCL is in build/test-runner.jcl
```

The exit code is 4 (mainframe error), not 1 (tests failed), so CI can tell the
two apart. `build/test-runner.jcl` is written before the submit, so it is always
there to look at — the statement number points into it; `build/test-runner.spool`
is written whenever a job came back at all.

A partial result is different and still yields a matrix: if some steps ran, the
`NO RC` cells are real information about those steps — *provided the spool was
read in full*, which is the next case.

### When the spool cannot be read back

The tests running and their results arriving are two different things. If the
jobs API cannot return the spool, the run reports that, rather than the "no test
ran" it looks like from the empty result:

```
[mbt] ERROR: runner job MBTTEST JOB01179 ran, but its output could not be read -- no test result
[mbt]        HTTP 500 Internal Server Error for GET /restjobs/jobs/MBTTEST/JOB01179/files
[mbt]        this is the readback failing, not the job -- the tests may well have passed
[mbt]        the return codes are on the console: IEFACTRT writes the per-step RC to SYSLOG,
[mbt]          IEFACTRT B05     /TSTEXPIR/00:00:00.02/00:00:00.05/00000/MBTTEST
[mbt]                                                             ^^^^^ step RC
[mbt]        and $HASP165 carries the job-level MAX COND CODE
[mbt]        whatever was read is in build/test-runner.spool
```

Exit code 4 again — it is a mvsMF communication error, which is what 4 means.
The console hint is the recoverable part: `IEFACTRT` and `$HASP165` need no
working REST API, so a run whose results are sitting on the spool intact can be
read off SYSLOG instead of rerun.

When only *some* `/records` calls fail, the steps whose verdict did arrive are
printed as usual and the rest show `??`, counted as neither pass nor fail:

```
  TEST       BATCH          TSO
  ---------- -------------- --------------
  TSTA       ok CC 0        ok CC 0
  TSTB       ??   NO RC     ??   NO RC
```

A `??` never becomes a test failure — the exit code is 1 only if a step really
returned nonzero, and 4 when `??` cells are all that is missing. The assertion
tally under the matrix is short by whatever was not read, and says so.

### Running a subset

Pass `--only` (repeatable) to build, deploy and run just the named tests --
e.g. to rerun the failures from a previous run:

```sh
make test-mvs ARGS="--only TSTLOAD --only TSTJCL"
```

### Fixtures (input DDs + pre-loaded members)

Some tests need a DD with pre-loaded PDS members at runtime -- e.g. a LOAD test
that reads REXX execs from `SYSEXEC` (the host build self-provisions these under
`#ifndef __MVS__`; on MVS they must be real datasets). Declare them per test:

```toml
[[test]]
name = "TSTLOAD"
sources = [...]
[[test.fixture]]
dd = "SYSEXEC"
members = ["test/fixtures/tstload/HELLO", "test/fixtures/tstload/EMPTY"]
[[test.fixture]]
dd = "ALTDD"
members = ["test/fixtures/tstload/ALTM"]
```

For each fixture test the runner allocates a **per-test** fixture PDS
(`{HLQ}.{PROJECT}.FIX.{TEST}` -- per-test so member names may collide across
tests), loads each member via an `IEBGENER` step (`DLM=` so a `/* ... */` REXX
comment in the data does not end the instream early; member name = file basename
uppercased), and adds each declared `dd` to that test's batch + TSO steps.

### Per-leg arguments (environment-dependent tests)

A test whose correct result depends on the run environment can't know it a
priori -- so the runner passes the **expected** value as the program argument,
differently per leg, and the test asserts against it. Declare:

```toml
[[test]]
name = "TISTSO"
sources = ["test/mvs/tistso.c", "asm/istso.asm"]
parm_batch = "0"     # batch leg -> PARM='0'  (expect is_tso()==0)
parm_tso   = "1"     # TSO leg   -> CALL '...' '1'  (expect is_tso()==1)
```

`parm` sets the same argument for both legs; `parm_batch` / `parm_tso` override
per leg. The argument reaches `main(argc, argv)` via the batch `PARM=` and the
TSO `CALL 'ds(mem)' 'arg'` form (crent370 reconstructs argv for both). This
turns an otherwise un-gateable diagnostic into a real pass/fail test on both
legs.

## How evaluation works

- **Gate:** each test is one job step; the runner parses the step's RC from the
  spool (`IEF142I … COND CODE nnnn`, or `IEF450I … ABEND` → fail). RC 0 = pass.
- **Count (informational):** the runner counts `PASS:` / `FAIL:` lines across
  the spool — uniform because every test emits them via `mbtcheck.h` (the
  per-test summary line formats vary, so they are not used for the count).

The RC is the contract; the output is for humans.
