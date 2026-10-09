#import "../bookmaster/bookmaster.typ": *

= Testing <ug-test>

#idx("test")
MBT runs a project's tests in three places: on the workstation, which takes
seconds and needs no MVS\; on MVS, in batch and under TSO, as load modules\;
and in a TSO session at a 3270 terminal, for what only an interactive user
sees. This chapter describes how tests are written and found, and how each
of the three runs works.

== What a Test Is <ug-test-what>

#idx("test", "return code")
A test is a program. It returns 0 when everything it checked was right, and
anything else when something was not. *That return code is all MBT relies
on:* on the workstation it is the exit code, on MVS the condition code of
the job step. A test may print what it likes.

#idx("mbtcheck.h")
The header #cmd("<mbtcheck.h>"), which comes with MBT, makes such a program
short and its output uniform (@ug-first-tstadd):

#deflist(width: 2.1in,
  [#cmd("CHECK(")#var("cond")#cmd(", ")#var("msg")#cmd(")")], [records
    one check and prints #cmd("PASS:") or #cmd("FAIL:") with #var("msg").],
  [#cmd("CHECK_EQ(")#var("got")#cmd(", ")#var("want")#cmd(", ")#var("msg")#cmd(")")],
    [the same for two numbers, printing both when they differ.],
  [#cmd("mbt_test_summary(")#var("name")#cmd(")")], [prints the summary
    and returns 0 when every check passed, else 1.],
)

MBT counts the #cmd("PASS:") and #cmd("FAIL:") lines for its report, but
decides by the return code alone. One test is one source with one
#cmd("main"), because the counters of #cmd("<mbtcheck.h>") belong to the
source that includes it.

== Finding the Tests <ug-test-find>

#idx("test", "discovery")
Every #cmd(".c") and #cmd(".asm") file under #cmd("test/"), in any
subdirectory, is a test, named after its file in upper case:
#cmd("test/mvs/tstgctx.c") is #cmd("TSTGCTX"). The name becomes a member
name and the #cmd("PGM=") of a job step, so it must be a valid member
name: at most eight characters, letters, digits, #cmd("@"), #cmd("#") and
#cmd("$"), not starting with a digit. A file whose name is not is an
error that names it.

A test links its own source, the private archive of #cmd("[internal]")
(@ug-build-internal), the dependencies and the C library. #cmd("[tests]")
gives what all tests share:

```
[tests]
rent    = false
reus    = false
exclude = ["test/helpers/fixture.c"]    # under test/, but not a test
```

#cmd("rent") and #cmd("reus") are required once a project has a test, as
for a load module. #cmd("exclude") names files under #cmd("test/") that are
not tests, such as helpers.

=== A Test That Differs <ug-test-entry>

#idx("[test.NAME]")
A table #cmd("[test.")#var("NAME")#cmd("]") is needed only where a test
differs from the rule:

```
[test.TSTSHA]
sources = ["test/mvs/tstsha.c", "src/sha256i.c", "src/sha256u.c"]

[test.TREXXVL]
host = false                         # MVS only

[test.TISTSO]
parm = { batch = "0", tso = "1" }    # a different argument per run

[test.TSTLOAD]
fixtures = { SYSEXEC = ["test/fixtures/HELLO", "test/fixtures/EMPTY"] }
```

#deflist(width: 1.1in,
  [#cmd("sources")], [the test's sources, when it needs more than its own
    file. A source named here by another test is not a test of its own.],
  [#cmd("host")], [#cmd("false"): do not run it on the workstation, for a
    test that uses services only MVS has.],
  [#cmd("mvs")], [#cmd("false"): do not build or run it for MVS, for a test
    that needs files only the workstation has.],
  [#cmd("parm")], [the argument of the test program, the same in both MVS
    runs, or #cmd("{ batch = ..., tso = ... }") for one each
    (@ug-test-mvs).],
  [#cmd("fixtures")], [data sets the test reads on MVS
    (@ug-test-fixtures).],
)

A test table also takes the keys of a load module, such as #cmd("entry"),
#cmd("startup") and #cmd("ac") (@ug-build-module-keys).

== On the Workstation: mbt test <ug-test-host>

#idx("mbt test")#idx("host test")
#cmd("mbt test") compiles every test with the C compiler of the workstation
and runs it there, with MBT's warning options and C99. A test written in
portable C, as the ones above, runs on the workstation and on MVS from the
same source. A test with an assembler source runs on MVS only.

```
mbt test                       # every test
mbt test --only TSTSHA         # one; repeat --only for more
mbt test -v                    # the commands and every line of output
```

When a test needs something on the workstation that MVS provides
otherwise, #cmd("[build.host]") supplies it for the workstation build
alone:

```
[build.host]
cflags  = ["-Wno-unused-parameter"]
sources = ["../lstring370/src/lstr#*.c"]           # extra sources
replace = { "asm/istso.asm" = "src/irx#env.c" }    # this one instead of that one
```

This is the fastest check there is: run it after every change.

== On MVS: mbt test --mvs <ug-test-mvs>

#idx("mbt test", "--mvs")#idx("TESTLIB")
#cmd("mbt test --mvs") builds the tests as load modules, sends them to the
target (@ug-targets) and runs them there:

