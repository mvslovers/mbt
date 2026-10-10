#import "../bookmaster/bookmaster.typ": *

= A First Project <ug-first>

#idx("first project")
This chapter builds a small project from nothing: one C program and one
test. It writes the project file, builds the load module, runs the test on
the workstation and prepares the deployment to MVS. Each step is described
in more detail in the chapters that follow\; here the aim is to go through
all of them once.

You need MBT and CC/370 installed as described in @ug-install. No MVS
system is needed for this chapter.

== The Project Directory <ug-first-dir>

#idx("project", "layout")
Create a directory #cmd("hello") with this layout:

```
hello/
  mbt.toml          the project file
  src/hello.c       the program
  test/tstadd.c     a test
```

MBT expects public headers under #cmd("include/"), which is on the include
path without being named, and tests under #cmd("test/"), where it finds
them by itself. The sources live where the project file lists them\;
#cmd("src/") is the custom. Make the directory a Git repository
(#cmd("git init")): MBT puts the commit it built into a build stamp that a
program can include (@ug-build), and #cmd("mbt release") commits, tags and
pushes a release. Outside a repository the stamp names no commit, and
nothing else fails.

== The Program <ug-first-program>

#fig(caption: [src/hello.c])[
  #code(read("../ex/ug-first/hello.c"), numbers: true)
] <ug-first-hello>

Nothing in it is particular to MVS. On MVS, #cmd("printf") writes to the DD
statement #cmd("SYSPRINT"), and the value #cmd("main") returns becomes the
return code of the job step.

== The Project File <ug-first-toml>

#idx("mbt.toml", "first")
@ug-first-mbt-toml is the whole description of the project.

#fig(caption: [mbt.toml])[
  #code(read("../ex/ug-first/mbt.toml"), numbers: true)
] <ug-first-mbt-toml>

#deflist(width: 1.4in,
  [#cmd("schema = 3")], [the version of the file format. MBT 3 reads
    schema 3.],
  [#cmd("[project]")], [the project's name and version. A version ending
    in #cmd("-dev") is a version being worked on, not yet released. The
    kind of the project is not given, so it is an #cmd("application"),
    whose main product is load modules.],
  [#cmd("[toolchain]")], [the version of MBT the project is built with:
    any 3.0._x_.],
  [#cmd("[module.HELLO]")], [one load module, the member #cmd("HELLO"),
    built from #cmd("src/hello.c"). #cmd("rent") and #cmd("reus") say
    whether the module is reentrant and serially reusable. MBT has no
    default for them: every module states both (@ug-build).],
  [#cmd("[tests]")], [the same two attributes for every test of the
    project.],
)

The entry point of the module, the start-up code of the C library and the
library itself are not named: a C program gets them without being asked.

== Building <ug-first-build>

#idx("mbt build", "first")
In the project directory, run #cmd("mbt build"):

#fig(caption: [Building the project])[
  #screen(raw(read("../ex/ug-first/build.txt")))
] <ug-first-build-fig>

MBT compiled #cmd("src/hello.c") with #cmd("cc370") and linked the load
module #cmd("HELLO") with #cmd("ld370"), with the C library and its
start-up routine #cmd("@@CRT0") as entry point. Only what changed since the
last build is built again: a second #cmd("mbt build") right away compiles
and links nothing and only reports #cmd("Build complete").

=== What the Build Leaves <ug-first-files>

#idx("build directory")
The build writes into the directory #cmd("build/"):

#deflist(width: 1.6in,
  [#cmd("hello.o")], [the object module of #cmd("src/hello.c").],
  [#cmd("hello.d")], [the headers #cmd("hello.c") included, so that a
    change to one of them builds it again.],
  [#cmd("HELLO")], [the load module, as a member of a load library.],
  [#cmd("HELLO.iebcopy")], [the same member in the form in which it is
    sent to MVS.],
)

Besides #cmd("build/"), MBT keeps a directory #cmd(".mbt/") in the project
for what it fetches and stages, and #cmd("dist/") for the files of a release
(@ug-package). None of the three belongs into the repository. Add them to
#cmd(".gitignore"):

```
build/
dist/
.mbt/
```

== Testing on the Workstation <ug-first-test>

#idx("test", "first")
#cmd("test/tstadd.c") is a test. MBT treats every C or assembler source
under #cmd("test/") as one, named after its file in upper case:
#cmd("TSTADD").

#fig(caption: [test/tstadd.c])[
  #code(read("../ex/ug-first/tstadd.c"), numbers: true)
] <ug-first-tstadd>

#cmd("<mbtcheck.h>") comes with MBT. #cmd("CHECK") records whether a
condition holds, #cmd("CHECK_EQ") whether two numbers are equal, each with
a description, and #cmd("mbt_test_summary") prints the result and returns
0 when every check passed. That return code is all MBT looks at: 0 is a
passed test, anything else a failed one.

#cmd("mbt test") compiles each test with the C compiler of the workstation
and runs it there:

#fig(caption: [Running the test on the workstation])[
  #screen(raw(read("../ex/ug-first/test.txt")))
] <ug-first-test-fig>

A test written this way is portable C, so the same source also runs on MVS
as a load module, in batch and under TSO, with #cmd("mbt test --mvs")
(@ug-test). On the workstation it runs in seconds, which makes it the
check to run after every change.

== Preparing the Deployment <ug-first-deploy>

#idx("mbt deploy", "dry run")
#cmd("mbt deploy") sends the load modules to an MVS system and receives
them into the project's development library, #cmd("HELLO.DEV.LINKLIB") by
default. That needs a target (@ug-targets). Without one, #cmd("--dry-run")
shows what would happen, and needs no system and no password:

#fig(caption: [What a deployment would do])[
  #screen(raw(read("../ex/ug-first/deploy.txt")))
] <ug-first-deploy-fig>

The warning says that no target is defined yet, so MBT fell back on the
settings of MBT 2. MBT packed the load module into a TSO TRANSMIT file, which it would upload
to MVS, receive into a staging library, and copy from there into the
development library. @ug-deploy describes the deployment,
and why it goes to a development library of its own.

== What Comes Next <ug-first-next>

- @ug-build describes the build: more modules, assembler sources, a
  library, and the attributes of a module.
- @ug-deps adds libraries of other projects.
- @ug-targets and @ug-deploy bring the project to MVS, @ug-test runs its
  tests there.
- @ug-package and @ug-release turn a version of the project into a release
  that can be installed with SMP.
