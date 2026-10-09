#import "../bookmaster/bookmaster.typ": *

= Introducing MBT <ug-intro>

#idx("MBT")
MBT is the build tool for programs that run on MVS 3.8j and are built on a
workstation. It reads a short description of a project, the file
#cmd("mbt.toml"), and from it compiles, assembles and links the project with
the CC/370 cross-toolchain, runs its tests on the workstation and on MVS,
sends its load modules to a system to try them, and packages a release so
that it can be installed with SMP. This chapter describes what MBT does,
which terms it uses, and how its version is chosen for each project.

== What MBT Does <ug-intro-what>

#idx("MBT", "tasks")
A project for MVS has more steps than compiling: C and assembler sources
become object modules, object modules become load modules or a library,
other projects' libraries have to be fetched in the right version, tests have
to run, and the result has to reach MVS. MBT does each of these with one
command:

#tab(caption: [What MBT does, and where])[
  #table(columns: (1.6in, 1fr, 0.9in),
    [Command], [What it does], [Touches MVS],
    [#cmd("mbt build")], [compiles, assembles and links the project with
      CC/370], [no],
    [#cmd("mbt deps")], [fetches the libraries the project depends on, in
      the versions it pins], [no],
    [#cmd("mbt test")], [builds the tests for the workstation and runs
      them there], [no],
    [#cmd("mbt package")], [writes the files of a release, including the
      SMP installation package], [no],
    [#cmd("mbt release")], [sets the version and tags the release], [no],
    [#cmd("mbt deploy")], [sends the load modules to an MVS system and
      receives them into a development library], [yes],
    [#cmd("mbt test --mvs")], [runs the tests on MVS, in batch and under
      TSO], [yes],
  )
] <ug-intro-tab>

#idx("host build")
*The build runs on the workstation.* Everything that turns a source into a
load module happens on macOS, Linux or Windows, with the commands of CC/370:
#cmd("cc370"), #cmd("as370"), #cmd("ld370") and #cmd("ar370"). MVS is not
needed to build a project. Only deploying and testing on MVS reach a
system, and they do so through mvsMF, a REST interface that runs on MVS.

*MBT is one program.* It is installed once on the workstation and serves
every project there (@ug-install). A project holds no copy of it, no
#cmd("Makefile") and no build scripts: its directory contains the project
file, the sources and, once it has dependencies, a lock file.

== Projects, Modules and Libraries <ug-intro-terms>

#idx("project")#idx("load module")#idx("library")
MBT builds _projects_. A project is a directory with a file
#cmd("mbt.toml") in it, usually a Git repository. The project file names the
project, its version and its kind, and describes what the project produces:

- *Load modules.* Each is a member of a load library on MVS: a program that
  is run with #cmd("EXEC PGM=")#var("name"), or a module that another
  program loads. A project can build several. The project file has a table
  for each, #cmd("[module.")#var("NAME")#cmd("]"), named like the member.
- *A library.* A static library of object modules, which other projects
  link their programs with. A project builds at most one, described in
  #cmd("[lib]"), and publishes it with its releases together with the
  headers that declare its functions.
- *Tests.* Small programs that check the project, each a load module of its
  own. MBT finds them under #cmd("test/") by itself.

#idx("project", "kind")
The project's _kind_ says what it is: an #cmd("application"), a
#cmd("library"), or a #cmd("module") such as a server module that another
program loads. Only #cmd("library") changes what MBT does: #cmd("mbt build")
without arguments builds the library of such a project, and the load modules
of any other. #cmd("application") and #cmd("module") build the same and
record what the project is.

#note[*To be confirmed:* whether #cmd("module") will come to mean more
than #cmd("application").]

#idx("target")
The MVS systems a project is deployed to and tested on are _targets_. They
are not part of the project: they belong to the workstation and are
described once, in the file #cmd("~/.mbt/targets.toml") (@ug-targets). So
every project on the workstation can use them, and no password is kept in a
project directory.

== One Version of MBT per Project <ug-intro-version>

#idx("MBT", "version")#idx("launcher")
A project names the version of MBT it is built with:

```
[toolchain]
mbt = "3.0"
```

#cmd("\"3.0\"") means any release 3.0._x_, #cmd("\"3.0.2\"") exactly that
one. When the installed #cmd("mbt") is another version, it fetches the one
the project names into #cmd("~/.mbt/versions/"), checks it against the
checksums published with that release, and runs it instead. So every
project builds with the MBT it was written for, however many projects share
the workstation, and a project that is not ready for a newer MBT keeps its
pin. The environment variable #cmd("MBT_NO_SWITCH=1") keeps the #cmd("mbt")
that was started.

#cmd("[toolchain] cc370") and #cmd("[toolchain] libc370") name the
releases of CC/370 and of its C library LIBC/370 that a release of the
project is built with. They do not select a toolchain on the workstation,
which builds with the one it has installed; MBT only checks that one
against them (@ug-deps).

== MBT 2 and MBT 3 <ug-intro-mbt2>

#idx("MBT 2")
This book describes MBT 3. Its predecessor, MBT 2, was not a program of its
own but a Git submodule in every project, driven by #cmd("make"), and it
read the project from #cmd("project.toml"). MBT 3 builds the same object
modules and load modules from the same sources and can still read a
#cmd("project.toml"). @ug-migrate shows how to move a project from MBT 2 to
MBT 3.
