#import "../bookmaster/bookmaster.typ": *

= The mbt Command <ref-cmd>

#idx("mbt command")
```
mbt command [options] [arguments]
```

#cmd("mbt") is run in the directory of a project, the one that holds its
#cmd("mbt.toml"), except #cmd("mbt target") and #cmd("mbt version"), which
need no project. #cmd("mbt") without a command lists the commands and ends
with return code 2\; #cmd("mbt help"), #cmd("-h") and #cmd("--help") list
them with return code 0. Options may stand before or after the arguments,
and #cmd("--") ends them. A long option is written with one or two
hyphens.

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

Before it links, #cmd("mbt build") runs the check of
#cmd("mbt module-data"). It also checks that the installed LIBC/370 is not
older than #cmd("[toolchain] libc370") (return code 1 otherwise), and that
the declared dependencies are staged (return code 3). A #var("NAME") that
is no module or test builds nothing.

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
per C source of the modules, tests, #cmd("[lib]") and #cmd("[internal]"),
for the language server #cmd("clangd"): #cmd("clang") commands with the
options #cmd("clangd") needs to read the sources as CC/370 does
(#cmd("-D__MVS__"), no host headers), #cmd("-std=gnu99"),
#cmd("[build] cflags") and the include directories.

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

A project without load modules has nothing to deploy and ends with return
code 0. An unknown #cmd("--module") is return code 2, a failed pack 1, every
failure on MVS 4.

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
(#cmd("cc370"), #cmd("as370"), #cmd("ld370"), #cmd("ar370"),
#cmd("xmit370")), and logs on to the target, read-only\; the target is
chosen as for every command, #cmd("MBT_TARGET") included. Errors, an
unreachable mvsMF among them, end it with return code 2\; a CC/370 older
than #cmd("[toolchain] cc370") is a warning.

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
file would describe a different project than the old one: it says
#cmd("nothing written") and ends with return code 99. Notes go to standard
error.

#deflist(width: 1.2in,
  [#cmd("--dry-run")], [print the new file to standard output instead.],
)

== mbt module-data <ref-cmd-moddata>

```
mbt module-data [--all] [--raw]
```

Checks the sources of the load modules for writable data: definitions
outside functions and #cmd("static") definitions inside them that are not
#cmd("const"). Writable data in a #cmd("rent = true") module is an error,
in an #cmd("ac = 1") module, or a module without a #cmd("rent")
declaration (an MBT 2 project), a warning. Modules with
#cmd("rent = false") and no #cmd("ac"), and tests, are not checked.

#deflist(width: 1.2in,
  [#cmd("--all")], [list every warning, not three per module. Errors are
    always listed in full.],
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
package #cmd("-dist.zip") and #cmd("-dist.tar.gz") (@ref-smp). A release
version whose FMID cannot be derived is refused (@ref-toml-smp)\; a
prerelease gets a warning and no installation package, and the other files
are written.

== mbt prerelease <ref-cmd-prerelease>

```
mbt prerelease
```

Tags the current commit #cmd("v")#var("version") with the version in
#cmd("mbt.toml"), such as #cmd("v1.4.0-dev"), moving the tag if it exists,
and pushes it. The working tree must be clean, and the project must own the
repository's tags: its project file is at the root, or it is the only one
in the repository. A version without #cmd("-dev") or #cmd("-rc")#var("n")
is refused.

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
repository's tags (@ref-cmd-prerelease), and, for a product with an SMP
package, when the next version has a number above 9. A step that fails is
not undone.

#deflist(width: 1.5in,
  [#cmd("--next") #var("version")], [the next development version, a
    #cmd("-dev") version. Default: the patch number plus one.],
)

== mbt run <ref-cmd-run>

```
mbt run [name] [-v] [--dry-run] [-- arguments...]
```

Runs a command or a task of the Lua of the project and of
#cmd("~/.mbt/init.lua") (@ref-lua). Without #var("name"), lists them. A task
runs even when it is up to date. The arguments after #cmd("--") reach a
command as #cmd("ctx.args")\; a task gets none.

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
#var("name"), on the selected target. #cmd("mbt target") alone is
#cmd("list"), which also shows the target #cmd("env") when
#cmd("MBT_TARGET_*") defines one. #cmd("ping") ends with return code 4 only
when mvsMF does not answer.

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
    #var("name"), filling what it leaves out from the environment and
    #cmd("~/.mbt/config.toml").],
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
    Default: #cmd("[deploy] target"). If it does not exist on MVS, the
    tests run from the test library alone.],
  [#cmd("-v")], [on the workstation, the compile commands and the
    compiler's errors\; with #cmd("--mvs"), the pack commands and the
    deploy's details\; with #cmd("--tso"), a trace of the 3270 data stream
    on standard error.],
)

== mbt version <ref-cmd-version>

```
mbt version
```

Prints the version of MBT, the commit it was built from and the date.
#cmd("mbt --version") is the same.

== The Launcher <ref-cmd-launcher>

#idx("launcher")
Every #cmd("mbt") checks #cmd("[toolchain] mbt") of the project before it
runs a command. #cmd("\"3.0\"") accepts any 3.0._x_, #cmd("\"3.0.2\"") only
that version\; #cmd("\"3.0\"") also accepts prereleases of 3.0. When the
running #cmd("mbt") does not match, the highest matching one under
#cmd("~/.mbt/versions/") runs instead. Only when none is there is one
downloaded from the releases of #cmd("mvslovers/mbt"), a release preferred
to a prerelease, and checked against their SHA-256 list.
#cmd("MBT_NO_SWITCH=1") keeps the running one. A pin that is no version is
return code 2, a version that cannot be fetched 3. #cmd("help") is never
switched.

== mbt ci-info <ref-cmd-ci-info>

```
mbt ci-info
```

For workflows: prints #cmd("PROJECT_NAME"), #cmd("PROJECT_VERSION"),
#cmd("PROJECT_FILE"), #cmd("CC370_REF"), #cmd("LIBC370_REF") and
#cmd("MBT_PIN") from the project file.