+ The test modules go into a library of their own,
  #var("hlq")#cmd(".")#var("PROJECT")#cmd(".")#var("VvRrMm")#cmd(".TESTLIB"),
  such as #cmd("IBMUSER.SUMS.V1R0M0D.TESTLIB") for version 1.0.0-dev. Tests
  never go into the library the project deploys to.
  #cmd("[deploy] test_target") names another one.
+ MBT writes a job with two steps for each test: one runs the program in
  batch (#cmd("EXEC PGM=")), the other under TSO (#cmd("IKJEFT01") with
  #cmd("CALL")). The development library of the project
  (#cmd("[deploy] target"), @ug-deploy), or the library #cmd("--linklib")
  names, follows the test library in the #cmd("STEPLIB"), so a test can
  load the project's modules. When that library does not exist yet, the
  tests run from the test library alone, and MBT says so.
+ It submits the job, reads its output and prints a table: one row per
  test, one column for the batch run and one for the TSO run, each with
  its condition code.

Both runs exist because a program can behave differently under TSO. A test
passes only when both end with 0. #cmd("--only") #var("NAME") runs a subset,
#cmd("--no-deploy") reuses the test library already on MVS, and
#cmd("--target") names the system.

#idx("parm")
*Arguments.* #cmd("parm") becomes the #cmd("PARM=") of the batch step and
the argument of the #cmd("CALL") under TSO, and reaches
#cmd("main(argc, argv)") in both. A test whose right answer depends on where
it runs gets the expected answer this way: #cmd("{ batch = \"0\", tso =
\"1\" }") tells it whether to expect TSO.

=== Fixtures <ug-test-fixtures>

#idx("fixture")
A test that reads members of a partitioned data set on MVS declares them as
fixtures: for each DD name, the files that become its members.

```
fixtures = { SYSEXEC = ["test/fixtures/HELLO"], SYSPROC = ["test/fixtures/OLD"] }
```

Before each run, MBT allocates one partitioned data set per test and DD name,
#var("hlq")#cmd(".")#var("PROJECT")#cmd(".FIX.")#var("TEST")#cmd(".")#var("DD"),
sized to what it will hold, loads each file as a member named after the
file in upper case, and adds the DD statement to both steps of the test. A
member is visible only under the DD that declares it, so a test of a search
order can give one DD a member the other lacks.

=== When the Run Itself Fails <ug-test-fail>

#idx("mbt test", "return codes")
A test that fails and a run that could not test anything are different
things, and the return code of #cmd("mbt test --mvs") tells them apart:

#tab(caption: [Return codes of mbt test --mvs])[
  #table(columns: (0.6in, 1fr),
    [Code], [Meaning],
    [0], [every test passed in both runs.],
    [1], [at least one test failed.],
    [4], [the job could not run or its output could not be read: JES
      rejected it, the wait for it ran out, or mvsMF could not return its
      output. MBT shows the reason, and the generated job is in
      #cmd("build/test-runner.jcl").],
    [5], [a fixture could not be loaded, for example because its data set
      ran out of space. The table is shown, but those tests did not run
      against what they declared.],
  )
] <ug-test-rc>

When only part of the output could be read, the steps whose result is
missing show #cmd("??") and count neither as passed nor as failed.

== Everything: mbt check <ug-test-check>

#idx("mbt check")
#cmd("mbt check") runs the tests on the workstation and then on MVS. When
the first run fails, the second does not start.

== Interactive Tests: mbt test --tso <ug-test-tso>

#idx("mbt test", "--tso")#idx("TN3270")
Some things can only be tested the way a user meets them: a full-screen
dialog, a TSO command's prompts, the messages at the terminal. For these,
MBT runs a TSO session of its own over TN3270, as the user of the target's
#cmd("[tn3270]") (@ug-targets), and drives it from Lua.

Each file #cmd("test/tso/*.lua") is one such test. It returns a function
that receives the session:

```
-- test/tso/time.lua
return function(t)
  t:logon()
  t:type("TIME"):enter()
  t:expect("TIME-")
  t:logoff()
end
```

#deflist(width: 1.9in,
  [#cmd("t:logon()"), #cmd("t:logoff()")], [log on as the target's user, and
    off.],
  [#cmd("t:type(")#var("s")#cmd(")")], [type #var("s") at the cursor\; it
    returns the session, so #cmd(":enter()") can follow.],
  [#cmd("t:enter()"), #cmd("t:pf(")#var("n")#cmd(")"), #cmd("t:pa(")#var("n")#cmd(")"),
    #cmd("t:clear()"), #cmd("t:tab()")], [the keys.],
  [#cmd("t:expect(")#var("text")#cmd(", { timeout = ")#var("s")#cmd(" })")],
    [wait until #var("text") is on the screen\; if it does not come, the
    test fails and shows the screen it saw.],
  [#cmd("t:screen()"), #cmd("t:text()"), #cmd("t:cursor()")], [read the
    screen and the cursor position.],
  [#cmd("t.testlib")], [the name of the library #cmd("mbt test --mvs")
    deploys the tests to, for a session that calls one of them.],
)

Each file gets its own logon, and the session is logged off whatever
happens. #cmd("mbt test --tso") does not deploy the test modules itself:
run #cmd("mbt test --mvs") first when a session needs them.
