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
    #cmd("target"), by default #var("NAME")#cmd(".")#var("DIR"). With
    #cmd("element = \"MAC\"") or #cmd("\"SRC\"") SMP installs it
    instead, one element per member (@ug-package-elements).],
)

#cmd("[smp]") describes the SMP side: above all the function identifier,
and the data sets the modules pass through. The #cmd("fmid") of
@ug-package-toml is set by hand because #cmd("sums") is at
#cmd("1.0.0-dev"), a prerelease: MBT gives a prerelease no identifier of
its own (@ug-package-prerelease).

#idx("DD name")
*DD names.* The line #cmd("WARNING") in @ug-package-run is about the DD name
of the target library. SMP addresses each library by a DD name and stores it
with every element it installs, and MBT uses the last qualifier of the data
set name. The default target #var("NAME")#cmd(".LINKLIB") is therefore known
as #cmd("LINKLIB"), the name of #cmd("SYS1.LINKLIB") in the SMP procedures.
The installation works, but a later run of the standard procedures resolves
that name to the system library. Give each library a DD name of its own with
#cmd("target_dd"), #cmd("distlib_dd") and #cmd("lklib_dd") in #cmd("[smp]"),
and with #cmd("target_dd") and #cmd("distlib_dd") in a library with
#cmd("element"). Set them before the first installation: the elements SMP has
already recorded stay under the old name (_MBT Reference_, "DD Names").

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

The job puts every library on a volume you name. Its two symbolic parameters,
#cmd("TGTVOL") for the target libraries and #cmd("DLBVOL") for the
distribution libraries, are set on the #cmd("PROC") statement at the top of
the job, and default to the volumes of an MVS/CE system, #cmd("MVSRES") and
#cmd("SMP000"). *On TK5, set #cmd("TGTVOL=TK5RES") and
#cmd("DLBVOL=TK5DLB").* The defaults can be changed for every package of a
project with #cmd("target_volume") and #cmd("dlib_volume") in
#cmd("[smp]"). A volume the system does not have does not make the job fail:
it waits, and the console shows #cmd("IEF238D") and asks for a device name
or #cmd("CANCEL"). Reply #cmd("CANCEL"), correct the volume, and submit the
job again (_MBT Reference_, "The Volumes of the Allocation Job").

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
    [#cmd("APPLY")], [copies the modules into the target library, and
      the elements of an element library into theirs.],
    [#cmd("ACCEPT")], [copies them into the distribution library, the level
      a later #cmd("RESTORE") returns to. Left out with
      #cmd("[smp] accept_fmid = false").],
    [#cmd("CLEANUP")], [deletes the staging library, and those of the
      element libraries.],
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

== Libraries Installed by SMP <ug-package-elements>

#idx("element library")
A library of members that SMP should know, such as REXX functions and
their sources, goes into the package as #cmd("[distribution.library.")#var("dir")#cmd("]")
with #cmd("element = \"MAC\"") (or #cmd("\"SRC\"") for sources). Every
file of the directory becomes a #cmd("++MAC") (or #cmd("++SRC")) of the
SYSMOD, and the installation job reads each from the staging library of
its library through a #cmd("TXLIB") card of its own, in four steps. It
then has about as many DD cards per step as the library has members\: 235
for libraries of 114 and 121 members ran on MVS/CE and TK5.

The members are fixed 80, and nothing in the 80 columns is changed.
#cmd("mbt package") refuses a file with a line past column 80, with a
control character or a character that EBCDIC (CP037) has no equivalent for,
and names the file, the line and the column. Sequence numbers in
columns 73 to 80 are carried as they are.

#note[Before the first installation run the #cmd("LIST CDS") statements
#cmd("mbt package") prints on the target system. A name SMP already has as
the same type of element is silently not installed (#cmd("NOT SEL"), return
code 0). A #cmd("SRC") with the name of a load module makes SMP assemble the
member into that module, or end the #cmd("APPLY") at return code 12.]

A later version that leaves a member out removes it from the target library
and the distribution library when it installs, as for a load module.
The Reference has the rules in full (_MBT Reference_, "Libraries as SMP Elements").

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
    a SYSMOD that replaces nothing does not own the modules it ships
    (@ug-package-check). MBT cannot tell a patch from a first level, so an
    explicit #cmd("fmid") without #cmd("delete") is an error\; a product's
    first level writes #cmd("delete = []"). MBT refuses to spend the
    minor's identifier a second time.],
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
  [*When it is checked.*], [A missing #cmd("delete") for x.0.0 or beside
    an explicit #cmd("fmid"), and a digit above 9, make the project file
    invalid: every command stops, even
    #cmd("mbt build"). A patch release without an explicit #cmd("fmid")
    stops #cmd("mbt package") and #cmd("mbt release") for a final
    version\; a prerelease without an explicit #cmd("fmid") stops
    #cmd("mbt package") and #cmd("mbt dist") too (@ug-package-prerelease).
    The check for a released patch tag needs the repository's tags, which a
    shallow CI checkout does not have: it takes effect where a release is
    prepared, on the workstation.],
  [*Every FMID once.*], [An identifier that was installed anywhere, even
    on a test system, is spent. Never give it to another release.],
)

