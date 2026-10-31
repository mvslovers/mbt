#import "../bookmaster/bookmaster.typ": *

= mbt.toml <ref-toml>

#idx("mbt.toml")
#cmd("mbt.toml") describes a project, in TOML, schema 3. This chapter lists
every table and key, with its default. #var("NAME") below is the project
name in upper case.

A key not listed here is an error that names it, with return code 2, in
every table except #cmd("[dependencies]") and #cmd("[plugins]"), whose keys
are repository names, and in #cmd("[tools]") the keys of an entry are
checked the same way. So is a table in a form other than the one shown
here: #cmd("[[distribution.library]]") where
#cmd("[distribution.library.DIR]") is meant, #cmd("[[build]]") or
#cmd("[[smp]]") for a table of its own, a key that is a string where a table
belongs, a #cmd("[dependencies]") entry that is no version range. The
message names the key and the form to write. Every command that reads
#cmd("mbt.toml") stops with it, #cmd("mbt build") and #cmd("mbt deps")
included; nothing is left out of a package without a word. The reserved
tables of @ref-toml-reserved are errors too. The types of the other
values are not checked: give them as shown. A directory that holds both
#cmd("mbt.toml") and an MBT 2 #cmd("project.toml") is an error, and so are
the MBT 2 forms #cmd("[[module]]") and #cmd("[[test]]").


== Top Level <ref-toml-top>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("schema")], [required], [the number #cmd("3"), not the string.],
)

== [project] <ref-toml-project>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("name")], [required], [the project's name.],
  [#cmd("version")], [required], [#var("major")#cmd(".")#var("minor")#cmd(".")#var("patch"),
    optionally #cmd("-dev") or #cmd("-rc")#var("n"). No number above 9 in a
    project whose FMID is derived.],
  [#cmd("kind")], [#cmd("\"application\"")], [#cmd("application"),
    #cmd("library") or #cmd("module"), not checked. Only #cmd("library")
    changes anything: #cmd("mbt build") then builds the library.],
)

== [toolchain] <ref-toml-toolchain>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("mbt")], [none], [the MBT that runs the project: #cmd("\"3.0\"")
    for any 3.0._x_, #cmd("\"3.0.2\"") or #cmd("\"3.0.2-dev\"") for that
    one (@ref-cmd-launcher).],
  [#cmd("cc370")], [none], [the CC/370 a release is built with: a version,
    which means its tag, or any Git reference. Only #cmd("mbt doctor")
    compares it with the installed one, and warns.],
  [#cmd("libc370")], [none], [the LIBC/370 a release is built with, a
    version or a Git reference. A version is also the oldest LIBC/370 a
    build accepts. Since the C start-up must be in #cmd("libc.a"), name
    2.3.0 or later.],
)

== [dependencies] <ref-toml-deps>

One key per dependency: #cmd("\"")#var("owner")#cmd("/")#var("repo")#cmd("\" =
\"")#var("range")#cmd("\""). A range is one or more conditions joined by
commas:

