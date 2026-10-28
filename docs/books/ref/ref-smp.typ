#import "../bookmaster/bookmaster.typ": *

= The SMP Package <ref-smp>

#idx("SMP", "package")
With #cmd("[distribution]") and #cmd("[smp]") (@ref-toml-smp),
#cmd("mbt package") writes an installation package. #var("n") is the project
name, #var("v") the version, #var("NAME") the name in upper case, #var("P4")
its first four characters.

== Contents <ref-smp-contents>

#var("n")#cmd("-")#var("v")#cmd("-dist.zip") and #cmd("-dist.tar.gz") hold
one directory #var("n")#cmd("-")#var("v")#cmd("/") with:

#table(columns: (2.6in, 1fr),
  [File], [Contents],
  [#cmd("README.md")], [#cmd("[distribution] readme").],
  [#var("n")#cmd("-")#var("v")#cmd("-load.xmit")], [the load modules: a
    TSO TRANSMIT file of a load library.],
  [#var("n")#cmd("-")#var("v")#cmd("-")#var("q")#cmd(".xmit")], [one per
    #cmd("[distribution.library.")#var("dir")#cmd("]"): a TRANSMIT file of a
    text library, #var("q") the last qualifier of its target in lower
    case.],
  [#var("n")#cmd("-")#var("v")#cmd("-alloc.jcl")], [the allocation job.],
  [#var("n")#cmd("-")#var("v")#cmd("-inst.jcl")], [the installation job,
    with the SYSMOD inline.],
  [#cmd("extra") files], [under their own names.],
)

== Data Sets <ref-smp-datasets>

#table(columns: (1.4in, 1.9in, 1fr),
  [Data set], [Default], [Role],
  [staging], [#var("NAME")#cmd(".")#var("P4")#cmd("LOAD") (#cmd("lklib"))],
    [the load modules are received into it\; the #cmd("LKLIB") of the
    #cmd("++MOD")s. Deleted at the end.],
  [target], [#var("NAME")#cmd(".LINKLIB") (#cmd("target"))], [the
    installed modules.],
  [distribution], [#var("NAME")#cmd(".A")#var("P4")#cmd("LOD")
    (#cmd("distlib"))], [the accepted modules.],
  [text library], [#var("NAME")#cmd(".")#var("DIR")], [received with TSO
    #cmd("RECEIVE"), outside SMP.],
)

The DD name of each data set in the jobs and the SYSMOD is its last
qualifier, so no two of them may share one, and none may be
#cmd("SMPTLIB"). Data set names are at most 44 characters.
#cmd("@VRM@") in a name is replaced by the version qualifier.

*Text libraries* are made from the regular files directly in #var("DIR"),
not from subdirectories or files whose name begins with a period. Each file
is a member and needs a valid member name\; an empty directory is an
error. In their text, #cmd("@LINKLIB@"), #cmd("@VRM@"), #cmd("@VERSION@"),
#cmd("@FMID@") and #cmd("@")#var("qualifier")#cmd("@") (the last qualifier
of each library's target) are replaced by the values of the release.

The allocation job allocates the target and distribution libraries,
#cmd("RECFM=U"), #cmd("BLKSIZE=15040"), in cylinders, and never deletes
them.

== The Installation Job <ref-smp-job>

#table(columns: (1.1in, 1fr),
  [Step], [Action],
  [#cmd("DELOLD")], [IDCAMS: deletes the staging library and every text
    library\; #cmd("SET MAXCC=0").],
  [#cmd("RECV")#var("n")], [IKJEFT01: #cmd("RECEIVE INDSN(...)
    DATASET(...)") for each TRANSMIT file. The names to edit are marked
    #cmd("CHANGE.ME"). Each #cmd("RECEIVE") also carries a commented
    #cmd("VOLUME('CHANGE.ME')") line: harmless as shipped (TK5), to be
    uncommented and given a volume where #cmd("RECEIVE") needs one (usually
    MVS/CE, #cmd("PUB000") on a stock system).],
  [#cmd("RECV")], [SMP #cmd("RESETRC") and #cmd("RECEIVE SELECT(")#var("fmid")#cmd(")")
    of the SYSMOD, inline in the job (#cmd("DD DATA,DLM=@@")), if the last
    #cmd("RECV")#var("n") ended with 0.],
  [#cmd("APPLYCHK")], [#cmd("APPLY CHECK"), if #cmd("RECV") ended with 0.],
  [#cmd("APPLY")], [#cmd("APPLY"), only after a clean check.],
  [#cmd("ACCEPT")], [#cmd("ACCEPT"), unless #cmd("accept_fmid = false").
    With #cmd("delete"), it also runs after an #cmd("APPLY") that ended with
    4.],
  [#cmd("CLEANUP")], [IDCAMS: deletes the staging library, if
    #cmd("ACCEPT") (or, without it, #cmd("APPLY")) ended with 0.],
)

== The SYSMOD <ref-smp-sysmod>

For a release 1.2.0 of a product with two modules, one with aliases,
that requires #cmd("TXYZ100") and replaces #cmd("TABC110"):

```
++FUNCTION(TABC120) .
++VER(Z038) REQ(TXYZ100) DELETE(TABC110)
   /* prod 1.2.0                                                 */
   /* Load modules are copied from the LKLIB, not re-bound.      */ .
++JCLIN .
//TABC120 JOB 1,'PROD JCLIN',MSGLEVEL=1,CLASS=A
//COPYLOAD EXEC PGM=IEBCOPY
//AABCLOD  DD  DISP=SHR,DSN=ABC.AABCLOD
//LINKLIB  DD  DISP=SHR,DSN=ABC.LINKLIB
//SYSIN    DD  *
  COPY INDD=AABCLOD,OUTDD=LINKLIB
  SELECT MEMBER=(...)
/*
++MOD(ABCSRV) LKLIB(LKLIB) DISTLIB(AABCLOD)
      TALIAS(ABC,ABCX) .
++MOD(ABCUTIL1) LKLIB(LKLIB) DISTLIB(AABCLOD) .
```

#cmd("REQ") and #cmd("DELETE") appear only when #cmd("prereq") and
#cmd("delete") are not empty, the identifiers separated by commas. The
first comment card names the product and the version. #cmd("LKLIB"),
#cmd("DISTLIB"), #cmd("INDD") and #cmd("OUTDD") are DD names, the last
qualifiers of the data sets. The modules follow in name order. A
#cmd("TALIAS") card that would reach beyond column 71 is an error.

The #cmd("++JCLIN") is a copy, so SMP copies each module as it was linked
and keeps its attributes, entry point and authorization code. Aliases
travel as #cmd("TALIAS").


== Prerelease Builds <ref-smp-prerelease>

#idx("prerelease", "SMP package")#idx("FMID", "throwaway")
A version with #cmd("-dev") or #cmd("-rc")#var("n") gets no derived FMID.
The FMID derived from #cmd("1.4.0-dev") is the one of the release 1.4.0, and
an installation spends an identifier for good (_MBT User's Guide_, "The Function Identifier"). An
installation of the prerelease build would therefore:

- *spend the release's identifier*, so that the release itself can no longer
  be installed under it on that system\;
- *take the modules*: SMP gives a module to the SYSMOD that owns its name, so
  a later patch release of the previous minor, shipped under an identifier of
  its own, installs nothing. Its elements are #cmd("NOT SEL"), and every step
  still ends with return code 0 (_MBT User's Guide_, "Checking an Installation").

#cmd("mbt package") and #cmd("mbt dist") refuse such a version, with return
code 2, unless #cmd("[smp] fmid") is set:

```
[mbt] ERROR: version 1.0.0-dev is a prerelease: the derived FMID TSUM100 is the release's, and an install of this build would spend it and block the release and patch releases of the previous minor (NOT SEL) -- set [smp] fmid and delete explicitly, to a throwaway id (Reference, SMP chapter)
```

#cmd("mbt package") builds and writes the load TRANSMIT file and the library
archive before it refuses\; the installation package is not written. A
project without #cmd("[distribution]") is not affected.

*A throwaway installation.* To get an installable package from a
prerelease, set both keys:

```toml
[smp]
fmid   = "TTST100"
delete = []
```

#deflist(width: 1.2in,
  [*The identifier*], [seven characters, a letter and six letters or
    digits\; MBT checks nothing more. It is not checked against the
    derived one either: #cmd("TSUM100") for #cmd("1.0.0-dev") is accepted and
    has all the consequences above. Take one that no release will carry,
    for example under a prefix that no product uses. Once installed anywhere,
    it is spent like any other.],
  [*delete*], [is required with an explicit #cmd("fmid"). #cmd("[]") means
    the build replaces nothing, which is right on a system where the product
    is not installed. A throwaway SYSMOD that replaces nothing does not own
    the modules the product already has: on a system that carries the
    product, its elements come back #cmd("NOT SEL") with return code 0. Give
    the modules of the throwaway build names the product does not use, or
    install on a system without the product. Naming the product's
    identifier in #cmd("delete") instead deletes the installed level's
    modules.],
  [*Checking*], [the target library's member list shows whether anything
    was installed, not the return codes (_MBT User's Guide_, "Checking an Installation").],
)

MBT does not notice a throwaway identifier at a release. The version
#cmd("1.4.0") with an explicit #cmd("fmid") is packaged under that
#cmd("fmid"), without a warning. Remove #cmd("fmid") from #cmd("[smp]")
again, put #cmd("prefix") back, and set #cmd("delete") as
@ref-smp-replace describes, before #cmd("mbt release")
(_MBT User's Guide_, "Releasing").

#cmd("mbt prerelease") tags the commit and does not package\; the release
workflow does, on the tag (_MBT User's Guide_, "The Workflows"). For a product with
#cmd("[distribution]") and a derived FMID, the #cmd("Package") step of
#cmd("release3.yml") fails with the message above and nothing is published,
until the project sets a throwaway #cmd("fmid") and #cmd("delete").


== Replacing a Throwaway Installation <ref-smp-replace>

#idx("FMID", "throwaway, replacing")#idx("DELETE", "throwaway")
The release replaces a throwaway installation the way it replaces its
predecessor: it names the throwaway identifier in #cmd("delete"). MBT derives
only the identifier of the previous minor. A #cmd("delete") that is set takes
the place of the derived one, it does not add to it. After a throwaway
#cmd("TTST1D0") was installed, the release 1.1.0 therefore names both:

```toml
[smp]
prefix = "TSUM"
delete = ["TSUM100", "TTST1D0"]
```

For a release #var("x")#cmd(".0.0") there is no derived #cmd("delete") at all\;
set it to the throwaway identifier next to whatever the release replaces,
or to #cmd("[]") for a first level that no throwaway build was installed
for. Take #cmd("fmid") out and put #cmd("prefix") back, as above.

The following was observed on a scratch MVS/CE system with SMP4, with small
C modules that SMP copies from a #cmd("++JCLIN"), as MBT writes them
(@ref-smp-sysmod). It was not observed on TK5, nor with other element types
such as macros, source or modules shared by several load modules.

#deflist(width: 1.4in,
  [*The throwaway is named*], [The release is received, applied and
    accepted. It becomes the owner of the modules in the target zone and in
    the distribution zone. #cmd("LIST") shows the throwaway identifier and
    the previous minor as deleted, with #cmd("DELBY") naming the release.
    The target and the distribution library hold exactly the modules of the
    release, and the modules report the release's version when they run.
    The #cmd("APPLY") ends with return code 4 and #cmd("HMA2461") for every
    identifier in #cmd("delete") that is installed, as in an upgrade
    (_MBT User's Guide_, "Upgrading an Installation").],
  [*The throwaway has modules of its own names*], [They are deleted from both
    libraries, with #cmd("HMA2240") for each, and the release installs its
    own. A throwaway build whose modules carry names the product does not
    use leaves nothing behind once it is named in #cmd("delete").],
  [*The throwaway was never installed*], [Naming it costs nothing. SMP
    gives no message and no return code for it. It does record the
    identifier as deleted by the release, in both zones, so it is spent.
    List every throwaway identifier that may be installed on any system the
    release goes to.],
  [*Several throwaway levels*], [A second throwaway build can name the first in
    its own #cmd("delete"), and the release can name the whole chain. The
    release then owns the modules, all identifiers are recorded as deleted,
    and the libraries hold only the release's modules. Installing the same
    throwaway identifier again, while it is installed, ends with return
    code 0.],
)

*The throwaway is not named.* Two things can happen. Neither is an error for
SMP:

- *The release has the module names of the throwaway build.* #cmd("RECEIVE"),
  #cmd("APPLY") and #cmd("ACCEPT") all end with return code 0, and no
  #cmd("HMA2380") message appears for any module. The elements of the
  release come back #cmd("NOT SEL") against the release's own SYSMOD. The
  release is recorded as applied and accepted, but it owns no module. The
  throwaway build stays the owner, and it is what runs.
- *The release has other module names.* It installs, and the throwaway build
  stays installed beside it, with its modules and its identifier not
  deleted.

#note[In the element summary of a correct upgrade, a module the release
applied also shows a line #cmd("NOT SEL") that ends in #cmd("DELETED"): that
is the deleted SYSMOD, which SMP does not select, and it is normal. The
failure is a module that is #cmd("NOT SEL") against the *new* SYSMOD, with no
#cmd("HMA2380").]

*Checking.* Do not rely on the return code to tell these cases from a
replacement. What does (_MBT User's Guide_, "Checking an Installation"):

+ #cmd("HMA2380 COPY SUCCESSFUL") for each module and each library, with the
  release's identifier as #cmd("SYSMOD"), in the #cmd("APPLY") and in the
  #cmd("ACCEPT")\;
+ #cmd("LIST") of the release and of the throwaway identifier in both zones:
  the release applied and accepted, the other with #cmd("DELBY") naming the
  release\;
+ #cmd("LIST") of the modules, with the release as the last updater\;
+ the member lists of the target and the distribution library.

*A library of its own.* A #cmd("DELETE") acts on the library that the DD name
of the deleting job resolves to. A throwaway installed into a target library
of its own, set with #cmd("[smp] target"), is not cleaned by the release,
whose job points at the product's library. Its modules stay and run from that
library, and a library that comes earlier in the STEPLIB than the product's
hides the release's modules. Take such a library out of the STEPLIB, or delete
it, after the release is installed. For the same reason a throwaway build
that names the product's identifier in #cmd("delete") deletes the modules of
the installed level only in its own target library.
