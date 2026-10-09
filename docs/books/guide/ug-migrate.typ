#import "../bookmaster/bookmaster.typ": *

= Migrating from MBT 2 <ug-migrate>

#idx("MBT 2", "migrating")#idx("mbt migrate")
This appendix takes a project from MBT 2 to MBT 3. The build does not
change: the same CC/370 commands write the same object modules, archives
and load modules. What changes is everything around it.

#tab(caption: [MBT 2 and MBT 3])[
  #table(columns: (1.5in, 1fr, 1fr),
    [], [MBT 2], [MBT 3],
    [The tool], [a Git submodule #cmd("mbt/") and a #cmd("Makefile")], [the
      program #cmd("mbt") on the #cmd("PATH")],
    [Its version], [the submodule's commit], [#cmd("[toolchain] mbt")],
    [Project file], [#cmd("project.toml") and #cmd("VERSION")],
      [#cmd("mbt.toml")],
    [Commands], [#cmd("make") #var("target")], [#cmd("mbt") #var("command")],
    [MVS systems], [#cmd(".env") in each project],
      [#cmd("~/.mbt/targets.toml")],
    [Extra steps], [rules in the #cmd("Makefile")], [Lua, or a plugin],
    [CI], [#cmd("build.yml"), #cmd("release.yml")], [#cmd("build3.yml"),
      #cmd("release3.yml")],
  )
] <ug-migrate-tab>

The project should build with MBT 2 before it is migrated.

== Converting the Project File <ug-migrate-convert>

```
mbt migrate --dry-run     # print the new mbt.toml, change nothing
mbt migrate               # write mbt.toml, remove project.toml and VERSION
```

#cmd("mbt migrate") reads #cmd("project.toml") and writes #cmd("mbt.toml").
Comments travel with the key or table they stand above. Before it writes
anything, it loads the new file and compares it with the old one\; if they
differ in anything but the intended changes, nothing is written. Where it
drops a comment, or where the #cmd("Makefile") does more than include MBT,
it says so on standard error. Then pin the version:

```
[toolchain]
mbt = "3.0"
```

What you will notice in the new file:

- *Modules are tables keyed by name*: #cmd("[[module]] name = \"UFSD\"")
  becomes #cmd("[module.UFSD]"). MBT orders modules by name wherever order
  shows, so the first build lists them in a different order. The load
  modules do not change.
- *#cmd("rent") and #cmd("reus") are stated on every module*;
  #cmd("norent = true") becomes #cmd("rent = false").
- *#cmd("[build]") is shorter.* #cmd("include/") is on the include path by
  convention, and #cmd("-Wall -Wextra -Werror") are MBT's own, so the
  migration leaves all three out. Other #cmd("-I") pairs move to
  #cmd("include").
- *Tests are found* under #cmd("test/") (@ug-test-find). A test needs a
  table only where it differs. The migration keeps every source list that
  differs from the found default, so the test modules stay the same, and
  puts a file under #cmd("test/") that MBT 2 did not build as a test into
  #cmd("[tests] exclude").
- *The version is in #cmd("[project] version") only.* #cmd("VERSION") is
  gone.
- *The FMID is derived* from #cmd("[smp] prefix") and the version
  (@ug-package-fmid).
- *C99 is the default.* MBT 2 compiled C89 with GNU extensions unless the
  project said otherwise, MBT 3 compiles C99. The code generated changes,
  the meaning does not\; run the project's tests on MVS once.
  #cmd("cflags = [\"-std=gnu89\"]") keeps the old dialect.
- *#cmd("startup = \"crt0\"") and #cmd("\"crt1\"") are gone*: the C start-up
  comes out of #cmd("libc.a"), which needs LIBC/370 2.3.0 or later.

A directory with both #cmd("mbt.toml") and #cmd("project.toml") is an
error.

== Removing the Submodule <ug-migrate-submodule>

In the same change, remove the #cmd("Makefile") and the submodule with one
command:

```
git rm Makefile mbt .gitmodules     # mbt was the only submodule
git rm Makefile mbt                 # there are others: keep .gitmodules
rm -rf .git/modules/mbt
git config --remove-section submodule.mbt
```

Keep #cmd(".mbt/"), #cmd("build/") and #cmd("dist/") in #cmd(".gitignore").
The directory #cmd("mbt/") is the project's own from now on: it holds the
project's Lua (@ug-extend), so do not ignore it. Commit #cmd("mbt.lock") as
before.

== Commands <ug-migrate-commands>

#tab(caption: [make targets and mbt commands])[
  #table(columns: (2.3in, 1fr),
    [MBT 2], [MBT 3],
    [#cmd("make")], [#cmd("mbt build")],
    [#cmd("make all")], [#cmd("mbt build --all")],
    [#cmd("make") #var("name")], [#cmd("mbt build") #var("NAME")],
    [#cmd("make test")], [#cmd("mbt build --tests")],
    [#cmd("make test-host")], [#cmd("mbt test")],
    [#cmd("make test-mvs ARGS=\"--only X\"")], [#cmd("mbt test --mvs --only X")],
    [#cmd("make check")], [#cmd("mbt check")],
    [#cmd("make deps")], [#cmd("mbt deps")],
    [#cmd("make package"), #cmd("make dist")], [#cmd("mbt package"),
      #cmd("mbt dist")],
    [#cmd("make deploy")], [#cmd("mbt deploy")],
    [#cmd("make doctor"), #cmd("make compiledb")], [#cmd("mbt doctor"),
      #cmd("mbt compiledb")],
    [#cmd("make clean"), #cmd("make distclean")], [#cmd("mbt clean"),
      #cmd("mbt distclean")],
    [#cmd("make release VERSION=")#var("v")], [#cmd("mbt release")
      #var("v")],
    [#cmd("make prerelease")], [#cmd("mbt prerelease")],
    [#cmd("VERBOSE=1 make")], [#cmd("-v") on the command],
  )
] <ug-migrate-commands-tab>

Behaviour you may notice:

- *#cmd("mbt deps") never moves a pin by itself* (@ug-deps-lock). MBT 2
  re-pinned a prerelease that was published again, with a warning. A lock
  written that way may name an archive that no longer exists: expect the
  first #cmd("mbt deps") of a project that depends on a #cmd("-dev")
  version to ask for #cmd("--update").
- *#cmd("--target") names a system now*, and #cmd("--linklib") the library
  (@ug-deploy-library). The default library is
  #var("NAME")#cmd(".DEV.LINKLIB").
- *#cmd("mbt deploy") replaces members* instead of deleting and receiving
  the library (@ug-deploy).

== MVS Systems <ug-migrate-targets>

Turn the project's #cmd(".env") into a target once per workstation, with
#cmd("mbt target import .env --name") #var("name") (@ug-targets-env). Then
delete #cmd(".env.example") from the project: it documents variables MBT 3
no longer reads.

== Makefile Rules <ug-migrate-make>

A rule in the #cmd("Makefile") beyond MBT's own targets becomes Lua in
#cmd("mbt/init.lua"), a tool in #cmd("[tools]"), or a plugin
(@ug-extend). #cmd("mbt migrate") names the rules it found.

== CI <ug-migrate-ci>

Replace the two workflow files by the ones in @ug-release-ci. Keep any
other job of your own. Then look for anything of the project's own that
reads #cmd("project.toml"), such as a checking script, and for comments
that describe how MBT 2 moved #cmd("VERSION") or the FMID.

== Checking the Result <ug-migrate-check>

#idx("MBT 2", "comparing")
Build the same commit with MBT 2 in a second checkout and compare
#cmd("build/") and #cmd("dist/"). Object modules carry the date of the
assembly and load modules the time of the link\; set the same values on
both sides to compare byte for byte:

```
export ASMDATE=10/07/26 ASMTIME=12.00 LDDATE=26280 LDTIME=120000
```

A project whose #cmd("project.toml") gave no #cmd("-std") compiled C89 with
MBT 2: for the comparison, add #cmd("cflags = [\"-std=gnu89\"]") on the
MBT 3 side, and remove it afterwards. Archives (#cmd(".tar.gz")) differ
anyway in how they are written\; compare what they contain.
