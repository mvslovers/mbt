#import "../bookmaster/bookmaster.typ": *

= Packaging and SMP Installation <ug-package>

#idx("mbt package")#idx("SMP")
A release of a project is a set of files: the load modules for MVS, the
library for other projects, and, for a product that is installed on MVS,
an installation package for SMP, the System Modification Program of MVS
3.8j. #cmd("mbt package") writes all of them into #cmd("dist/"). This
chapter describes those files, how a project describes its SMP package, how
its function identifier is chosen, and what an operator should check after
an installation.

== What mbt package Writes <ug-package-files>

#idx("dist directory")
#cmd("mbt package") builds the project, load modules and library both, and
then writes the release files. @ug-package-run shows it for the project
#cmd("sums") of @ug-build, with the SMP package described below.

#fig(caption: [Packaging a release])[
  #screen(raw(read("../ex/ug-package/package.txt")))
] <ug-package-run>

#deflist(width: 2.5in,
  [#var("name")#cmd("-")#var("version")#cmd("-load.xmit")], [the load
    modules, as a TSO TRANSMIT file of a load library.],
  [#var("name")#cmd("-")#var("version")#cmd("-lib.tar.gz")], [the library
    and its headers, in #cmd("lib/") and #cmd("include/"): what
    #cmd("mbt deps") of another project fetches (@ug-deps).],
  [#var("name")#cmd("-")#var("version")#cmd("-dist.zip"), #cmd(".tar.gz")],
    [the installation package for an operator to unpack: the load modules,
    one TRANSMIT file per text library
    (#var("name")#cmd("-")#var("version")#cmd("-")#var("qualifier")#cmd(".xmit")),
    the jobs that install them, the README, and the #cmd("extra") files
    under their own names.],
)

The library archive is written only for a project with #cmd("[lib]"), the
installation package only for one with #cmd("[distribution]"). A project
with #cmd("[distribution]") but no load module is an error: there is
nothing to install.

== Describing the Package <ug-package-describe>

#idx("[distribution]")#idx("[smp]")
Two tables turn #cmd("mbt package") into a product installation:

#fig(caption: [The installation package of sums])[
  #code(read("../ex/ug-package/dist.toml"))
] <ug-package-toml>

#cmd("[distribution]") says what goes into the package besides the load
modules:

#deflist(width: 2.4in,
  [#cmd("readme")], [the file the operator reads first. It is shipped as
    #cmd("README.md").],
  [#cmd("extra")], [further files to ship as they are, such as a disk
    image the project builds.],
  [#cmd("[distribution.library.")#var("dir")#cmd("]")], [a library of
    text members made from the directory #var("dir") of the project, such
    as sample JCL and procedures. It travels as a TRANSMIT file of its own
    and is received with TSO #cmd("RECEIVE"), outside SMP, into
    #cmd("target"), by default #var("NAME")#cmd(".")#var("DIR").],
)

#cmd("[smp]") describes the SMP side: above all the function identifier,
and the data sets the modules pass through.

== The Installation Jobs <ug-package-jobs>

#idx("installation job")
The package holds two jobs. The operator uploads the TRANSMIT files to MVS
in binary, as files of 80-byte records, runs the allocation job once, puts
the names of the uploaded files into the installation job, and submits it.

*The allocation job*, #var("name")#cmd("-")#var("version")#cmd("-alloc.jcl"),
allocates the target library and the distribution library, by default
#var("NAME")#cmd(".LINKLIB") and #var("NAME")#cmd(".A")#var("xxxx")#cmd("LOD").
It has no step that deletes them, on purpose: after an installation the
distribution library holds what SMP accepted, and scratching it would leave
SMP reporting a level that is no longer there.

*The installation job*, #var("name")#cmd("-")#var("version")#cmd("-inst.jcl"),
does the rest. The SMP steps run in order, and each is skipped when the
step it depends on failed:

#tab(caption: [The steps of the installation job])[
  #table(columns: (0.9in, 1fr),
    [Step], [What it does],
    [#cmd("DELOLD")], [deletes every data set the job receives into, the
      staging library and each text library, since TSO #cmd("RECEIVE") does
      not write into an existing data set.],
    [#cmd("RECV1")...], [receives each uploaded TRANSMIT file: the load
      modules into the staging library, by default
      #var("NAME")#cmd(".")#var("xxxx")#cmd("LOAD"), and each text library
      into its target.],
    [#cmd("RECV")], [receives the SYSMOD into SMP. It is part of the job,
      not a file of its own.],
    [#cmd("APPLYCHK")], [checks the installation without changing
      anything.],
    [#cmd("APPLY")], [copies the modules into the target library.],
    [#cmd("ACCEPT")], [copies them into the distribution library, the level
      a later #cmd("RESTORE") returns to. Left out with
      #cmd("[smp] accept_fmid = false").],
    [#cmd("CLEANUP")], [deletes the staging library.],
  )
] <ug-package-steps>

#idx("SYSMOD")
The SYSMOD is a #cmd("++FUNCTION") with one #cmd("++MOD") per load module:

#fig(caption: [The SYSMOD of sums 1.0.0-dev])[
  #code(read("../ex/ug-package/sysmod.txt"))
] <ug-package-sysmod>

Its #cmd("++JCLIN") is a copy job, not a link-edit: SMP copies each module
as MBT linked it and does not bind it again. So every attribute set at the
link, such as #cmd("ac = 1"), the entry point and the aliases, survives the
installation. Aliases are carried as such on the #cmd("++MOD").

== The Function Identifier <ug-package-fmid>

#idx("FMID")
Every release that SMP installs is a SYSMOD with a function identifier, the
FMID, seven characters: #cmd("T"), three letters for the product, and
three digits for the version. #cmd("[smp] prefix") gives the first four,
and MBT derives the rest from the version:

#tab(caption: [FMIDs derived from the version])[
  #table(columns: (1in, 1.2in, 1fr),
    [Version], [#cmd("fmid")], [#cmd("delete")],
    [1.3.0], [#cmd("TSUM130")], [#cmd("TSUM120")],
    [1.4.0], [#cmd("TSUM140")], [#cmd("TSUM130")],
    [2.0.0], [#cmd("TSUM200")], [must be given],
  )
] <ug-package-fmid-tab>

#deflist(width: 1.2in,
  [*One per minor.*], [The FMID is the release's major and minor version,
    and a #cmd("0"). A patch release is meant to become a PTF against its
    minor's FMID, which MBT does not build yet. Until it does, a patch
    release needs an explicit #cmd("fmid"), such as #cmd("TSUM141") for
    1.4.1, *and an explicit #cmd("delete")*, here #cmd("[\"TSUM140\"]"):
    with an explicit #cmd("fmid"), #cmd("delete") is empty unless given,
    and a SYSMOD that replaces nothing does not own the modules it ships
    (@ug-package-check). MBT refuses to spend the minor's identifier a
    second time.],
  [*Each release replaces its predecessor.*], [#cmd("delete") names the
    FMID the release replaces. With a derived FMID it is the previous
    minor by default. A release x.0.0 has no previous minor to derive, so
    it names it, or writes #cmd("delete = []") for a product's first
    level, as #cmd("sums") does.],
  [*After a patch release, name it.*], [When 1.4.1 was released as
    #cmd("TSUM141"), that SYSMOD owns the modules, and 1.5.0 must delete
    #cmd("TSUM141"), not the derived #cmd("TSUM140"). MBT refuses to
    package 1.5.0 with a derived #cmd("delete") when the repository has a
    tag of a 1.4 patch release, and asks for an explicit one.],
  [*No digit above 9.*], [There is no room for two: at patch 9 the next
    release is a new minor, at minor 9 a new major.],
  [*When it is checked.*], [A missing #cmd("delete") for x.0.0 and a digit
    above 9 make the project file invalid: every command stops, even
    #cmd("mbt build"). A patch release without an explicit #cmd("fmid")
    stops #cmd("mbt package") and #cmd("mbt release") for a final
    version\; for a #cmd("-dev") version #cmd("mbt package") warns, leaves
    out the installation package, and still writes the other files. The
    check for a released patch tag needs the repository's tags, which a
    shallow CI checkout does not have: it takes effect where a release is
    prepared, on the workstation.],
  [*Every FMID once.*], [An identifier that was installed anywhere, even
    on a test system, is spent. Never give it to another release.],
)

#cmd("T") keeps the identifiers out of IBM's, which on MVS 3.8j begin with
#cmd("E"). Before a new prefix is taken into use, check on the systems it
will be installed on that no SYSMOD of that name exists
(@ug-package-check).

== Upgrading an Installation <ug-package-upgrade>

#idx("SMP", "DELETE")
A release whose SYSMOD names its predecessor in #cmd("delete") upgrades an
installation in one run of the job. SMP deletes the predecessor's modules
from the target library, copies the new ones in, and records the new FMID
as their owner. The #cmd("APPLY") then ends with return code 4, with the
message #cmd("HMA2461") that the deleted SYSMOD has no backup: that is
expected, and the job goes on to the #cmd("ACCEPT"), which does the same in
the distribution library.

The predecessor's identifier stays in the SMP inventory as a deleted
SYSMOD: #cmd("LIST") shows it with #cmd("DELBY") naming the release that
replaced it. It remains spent.

Text libraries shipped with #cmd("[distribution.library]") are received
afresh on every installation: changes made to them on the system are lost,
so copy what you change into a library of your own.

#idx("APF")
Two things an upgrade does not do:

- *It does not touch the predecessor's data sets.* SMP records the DD name
  a module was installed through, not the data set behind it. A product
  whose library names changed between two releases leaves the old library
  in place, populated and, if it was, APF-authorized. Changing the
  #cmd("STEPLIB") of the started task and the APF list to the new library,
  and scratching the old one, are then part of the upgrade, not tidying up:
  without them the old program keeps running.
- *It does not change the APF list.* An APF entry names a data set and its
  volume. A library that is on another volume than its entry says is not
  authorized.

#idx("prerequisite")
*A required version of another product* does not belong into the SYSMOD.
SMP's #cmd("REQ") demands that every SYSMOD it names is installed at once,
so it can name only exactly one level of another product and has no way to
say "this level or a later one". Leave #cmd("[smp] prereq") empty, its
default, and say in the README which versions of other products the
release needs.

== Checking an Installation <ug-package-check>

#idx("SMP", "checking")
*Check every installation by the content of the target library, not by the
return codes.* SMP gives a module to the SYSMOD that owns its name. A
SYSMOD that does not own a module it ships cannot install it: SMP marks the
element #cmd("NOT SEL"), copies nothing, and every step still ends with
return code 0. The message that shows a module was really installed is

```
HMA2380 COPY SUCCESSFUL - MOD=SUMS - LMOD=SUMS - LIBRARY=LINKLIB
```

and the check that cannot be fooled is a list of the members of the target
library. After an upgrade of a server, check also that the server that is
running is the new level, for example by the version it reports when it
starts.

#idx("SMP", "LIST")
Whether an FMID is already known to a system is shown by #cmd("LIST"),
with the zone named:

```
//LIST     EXEC SMPAPP
//SMPCNTL  DD  *
 LIST CDS SYSMOD(TSUM130) .
/*
```

Return code 4 and an empty list mean the identifier is free. A list that
shows only #cmd("TYPE = FUNCTION") and #cmd("DELBY") is a deleted
predecessor, which answers with return code 0 and is spent. #cmd("CDS")
lists what is applied, #cmd("ACDS") what is accepted\; check both.

== Changing the Package Alone: mbt dist <ug-package-dist>

#idx("mbt dist")
#cmd("mbt dist") writes the installation package again from the load
modules already in #cmd("dist/"), without building. It is for a change to
the package itself, such as a new README or another data set name.
