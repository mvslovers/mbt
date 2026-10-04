# mbt v2 behaviours — what mbt 3 has to keep

*Compiled 2026-10-04 from the 46 closed mbt issues and the reasoning in
`mk/mbt.mk` and `scripts/`. Together with the output baseline
(`docs/v2-baseline.md`) this is the specification for the rewrite: the
baseline says **what** v2 produces, this page says **how it has to behave**,
and why — usually because the opposite happened once.*

Each line: the behaviour mbt 3 must have, the reason, where v2 implements it,
and whether an automated test covers it today. **"no test" means the behaviour
was verified once, by hand, and mbt 3 needs a test for it from the start.**
Open v2 issues are the other half of the requirements; they are sorted in
`docs/mbt-3-design.md`, Appendix A.

## 1. The build

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| The cross tools are always cc370/as370/ld370/ar370, even where the host predefines `CC`/`AS`/`LD`/`AR` | GNU make predefines them; a plain default would silently run the host's cc/as/ld | `mk/mbt.mk` (toolchain defaults) | no |
| Host `CFLAGS`/`LDFLAGS` from the environment do not leak into the cross build | a Homebrew `LDFLAGS=-L…/libiconv` reached ld370 | `mk/mbt.mk` (`:=`) | no |
| Compile with `-Wall -Wextra -Werror` by default; project flags are added | a missing prototype went through green; behind httpd's 31 warnings were two real defects | #133 | no |
| A failed step leaves no output behind | as370 writes its object deck even at RC 8; the next build called the stale deck up to date, linked it and exited 0 | `.DELETE_ON_ERROR` | no |
| as370 RC < 8 passes, ≥ 8 fails with that RC | IFOX00's own severities; MVS ran the link after a warned assembly (`COND=(8,LT)`) | `mk/mbt.mk` (asm rules) | no |
| Header dependencies are tracked; member names with `#` survive in them, also after a compile that failed | a failed compile left an unescaped `.d` and every later build failed, even after the fix | `-MMD -MP` + escape, #134 | no |
| The build stamp (project, version, commit, dirty) comes from a generated header that is rewritten only when a value changes | `-DCOMMIT` baked into an object nothing recompiled — banners named the previous commit | #59, `.mbt/buildstamp.h` | yes (`test_buildstamp.py`) |
| Every input of a link is a prerequisite: objects, internal archive, libc.a, libcc370rt.a, crt*.o, dependency archives | a libc370 upgrade relinked nothing and `deploy` shipped the old module | #103 | no |
| Link the compiler runtime `-lcc370rt` before `-lc` when the sysroot has it | from cc370 1.1.0 the helpers are not in libc.a; mbt calls ld370 directly | #137 | no |
| Find the sysroot by the **resolved** path of cc370; libc370 in `<sysroot>/lib` or `<sysroot>/libc370/lib`; a fallback says that it fired | Homebrew's `bin/` is a symlink; cc370 1.2.0's second sysroot | #144 | yes (`test_sysroot.py`) |
| Test modules get libc370's `@@START`, never a dependency's | every MVS test exited CC 12 before `main()` with a CGI startup | #58 (`LIBC_FIRST`) | no |
| An internal autocall archive carries code shared by several modules; entry-point sources stay out of it | multi-module projects (httpd); a module's own `__start` must not shadow another's | #48, `[internal]` | no |
| Project kinds `library`, `module`, `application`; a pure library builds and packages its archive | `make` built nothing for lstring370 | #21, #23, #38 | partly |
| Module, test and data set names are valid MVS names: ≤ 8 characters, the project name cut to a valid qualifier | a 9-character test name surfaced as a JCL error; long project names broke `test-mvs` | #19, #73, #127 | yes for #73 and #127 (`test_mbtconfig.py`, `test_mbttest.py`); #19 no |

## 2. The toolchain and its version

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| `[toolchain] libc370` pins what a release builds against; PR builds deliberately float on `main` as an early warning | a release linked an unreleased libc370 | #67, `release.yml` | yes (`test_toolchain.py`) |
| The installed libc370 is checked against the pin: older fails, newer warns only on release | nothing checked it; an exact check blocked normal work | #69, #71 | yes |
| The libc370 version is read from the stamp in `libc.a` (EBCDIC), both stamp spellings | the sysroot has no other version marker | `mbt/sysroot.py` | yes |
| A consumer can hold a libc370 ref in CI during a migration | the 2.0 migration | #121, `build.yml` input | no |

