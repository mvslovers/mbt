#import "../bookmaster/bookmaster.typ": *

= mbt.toml <ref-toml>

#idx("mbt.toml")
#cmd("mbt.toml") describes a project, in TOML, schema 3. This chapter lists
every table and key, with its default. #var("NAME") below is the project
name in upper case.

A key not listed here is an error that names it, with return code 2, in
every table except #cmd("[dependencies]") and #cmd("[plugins]"), whose keys
are repository names, and #cmd("[tools]"), whose entries are checked when a
tool is needed (return code 3). The reserved tables of @ref-toml-reserved
are errors too. The types of the values are not checked: give them as
shown. A directory that holds both #cmd("mbt.toml") and an MBT 2
#cmd("project.toml") is an error, and so are the MBT 2 forms
#cmd("[[module]]") and #cmd("[[test]]").


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
)

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
    #cmd("\"crtm\""): a C module run by another C program.],
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
    or #cmd("{ batch = ..., tso = ... }").],
  [#cmd("fixtures")], [none], [#cmd("{ ")#var("DD")#cmd(" = [")#var("files")#cmd("] }"):
    members loaded into a data set per DD.],
)

The keys of a load module, #cmd("exclude"), #cmd("entry"),
#cmd("startup"), #cmd("dep_startup"), #cmd("ac"), #cmd("rent"),
#cmd("reus") and #cmd("refr"), are allowed too.

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
)

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
    version whose patch number is not 0.],
  [#cmd("delete")], [with a derived #cmd("fmid"): #var("prefix") + major +
    (minor − 1) + #cmd("0")\; else #cmd("[]")], [the FMIDs this release
    replaces. With a derived #cmd("fmid"), required for
    #var("x")#cmd(".0.0")\; #cmd("[]") for a first level. It must not name
    the release's own FMID.],
  [#cmd("system")], [#cmd("\"Z038\"")], [the SREL of the #cmd("++VER").],
  [#cmd("prereq")], [#cmd("[]")], [SYSMODs that must be installed.],
  [#cmd("accept_fmid")], [#cmd("true")], [accept the FMID in the
    installation job.],
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
with #var("p") > 0. For #cmd("-dev") and #cmd("-rc")#var("n") versions it
warns and writes no installation package. The tag check needs the
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
