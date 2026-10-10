#import "../bookmaster/bookmaster.typ": *

= Glossary <ug-glossary>

This glossary defines the terms of MBT, and of MVS and SMP as far as this
book uses them. A term printed in _italics_ in a definition has an entry of
its own. The terms of the toolchain itself are in the glossary of the
_CC/370 User's Guide_.

#let g(..items) = deflist(width: 1.45in, ..items.pos().enumerate().map(
  ((i, x)) => if calc.even(i) { par(justify: false, strong(x)) } else { x }))

#g(
  [APF], [The authorized program facility of MVS. A program runs authorized
    only if its _load module_ has authorization code 1 and every library of
    an authorized #cmd("STEPLIB") concatenation is listed as APF-authorized,
    by data set name and volume.],
  [build stamp], [The header #cmd("<buildstamp.h>") that MBT writes for each
    build, with the project's name, version and commit.],
  [deploy], [Putting the load modules of a _project_ into its _development
    library_ on a _target_, with #cmd("mbt deploy").],
  [dependency], [Another _project_ whose _library_ a project links with,
    declared in #cmd("[dependencies]") and pinned in the _lock file_.],
  [development library], [The load library a project deploys to, by
    default #var("NAME")#cmd(".DEV.LINKLIB"). It is never the library _SMP_
    installs into.],
  [distribution library], [The library in which _SMP_ keeps the accepted
    level of each element, the level a #cmd("RESTORE") returns to.],
  [FMID], [The function identifier of a _SYSMOD_ of type #cmd("FUNCTION"):
    #cmd("T"), three letters for the product and three digits for the
    version, such as #cmd("TSUM140").],
  [fixture], [A data set a test reads on MVS, loaded by #cmd("mbt test
    --mvs") from files of the project.],
  [hook], [Lua code that runs before or after one of MBT's commands.],
  [launcher], [The part of #cmd("mbt") that runs the version of MBT a
    project pins, fetching it if need be.],
  [library], [A static library of object modules, an archive, built from
    #cmd("[lib]") and published for other projects.],
  [load module], [An executable program in a load library on MVS, made by
    the linkage editor from object modules.],
  [lock file], [#cmd("mbt.lock"): the exact version and checksum of every
    _dependency_, _tool_ and _plugin_ of a project.],
  [mvsMF], [A REST interface on MVS, compatible in part with z/OSMF,
    through which MBT uploads files, submits jobs and reads their output.],
  [plugin], [Lua published by another repository, declared in
    #cmd("[plugins]") and pinned in the _lock file_.],
  [prerelease], [A version with #cmd("-dev") or #cmd("-rc")#var("n"),
    published before the release. A _dependency_ range gets one only if it
    names one.],
  [project], [A directory with an #cmd("mbt.toml"): what MBT builds.],
  [PTF], [A program temporary fix: a _SYSMOD_ that services a function.
    MBT does not build PTFs yet.],
  [reentrant], [Said of a _load module_ that one copy can serve several
    tasks at once, because it never changes itself. Declared with
    #cmd("rent = true").],
  [serially reusable], [Said of a _load module_ that a copy can be used
    again once a task has finished with it. Declared with
    #cmd("reus = true").],
  [SMP], [The System Modification Program of MVS 3.8j, release 4, which
    installs products and records what it installed.],
  [staging data set], [A data set that holds files on their way: a deploy
    uploads into #var("HLQ")#cmd(".MBT.XMIT.IN") and receives into the
    staging library #var("HLQ")#cmd(".")#var("PROJECT")#cmd(".MBTDPLY")\; an
    installation receives into #var("NAME")#cmd(".")#var("P4")#cmd("LOAD").],
  [SYSMOD], [A system modification: the unit SMP installs, such as a
    #cmd("FUNCTION") for a product release.],
  [target], [An MVS system MBT deploys and tests on, described in
    #cmd("~/.mbt/targets.toml").],
  [task], [Lua code with declared inputs and outputs, run before a command
    when its outputs are out of date.],
  [test library], [The library #cmd("mbt test --mvs") puts the test
    modules into, by default
    #var("HLQ")#cmd(".")#var("NAME")#cmd(".")#var("VRM")#cmd(".TESTLIB")\;
    #cmd("[deploy] test_target") names another.],
  [tool], [A program for the workstation, fetched from a GitHub release and
    pinned like a _dependency_, declared in #cmd("[tools]").],
  [TRANSMIT file], [A file in the format of the TSO #cmd("TRANSMIT")
    command, which TSO #cmd("RECEIVE") turns back into a data set. MBT ships
    load libraries in it.],
)