## 3. Dependencies

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| Ranges resolve against GitHub; the cache is only a fallback | a range resolved to the highest *cached* version | #125 | yes |
| A lock entry is re-resolved when its constraint no longer allows it | the lock kept a version the project no longer accepted | #29 | yes |
| Rolling `-dev` prereleases: SHA drift warns and is accepted; stable pins stay strict | a re-published `-dev` asset failed every build | #52 | yes (`test_deps_drift.py`) |
| A GitHub token (`GITHUB_TOKEN`, `MBT_GITHUB_TOKEN`) is sent when set | the unauthenticated API limit answers 403 (seen while measuring the baseline) | `mbt/dependencies.py` | no |

## 4. Talking to MVS — report what happened

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| A job rejected for a JCL error is reported as such, not as a matrix of steps without RC | JES ran no step; the report said every test failed | #74, #76 | yes (`test_mbttest.py`, `test_spool.py`) |
| A spool that cannot be read is not the same as an empty one | "no test ran" for a job whose every step ended RC 0 | #87 | yes (`test_mvsmf_spool.py`) |
| A failed RECEIVE gives its reason and keeps the spool | a sentinel instead of the reason, spool discarded | #78 | yes (`test_mbtdeploy.py`) |
| A timeout or dropped connection while reading is an mbt error, not a crash | the run ended with a traceback although the job had finished | #108 | yes (`test_mvsmf_transport.py`) |
| The RECEIVE step has a `REGION=` | IEBCOPY ran out of buffer storage (IEB135I) | #106 | yes |

## 5. Tests on MVS

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| Host and MVS tests are separate; a test can be marked MVS-only (`host = false`) | pure-C integration tests could not be skipped on the host | #33, #50 | partly |
| Each fixture DD gets its own PDS | a member could not be absent from one DD | #109 | yes |
| Fixture PDSes are sized to their members; a failed load is reported | a fixed TRK(2,1) ended in SE37, hidden by the runner | #122 | yes |
| One result per test, batch and TSO, from the return code | the only hard contract of a test | `mbttest.py` | yes |

## 6. The SMP4 package and the release

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| `make package`/`dist` build an SMP4-installable package (`++FUNCTION`, alloc and install jobs, README) | operators install through SMP | #80 | yes (`test_distribution.py`) |
| `[distribution.smp] delete` lets a release delete its predecessor; the ACCEPT gate is relaxed to RC 4 when it does | a deleted id has no SMPSCDS backup entry (HMA2461, RC 04 after success) | #97 | yes |
| Load module aliases travel as `TALIAS` | without it SMP copies the main member only, CC 0000 everywhere | #112 | yes |
| Generated job names keep their `ALC`/`INS` suffix whatever the project name's length | names of 6+ characters lost it | #130 | yes |
| `prerelease` moves only the tags of the project that owns the repository | a second project in one repository moved the first one's tag | #85 | yes (`test_release_tag_owner.py`) |
| The SMP rules measured on MVS — verify by the member list, not the RC; aliases; DELETE leaves tombstones; `^` does not survive mvsMF | the ecosystem's root context, "SMP4 FMIDs" | (root `CLAUDE.md`) | — |

## 7. IDE

| Behaviour | Why | v2 | Test |
|---|---|---|---|
| `compile_commands.json` is a **clang** database: `arguments[0]` is `clang`, target `powerpc-unknown-eabi` | CLion runs the command (cc370 rejects clang flags); s390x with `-U__LP64__` still analysed as LP64 | #118, #119 | yes (`test_mbtcompiledb.py`) |

## v1 only — not carried over

#14 (SYSLIB BLKSIZE in the MVS assemble step), #22 (`mvslink` max_rc), #27
(`mvspackage` exports), #30 (SE37 compress), #31 and #32 (MVS-side incremental
stamps), #34 (Actions runtime versions): these belong to the v1 build on MVS,
which v2 replaced.

## What this says about mbt 3

- **Most of section 1 has no automated test.** It lives in Make rules and was
  verified by reproducing each failure by hand. mbt 3's build engine needs a
  test per line of section 1 from its first commit — they are the cases the
  engine is most likely to get subtly wrong.
- Sections 3–7 are mostly covered by unit tests today. Their **cases** carry
  over even where the code does not: the tests are the specification.
