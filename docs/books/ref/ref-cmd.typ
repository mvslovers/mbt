#import "../bookmaster/bookmaster.typ": *

= The mbt Command <ref-cmd>

#idx("mbt command")
```
mbt command [options] [arguments]
```

#cmd("mbt") is run in the directory of a project, the one that holds its
#cmd("mbt.toml"), except #cmd("mbt target") and #cmd("mbt version"), which
need no project. #cmd("mbt") without a command lists the commands and ends
with return code 2. Options follow the command\; a long option is written
with one or two hyphens, #cmd("-v") or #cmd("--v") alike.

When #cmd("mbt.toml") pins another version of MBT in
#cmd("[toolchain] mbt"), the command is run by that version
(@ref-cmd-launcher). The return codes are listed in @apx-messages.

== mbt build <ref-cmd-build>

#idx("mbt build")
```
mbt build [--all] [--tests] [-j n] [-v] [NAME...]
```

Compiles, assembles, archives and links. Without arguments it builds the
main product: the library of a project of kind #cmd("library"), the load
modules of any other.

#deflist(width: 1.2in,
  [#cmd("--all")], [the load modules and the library.],
  [#cmd("--tests")], [also the test load modules.],
  [#cmd("-j") #var("n")], [run up to #var("n") steps at once. Default: the
    number of processors.],
  [#cmd("-v")], [print each command instead of a short line per step.],
  [#var("NAME")], [build only these load modules or tests, named in any
    case.],
)

Before linking a module declared #cmd("rent = true") or #cmd("ac = 1"),
#cmd("mbt build") runs the check of #cmd("mbt module-data"). It also
checks that the installed LIBC/370 is not older than
#cmd("[toolchain] libc370"), and that the declared dependencies are
staged.

== mbt check <ref-cmd-check>

```
mbt check
```

Runs #cmd("mbt test") and, if it passed, #cmd("mbt test --mvs").

== mbt clean, mbt distclean <ref-cmd-clean>

```
mbt clean
mbt distclean
```

#cmd("clean") removes #cmd("build/") and #cmd("dist/").
#cmd("distclean") also removes #cmd(".mbt/") with the staged dependencies,
tools and plugins. #cmd("mbt.lock") is never removed.

== mbt compiledb <ref-cmd-compiledb>

```
mbt compiledb
```

Writes #cmd("compile_commands.json") in the project directory, one entry
per C source, with the options #cmd("mbt build") uses.

== mbt deploy <ref-cmd-deploy>

#idx("mbt deploy")
```
mbt deploy [--target name] [--linklib dsn] [--module M]... [--dry-run] [-v]
           [--ignore-space] [--reallocate]
```

Puts the built load modules into the development library on a target,
replacing members of the same name. The library is never deleted unless
#cmd("--reallocate") is given.

#deflist(width: 1.5in,
  [#cmd("--target") #var("name")], [the target (@ref-targets-select).],
  [#cmd("--linklib") #var("dsn")], [deploy into this library instead of
    #cmd("[deploy] target").],
  [#cmd("--module") #var("M")], [deploy only this module. Repeatable.],
  [#cmd("--dry-run")], [pack the modules and report what would be done\;
    no logon.],
  [#cmd("-v")], [print the commands and jobs.],
  [#cmd("--ignore-space")], [copy although the free space of the library
    looks too small.],
  [#cmd("--reallocate")], [delete the library and allocate it again,
    larger, on the same volume, then deploy. Refused while the library is
    held.],
)

== mbt deps <ref-cmd-deps>

#idx("mbt deps")
```
mbt deps [--update]
```

Stages the dependencies, tools and plugins in #cmd(".mbt/"), at the
versions #cmd("mbt.lock") pins\; a dependency not yet in the lock is
resolved and added. A locked version outside its range, or an archive whose
checksum differs from the lock, is an error (return code 3).

#deflist(width: 1.2in,
  [#cmd("--update")], [resolve every range again and rewrite
    #cmd("mbt.lock").],
)

== mbt dist <ref-cmd-dist>

```
mbt dist
```

Writes the SMP installation package again from the load TRANSMIT file
already in #cmd("dist/"), without building.

== mbt doctor <ref-cmd-doctor>

#idx("mbt doctor")
```
mbt doctor [--offline]
```

Checks the commands of CC/370, the toolchain's directory tree with
LIBC/370, their versions against #cmd("[toolchain]"), and the project file,
and logs on to the target, read-only. Errors end it with return code 2\; a
CC/370 older than #cmd("[toolchain] cc370") is a warning.

#deflist(width: 1.2in,
  [#cmd("--offline")], [check the workstation only: no connection, no
    logon.],
)

== mbt migrate <ref-cmd-migrate>

```
mbt migrate [--dry-run]
```

Reads #cmd("project.toml") of MBT 2, writes #cmd("mbt.toml"), and removes
#cmd("project.toml") and #cmd("VERSION"). It writes nothing when the new
file would describe a different project than the old one. Notes go to
standard error.

#deflist(width: 1.2in,
  [#cmd("--dry-run")], [print the new file to standard output instead.],
)

== mbt module-data <ref-cmd-moddata>

```
mbt module-data [--all] [--raw]
```

Checks the sources of modules declared #cmd("rent = true") or
#cmd("ac = 1") for writable data: definitions outside functions and
#cmd("static") definitions inside them that are not #cmd("const").
Writable data in a #cmd("rent = true") module is an error, in an
#cmd("ac = 1") module a warning.

#deflist(width: 1.2in,
  [#cmd("--all")], [list every finding, not three per module.],
  [#cmd("--raw")], [scan the sources as written, without running the
    preprocessor first.],
)

== mbt package <ref-cmd-package>

#idx("mbt package")
```
mbt package [-j n]
```

Builds the load modules and the library and writes the release files into
#cmd("dist/"): #var("name")#cmd("-")#var("version")#cmd("-load.xmit"),
#cmd("-lib.tar.gz"), and, with #cmd("[distribution]"), the installation
package #cmd("-dist.zip") and #cmd("-dist.tar.gz") (@ref-smp). A version
whose FMID cannot be derived is refused (@ref-toml-smp).

== mbt prerelease <ref-cmd-prerelease>

```
mbt prerelease
```

Tags the current commit #cmd("v")#var("version") with the version in
#cmd("mbt.toml"), such as #cmd("v1.4.0-dev"), moving the tag if it exists,
and pushes it. The working tree must be clean, and the project must own the
repository's tags: its project file is at the root, or it is the only one
in the repository.

== mbt release <ref-cmd-release>

#idx("mbt release")
```
mbt release version [--next version]
```

Releases the tree's #var("version")#cmd("-dev") as #var("version"): sets
it, commits #cmd("release: v")#var("version"), tags, pushes, then sets the
next development version, commits #cmd("chore: bump to ")#var("next") and
pushes. Refused, with return code 2, unless the tree is clean and at a
prerelease of #var("version") (#cmd("-dev") or #cmd("-rc")#var("n")), when
the tag exists locally or on #cmd("origin"), for a patch release with an SMP
package but no explicit #cmd("fmid"), and when the project does not own the
repository's tags (@ref-cmd-prerelease). A step that fails is not undone.

#deflist(width: 1.5in,
  [#cmd("--next") #var("version")], [the next development version, a
    #cmd("-dev") version. Default: the patch number plus one.],
)

== mbt run <ref-cmd-run>

```
mbt run [name] [-v] [--dry-run] [-- arguments...]
```

Runs a command or a task of the project's Lua (@ref-lua). Without
#var("name"), lists them. A task runs even when it is up to date. The
arguments after #cmd("--") reach the code as #cmd("ctx.args").

#deflist(width: 1.2in,
  [#cmd("-v")], [show each program run and its output.],
  [#cmd("--dry-run")], [run the Lua, but start no program and write
    nothing.],
)

== mbt target <ref-cmd-target>

#idx("mbt target")
```
mbt target list
mbt target ping [name] [--wait seconds]
mbt target info [name] [--wait seconds]
mbt target console [name] -- command
mbt target import file --name name
```

Works on the targets of #cmd("~/.mbt/targets.toml") (@ref-targets). Without
#var("name"), on the selected target.

#deflist(width: 1.2in,
  [#cmd("list")], [the targets, the default marked #cmd("*"), with where
    each password comes from.],
  [#cmd("ping")], [asks each way of reaching the target whether it answers,
    without logging on.],
  [#cmd("info")], [logs on to mvsMF and shows what runs there.],
  [#cmd("--wait") #var("s")], [wait up to #var("s") seconds for mvsMF to
    answer.],
  [#cmd("console")], [issues an operator command and shows the reply.],
  [#cmd("import")], [turns an MBT 2 #cmd(".env") file into the target
    #var("name").],
)

== mbt test <ref-cmd-test>

#idx("mbt test")
```
mbt test [--only NAME]... [-v]
mbt test --mvs [--only NAME]... [--no-deploy] [--target name] [--linklib dsn] [-v]
mbt test --tso [--only NAME]... [--target name] [-v]
```

Without #cmd("--mvs") or #cmd("--tso"), builds the tests with the
workstation's C compiler and runs them there.

#deflist(width: 1.5in,
  [#cmd("--mvs")], [build the test load modules, deploy them to the test
    library and run each in batch and under TSO on the target.],
  [#cmd("--tso")], [run #cmd("test/tso/*.lua") in TSO sessions over
    TN3270.],
  [#cmd("--only") #var("NAME")], [only this test. Repeatable.],
  [#cmd("--no-deploy")], [with #cmd("--mvs"): use the test library already
    on MVS.],
  [#cmd("--target") #var("name")], [the target.],
  [#cmd("--linklib") #var("dsn")], [with #cmd("--mvs"): the library the
    tests run against, after the test library in the #cmd("STEPLIB").
    Default: #cmd("[deploy] target").],
  [#cmd("-v")], [print the commands and every line of the tests'
    output.],
)

== mbt version <ref-cmd-version>

```
mbt version
```

Prints the version of MBT, the commit it was built from and the date.

== The Launcher <ref-cmd-launcher>

#idx("launcher")
Every #cmd("mbt") checks #cmd("[toolchain] mbt") of the project before it
runs a command. #cmd("\"3.0\"") accepts any 3.0._x_, #cmd("\"3.0.2\"") only
that version. When the running #cmd("mbt") does not match, the matching
one under #cmd("~/.mbt/versions/") runs instead, downloaded first from the
releases of #cmd("mvslovers/mbt") and checked against their SHA-256 list
if it is not there. #cmd("MBT_NO_SWITCH=1") keeps the running one.