#table(columns: (1.3in, 1fr),
  [Condition], [Allows],
  [#cmd(">=")#var("v")], [#var("v") and later.],
  [#cmd("<")#var("v")], [earlier than #var("v").],
  [#cmd("=")#var("v")], [exactly #var("v").],
  [#cmd("^")#var("v")], [#var("v") and later, below the next major
    version, or for #cmd("0.")#var("x") below the next minor. #var("v")
    may have one, two or three numbers\; #cmd("^0") means below 1.0.0.],
)

Versions are ordered by their numbers, then #cmd("-dev") \<
#cmd("-rc")#var("n") \< the release. A prerelease is chosen only for a
range that names one.

== [build] <ref-toml-build>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("include")], [#cmd("[]")], [directories for #cmd("-I"), after
    #cmd("include/"), which is on the path when the project has one.],
  [#cmd("cflags")], [#cmd("[]")], [options for #cmd("cc370"), after
    MBT's #cmd("-O1 -std=gnu99 -Wall -Wextra -Werror").],
  [#cmd("asflags")], [#cmd("[]")], [options for #cmd("as370").],
  [#cmd("max_rc")], [#cmd("4")], [the highest return code of
    #cmd("as370") that still passes, an integer from 0 to 16. A higher one
    fails the step and removes its object module. An assembly that passes
    with a return code above 0 is shown as a warning with
    #cmd("as370")'s messages, and the build closes with their count.
    #cmd("0") fails any warning.],
  [#cmd("cobflags")], [#cmd("[]")], [options for #cmd("cobc370"), for every
    COBOL source: #cmd("-I") for copybook directories, #cmd("-s"). The
    #cmd("include/") of every dependency follows, so #cmd("COPY") finds the
    copybooks a dependency ships.],
)

A source ending in #cmd(".cbl") or #cmd(".cob") is COBOL. #cmd("cobc370")
translates it into #cmd("build/cobol/")#var("stem")#cmd(".asm") with
#cmd("-MMD"), so a changed copybook translates it again, and #cmd("as370")
assembles that into #cmd("build/")#var("stem")#cmd(".o"). #cmd("cobc370") is
found in #cmd("MBT_COBC370"), else a #cmd("[tools]") entry named
#cmd("cobc370"), else on #cmd("PATH"). A unit with COBOL sources needs
#cmd("entry"), the PROGRAM-ID that starts it, unless one of its C sources
defines #cmd("main()") at the start of a line; then the default
#cmd("@@CRT0") stays. It cannot be
#cmd("rent = true"): #cmd("cobc370")'s code keeps WORKING-STORAGE in the
module.

The compile line is MBT's options, the #cmd("-I") options, #cmd("cflags"),
then #cmd("-I") for the dependencies, MBT's headers and #cmd(".mbt").

=== [build.host] <ref-toml-host>

For #cmd("mbt test") on the workstation only:

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("cc")], [#cmd("CC"), else #cmd("cc")], [the C compiler of the
    workstation.],
  [#cmd("cflags")], [#cmd("[]")], [options after #cmd("[build] cflags").],
  [#cmd("sources")], [#cmd("[]")], [further sources linked into every test.],
  [#cmd("replace")], [#cmd("{}")], [#cmd("{ \"")#var("source")#cmd("\" =
    \"")#var("other")#cmd("\" }"): use #var("other") instead of
    #var("source").],
)

== [lib] <ref-toml-lib>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("name")], [the project name], [the library:
    #cmd("build/")#var("name")#cmd(".a").],
  [#cmd("sources")], [none], [its sources. Patterns such as
    #cmd("\"src/*.c\"") are allowed here and wherever sources are listed\;
    a pattern that matches nothing is a warning. Without sources, only the
    headers are published.],
  [#cmd("headers")], [#cmd("[]")], [the headers published with it.],
)

== [internal] <ref-toml-internal>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("sources")], [none], [sources of a private archive every
    module and test links with.],
  [#cmd("exclude")], [#cmd("[]")], [files the patterns match that are left
    out.],
)

== [module.NAME] <ref-toml-module>

One table per load module. #var("NAME") is the member name in upper case,
one to eight characters, #cmd("A")-#cmd("Z"), #cmd("0")-#cmd("9"),
#cmd("@"), #cmd("#"), #cmd("$"), not starting with a digit\; quoted when it
holds #cmd("#"), #cmd("@") or #cmd("$"). Aliases follow the same rule and
must be unique across all modules and aliases\; a test has none.

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("sources")], [required], [the module's own sources.],
  [#cmd("exclude")], [#cmd("[]")], [files the patterns match that are left
    out.],
  [#cmd("rent")], [required], [reentrant.],
  [#cmd("reus")], [required], [serially reusable.],
  [#cmd("refr")], [#cmd("false")], [refreshable.],
  [#cmd("entry")], [#cmd("\"@@CRT0\"")], [the entry point.],
  [#cmd("startup")], [a C program], [#cmd("false"): no C run time\;
    #cmd("\"crtm\""): a C module run by another C program\; it needs
    #cmd("crtm.o"), which newer LIBC/370 releases no longer ship.],
  [#cmd("dep_startup")], [none], [#cmd("true"): #cmd("@@START") comes from
    a dependency\; #cmd("false"): from LIBC/370.],
  [#cmd("ac")], [#cmd("0")], [the authorization code.],
  [#cmd("aliases")], [#cmd("[]")], [further names of the module.],
)

== Tests <ref-toml-tests>

Every #cmd("test/**/*.c") and #cmd("test/**/*.asm") is a test, named after
its file in upper case, with that file as its only source, unless it is in
#cmd("[tests] exclude") or the #cmd("sources") of a
#cmd("[test.")#var("NAME")#cmd("]").

=== [tests] <ref-toml-tests-defaults>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("rent"), #cmd("reus")], [required once there is a test], [for
    every test without its own.],
  [#cmd("refr")], [#cmd("false")], [as for a module.],
  [#cmd("exclude")], [#cmd("[]")], [files under #cmd("test/") that are not
    tests.],
)

=== [test.NAME] <ref-toml-test>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("sources")], [the found file], [the test's sources.],
  [#cmd("host")], [#cmd("true")], [#cmd("false"): not on the workstation.],
  [#cmd("mvs")], [#cmd("true")], [#cmd("false"): not on MVS.],
  [#cmd("parm")], [none], [the program argument: a string for both runs,
    or #cmd("{ batch = ..., tso = ... }"). Without it the test gets none\;
    one that then skips its work passes with no assertions.],
  [#cmd("fixtures")], [none], [#cmd("{ ")#var("DD")#cmd(" = [")#var("files")#cmd("] }"):
    members loaded into a data set per DD, #var("HLQ")#cmd(".")#var("PROJECT")#cmd(".FIX.")#var("TEST")#cmd(".")#var("DD"),
    FB 80. A member is the file's name without its extension, in upper
    case\; its lines are the records, columns 73 to 80 included, nothing is
    numbered. A line longer than 80 columns is an error (return code 2).
    Lines starting #cmd("//") or #cmd("/*"), JCL included, arrive as data.],
)

The keys of a load module, #cmd("exclude"), #cmd("entry"),
#cmd("startup"), #cmd("dep_startup"), #cmd("ac"), #cmd("rent"),
#cmd("reus") and #cmd("refr"), are allowed too. A test that must run
APF-authorized needs #cmd("ac = 1") as well as APF-authorized libraries:
without it #cmd("__isauth()") returns 0, even from a library on the APF
list.

=== [tso.NAME] <ref-toml-tso>

The data sets the interactive test #cmd("test/tso/")#var("name")#cmd(".lua")
needs (@ref-tso-fixtures), and its screen size. #var("NAME") is the file's
name, in any case\; a table that names no such file is an error.

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("fixtures")], [none], [#cmd("{ ")#var("KEY")#cmd(" = [")#var("files")#cmd("] }"):
    a data set per #var("KEY"), the last qualifier of its name, loaded as
    the fixtures of #cmd("[test.NAME]") are.],
  [#cmd("size")], [the target's], [the terminal for this test,
    #var("COLS")#cmd("x")#var("ROWS") such as #cmd("\"80x43\"")
    (@ref-tso-size). It wins over #cmd("size") in the target's
    #cmd("[tn3270]").],
)

== [deploy] <ref-toml-deploy>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("target")], [#var("NAME")#cmd(".DEV.LINKLIB")], [the development
    library.],
  [#cmd("test_target")], [#var("HLQ")#cmd(".")#var("NAME")#cmd(".")#var("VRM")#cmd(".TESTLIB")],
    [the test library. The name is cut to eight characters.],
)

#cmd("target") is derived only for a project with load modules. A default
#cmd("target") that would not be a valid data set name, because the
project name is longer than eight characters, is refused, with the key to
set. The same holds for the defaults of #cmd("[smp]") and
#cmd("[distribution.library]").

== [distribution] <ref-toml-distribution>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("readme")], [none], [shipped as #cmd("README.md"). The file must
    exist.],
  [#cmd("extra")], [#cmd("[]")], [further files shipped under their base
    names\; a missing file or a directory is an error.],
)

=== [distribution.library.DIR] <ref-toml-distlib>

A text library made from the directory #var("DIR") and received with TSO
#cmd("RECEIVE").

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("target")], [#var("NAME")#cmd(".")#var("DIR"), the last part of
    #var("DIR") in upper case], [the library it is received into.],
  [#cmd("element")], [none], [#cmd("\"MAC\"") or #cmd("\"SRC\"")\: SMP
    installs the library member by member, every member an element of that
    type, instead of the library being received as a plain data set
    (@ref-smp-elements).],
  [#cmd("distlib")], [#cmd("A") before the last qualifier of
    #cmd("target")], [the distribution library SMP keeps the elements in.
    Only with #cmd("element").],
  [#cmd("stage")], [#cmd("T") before the last qualifier of
    #cmd("target")], [the library the shipped members are received into
    and SMP reads them from. Only with #cmd("element").],
)

With #cmd("element") a member is a file of the directory, the member name its
base name in upper case. The records are fixed 80 (#cmd("FB"),
#cmd("LRECL=80"), #cmd("BLKSIZE=3120")); there is no key for another format.
All 80 columns reach the library as they are -- columns 73 to 80 may hold
sequence numbers, and mbt neither renumbers nor strips them.
#cmd("mbt package") refuses a member with a line past column 80 (tabs are
expanded to every eighth column and trailing blanks do not count, as
#cmd("xmit370") does), a control character, or a character without a CP037
equivalent, and names the file, line and column. A file is read as UTF-8
when it is valid UTF-8, else as Latin-1.

An element name is one for the whole SYSMOD: the same member name in two
libraries of the same #cmd("element") type is an error, the same name as
#cmd("MAC") in one library and #cmd("SRC") in another is not. Names, targets,
#cmd("distlib") and #cmd("stage") must differ from each other and from the
libraries of the load modules.

== [smp] <ref-toml-smp>

Needed with #cmd("[distribution]"), and only with it\; a
#cmd("[distribution]") needs at least one load module. #var("P4") is the
first four characters of #var("NAME").

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("prefix")], [required unless #cmd("fmid")], [#cmd("T") and three
    letters, by convention\; not checked.],
  [#cmd("fmid")], [#var("prefix") + major + minor + #cmd("0")], [the FMID,
    seven characters, a letter and six letters or digits. Required for a
    version whose patch number is not 0, and for a prerelease
    (@ref-smp-prerelease).],
  [#cmd("delete")], [with a derived #cmd("fmid"): #var("prefix") + major +
    (minor − 1) + #cmd("0")\; with an explicit one: none], [the FMIDs this
    release replaces. Required with an explicit #cmd("fmid"), and with a
    derived one for #var("x")#cmd(".0.0")\; #cmd("[]") for a first level.
    It must not name the release's own FMID.],
  [#cmd("system")], [#cmd("\"Z038\"")], [the SREL of the #cmd("++VER").],
  [#cmd("prereq")], [#cmd("[]")], [SYSMODs that must be installed.],
  [#cmd("accept_fmid")], [#cmd("true")], [accept the FMID in the
    installation job.],
  [#cmd("procedure")], [none], [#cmd("\"mvsce\"") or #cmd("\"tk5\""): the
    system the package installs on. The two #cmd("SMPAPP") procedures list
    different DDs (MVS/CE has #cmd("ASAMPLIB"), TK5 has not), and the install
    job must name an overridden DD in the procedure's order. Needed only if
    the order is not the same for both, and then mbt refuses to guess.],
  [#cmd("lklib")], [#var("NAME")#cmd(".")#var("P4")#cmd("LOAD")], [the
    staging library the load modules are received into. Not the same as
    #cmd("target").],
  [#cmd("target")], [#var("NAME")#cmd(".LINKLIB")], [the target library.],
  [#cmd("distlib")], [#var("NAME")#cmd(".A")#var("P4")#cmd("LOD")], [the
    distribution library.],
)

For a release version, #cmd("mbt package") refuses a version whose patch
number is not 0 without
an explicit #cmd("fmid"), and a derived #cmd("delete") for #var("x")#cmd(".")#var("y")#cmd(".0")
when the repository has a release tag #var("x")#cmd(".")#var("y−1")#cmd(".")#var("p")
with #var("p") > 0. It refuses a #cmd("-dev") or #cmd("-rc")#var("n") version
without an explicit #cmd("fmid"), and writes no installation package
(@ref-smp-prerelease). The tag check needs the
repository's tags, which a shallow clone does not have.

== [release] <ref-toml-release>

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("version_files")], [#cmd("[]")], [further files in which
    #cmd("mbt release") replaces the version.],
)

== [tools] <ref-toml-tools>

One key per tool: #var("name")#cmd(" = { ... }").

#table(columns: (1.3in, 1.2in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("repo")], [required], [#var("owner")#cmd("/")#var("repo") on
    GitHub.],
  [#cmd("version")], [required], [the release #cmd("v")#var("version").],
  [#cmd("asset")], [#cmd("{name}-{os}-{arch}.tar.gz")], [the release asset
    for this platform.],
  [#cmd("bin")], [#cmd("{name}-{os}-{arch}")], [the member of a
    #cmd(".tar.gz") or #cmd(".zip") asset that is the program.],
)

In #cmd("asset") and #cmd("bin"), #cmd("{name}"), #cmd("{version}"),
#cmd("{os}") (#cmd("darwin"), #cmd("linux"), #cmd("windows")) and
#cmd("{arch}") (#cmd("amd64"), #cmd("arm64")) are replaced.

== [plugins] <ref-toml-plugins>

One key per plugin: #cmd("\"")#var("owner")#cmd("/")#var("repo")#cmd("\" =
\"")#var("range")#cmd("\""), with the ranges of #cmd("[dependencies]").

== Reserved <ref-toml-reserved>

#cmd("[rule.\"*.")#var("ext")#cmd("\"]"), #cmd("[files.\"")#var("glob")#cmd("\"]"),
#cmd("[lang.*]") and #cmd("[usermod.*]") are reserved for later versions.