#cmd("T") keeps the identifiers out of IBM's, which on MVS 3.8j begin with
#cmd("E"). Before a new prefix is taken into use, check on the systems it
will be installed on that no SYSMOD of that name exists
(@ug-package-check).

== Prereleases and Throwaway Identifiers <ug-package-prerelease>

#idx("prerelease", "SMP package")#idx("FMID", "throwaway")
A prerelease has no identifier of its own to derive. The identifier derived
from #cmd("1.4.0-dev") is #cmd("TSUM140"), that of the release 1.4.0.
Installing the prerelease build would spend it, and the release could no
longer be installed under it on that system. The build would also own the
modules, so that a patch release of the previous minor, shipped under an
identifier of its own, installs nothing, with return code 0
(@ug-package-check). So #cmd("mbt package") refuses a prerelease whose
#cmd("[smp]") gives no #cmd("fmid"). With the project of @ug-package-toml
and #cmd("prefix = \"TSUM\"") in place of the #cmd("fmid"):

#fig(caption: [A prerelease without a throwaway identifier])[
  #screen(raw(read("../ex/ug-package/prerelease.txt")))
] <ug-package-prerelease-run>

The load modules and the library archive are written, the installation
package is not, and the return code is 2. #cmd("mbt dist") refuses the same
way. A project with #cmd("[distribution]") that has no #cmd("[smp]")
#cmd("fmid") must therefore set one before its first prerelease can be
packaged: a *throwaway identifier*, one that no release will carry, and a
#cmd("delete"), which an explicit #cmd("fmid") always needs. @ug-package-toml
shows both, with #cmd("delete = []") because the build replaces nothing.

#deflist(width: 1.2in,
  [*Any seven characters.*], [MBT checks that the identifier is a letter
    and six letters or digits, and no more. It does not check that it is
    not the release's: #cmd("fmid = \"TSUM100\"") on #cmd("1.0.0-dev") is
    accepted, and has the consequences above. Choose one that no release
    will carry, for example under a prefix that no product uses.],
  [*Spent once installed.*], [A throwaway identifier that was installed
    anywhere is spent like any other (@ug-package-fmid).],
  [*On a system that has the product.*], [A SYSMOD that replaces nothing does
    not own the modules the product already has, so its elements come back
    #cmd("NOT SEL") with return code 0. Give the modules of the throwaway
    build names the product does not use, or install on a system without
    the product. Naming the product's identifier in #cmd("delete") would
    delete the installed level's modules.],
  [*Before the release.*], [MBT does not notice a throwaway identifier at a
    release: #cmd("1.0.0") with an explicit #cmd("fmid") is packaged under
    it, without a warning. Remove #cmd("fmid") from #cmd("[smp]"), put the
    #cmd("prefix") back, and name the throwaway identifier in #cmd("delete")
    next to the previous minor's, before #cmd("mbt release") (@ug-release).
    A #cmd("delete") that is set replaces the derived one. Without the
    throwaway identifier in it, a system that carries the throwaway build
    gets a release that installs nothing, or that stands beside the build,
    and no step fails\; the Reference describes both (_MBT Reference_,
    "Replacing a Throwaway Installation").],
)

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
afresh on every installation, by design: a shipped library belongs to the
product and is replaced with it. Changes made to it on the system are lost,
so change a copy, in a library of your own.

#idx("predecessor")
*A release that gives a library another DD name* needs one more thing from the
job. SMP finds the predecessor's elements by the DD name it recorded, so the
job must define that name for the old data set. Without it the upgrade
fails (#cmd("HMA2832"), return code 12), or, if the old name is #cmd("LINKLIB"),
it silently deletes from #cmd("SYS1.LINKLIB") and leaves the old library
populated. List the old libraries in the one release that moves them. The
new level installs into a library of its own name, the old one stays where
it is:

```
[smp]
target    = "ABC.ABCLINK"
target_dd = "ABCLINK"

[smp.predecessor]
LINKLIB = "ABC.LINKLIB"
```

MBT adds the cards to the installation job (_MBT Reference_, "Moving a
Library to Another DD Name"). The release after that removes the table.
Keeping the data set name and changing only the DD name works as well, and
the line for the old DD name is required there too: the delete of the
predecessor runs before the new modules are copied, so the new level's
modules stay and the ones it no longer has are deleted.

With a library of its own name, the new library does not exist yet. The
allocation job of the release would
also allocate the libraries that do, and fails on them. Submit a job of your
own instead, with one #cmd("IEFBR14") step and the #cmd("DD") statement of the
new library taken from the allocation job, its volume written out. If you cut
the allocation job down instead, remove the symbols on its #cmd("PROC")
statement that no remaining #cmd("DD") uses (#cmd("TGTVOL") for target
libraries, #cmd("DLBVOL") for distribution libraries): MVS 3.8j ends the job
with a JCL error (#cmd("IEF452I"), #cmd("IEF657I")) otherwise.

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
