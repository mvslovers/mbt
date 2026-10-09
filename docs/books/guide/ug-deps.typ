#import "../bookmaster/bookmaster.typ": *

= Dependencies <ug-deps>

#idx("dependency")
A project can use the libraries of other projects. MBT fetches them from
the projects' GitHub releases, keeps the exact version and checksum of each
in the file #cmd("mbt.lock"), and builds with what it fetched. This chapter
shows how a project declares a dependency, how the versions are chosen and
pinned, how to build against a working copy of another project, and how
the toolchain and host tools fit into the same scheme.

== Declaring a Dependency <ug-deps-declare>

#idx("[dependencies]")
The project #cmd("b64") encodes a string in base64 with the library of the
project #cmd("crypto370"):

#fig(caption: [src/b64.c])[
  #code(read("../ex/ug-deps/b64.c"), numbers: true)
] <ug-deps-b64-c>

The header #cmd("<base64.h>") and the function #cmd("base64_encode") come
from #cmd("crypto370"). The project file says so in #cmd("[dependencies]"):

#fig(caption: [mbt.toml of the project b64])[
  #code(read("../ex/ug-deps/mbt.toml"), numbers: true)
] <ug-deps-toml>

The key is the GitHub repository, #var("owner")#cmd("/")#var("repo"), in
quotes because of the slash\; the value is the range of versions the
project works with (@ug-deps-ranges).

== Fetching: mbt deps <ug-deps-fetch>

#idx("mbt deps")
#cmd("mbt deps") resolves each range against the releases of the
repository, downloads the library of the version it chose, and stages it in
the project:

#fig(caption: [Fetching the dependencies])[
  #screen(raw(read("../ex/ug-deps/deps.txt")))
] <ug-deps-fetch-fig>

A library release is an archive
#var("repo")#cmd("-")#var("version")#cmd("-lib.tar.gz") with two
directories, #cmd("include/") and #cmd("lib/"). MBT unpacks it into
#cmd(".mbt/deps/")#var("repo")#cmd("/"):

```
.mbt/deps/crypto370/include/base64.h
.mbt/deps/crypto370/include/blowfish.h
.mbt/deps/crypto370/include/sha256.h
.mbt/deps/crypto370/lib/crypto370.a
```

From there the build takes it without being told: every
#cmd(".mbt/deps/*/include") is on the include path, and every archive in
#cmd(".mbt/deps/*/lib") is searched when a module is linked, after the
project's own code, with the C library searched before and after them:

#fig(caption: [Building with a dependency])[
  #screen(raw(read("../ex/ug-deps/build.txt")))
] <ug-deps-build-fig>

The linkage editor takes from the archive only the object modules the
program uses, here the base64 encoder, not SHA-256 or Blowfish.

#cmd("mbt build") does not fetch anything itself. When a dependency is
declared but not staged, it stops and says so:

```
[mbt] ERROR: dependencies not staged: mvslovers/crypto370 -- run 'mbt deps'
```

Run #cmd("mbt deps") after cloning a project, after changing
#cmd("[dependencies]"), and after #cmd("mbt distclean").

== Version Ranges <ug-deps-ranges>

#idx("version range")#idx("semantic versioning")
Versions in the ecosystem have three numbers,
#var("major")#cmd(".")#var("minor")#cmd(".")#var("patch"), optionally
followed by #cmd("-dev") for a version in development or
#cmd("-rc")#var("n") for a release candidate. They are ordered by the
numbers, and for equal numbers #cmd("-dev") comes before
#cmd("-rc")#var("n"), which comes before the release: #cmd("1.0.2-dev")
\< #cmd("1.0.2-rc1") \< #cmd("1.0.2").

#tab(caption: [Version ranges])[
  #table(columns: (1.5in, 1fr),
    [Range], [Allows],
    [#cmd("^1")], [1.0.0 and later, below 2.0.0],
    [#cmd("^1.4")], [1.4.0 and later, below 2.0.0],
    [#cmd("^0.3")], [0.3.0 and later, below 0.4.0: below 1.0, each minor
      may break the last],
    [#cmd(">=1.2.0")], [1.2.0 and every later version],
    [#cmd(">=1.2.0,<1.4.0")], [both conditions: 1.2.0 up to, not
      including, 1.4.0],
    [#cmd("=1.3.1")], [exactly 1.3.1],
  )
] <ug-deps-ranges-tab>

#idx("prerelease", "dependency")
*Prereleases only when asked for.* A version with #cmd("-dev") or
#cmd("-rc")#var("n") is chosen only for a range that names a prerelease
itself, such as #cmd(">=1.0.2-dev"). #cmd("^1") above chose 1.0.1 although
1.0.2-dev was already published. A project that needs a change not yet
released asks for it explicitly, and only until the release is out.

#cmd("^") is the range to use normally: a project that works with 1.4 of a
library works with every later 1._x_ as well, if the library keeps to the
rule that only a new major version may break its callers.

== The Lock File <ug-deps-lock>

#idx("mbt.lock")
#cmd("mbt deps") writes the version it chose and the SHA-256 checksum of the
archive into #cmd("mbt.lock"):

#fig(caption: [mbt.lock])[
  #code(read("../ex/ug-deps/mbt.lock"))
] <ug-deps-lock-fig>

*Commit #cmd("mbt.lock").* From then on, #cmd("mbt deps") on any workstation
and in CI fetches exactly that version and checks that the archive has
exactly that checksum. Two builds of the same commit link the same code,
whatever was released in between.

#idx("mbt deps", "--update")
*Nothing but #cmd("mbt deps --update") moves a pin.* When the range in
#cmd("mbt.toml") no longer includes the pinned version, #cmd("mbt deps")
stops with return code 3 rather than choose another one. Here the range was
changed to #cmd(">=1.0.2-dev"):

#fig(caption: [A pin outside its range])[
  #screen(raw(read("../ex/ug-deps/drift.txt")))
] <ug-deps-drift>

#cmd("mbt deps --update") resolves every range again, fetches what it
chose and rewrites the lock:

#fig(caption: [Moving the pins])[
  #screen(raw(read("../ex/ug-deps/update.txt")))
] <ug-deps-update>

The same holds when an archive no longer has the checksum the lock
records, which happens when a prerelease is published again under the same
version: #cmd("mbt deps") refuses it until #cmd("mbt deps --update")
accepts the new one. A prerelease that was fetched once stays buildable on
that workstation, because MBT keeps the archives it downloaded, by
checksum.

A dependency newly added to #cmd("[dependencies]") is resolved and added to
the lock by a plain #cmd("mbt deps"): that is a deliberate change, not a
drift.

== Building Against a Working Copy <ug-deps-local>

#idx("deps.local.toml")#idx("dependency", "working copy")
When two projects change together, the dependent one can build against the
other's working copy instead of a release. Name it in
#cmd(".mbt/deps.local.toml"):

```
[override]
"mvslovers/crypto370" = { path = "../crypto370" }
```

#cmd("mbt deps") then stages that dependency from the working copy: the
library its last build left in #cmd("build/") and the headers its
#cmd("[lib]") names. Build the library there first. GitHub and the lock are
not consulted for it, and #cmd("mbt.lock") keeps the release pin, so
deleting the file returns to the release. #cmd(".mbt/") is not committed,
so the override never reaches anyone else.

#note[*To be confirmed:* that the override of MBT 3 stages exactly as
described, from #cmd("build/") and the #cmd("[lib]") headers of the working
copy.]

== The Toolchain <ug-deps-toolchain>

#idx("[toolchain]")
CC/370 and LIBC/370 are not dependencies in this sense: they are installed
on the workstation, and every project there builds with the same ones
(@ug-install). #cmd("[toolchain]") records which releases the project
expects:

```
[toolchain]
mbt     = "3.0"
cc370   = "1.5.0"
libc370 = "2.4.0"
```

#deflist(width: 1.1in,
  [#cmd("libc370")], [the oldest LIBC/370 the project builds with. A build
    on a workstation with an older one stops\; @ug-deps-build-fig shows the
    check passing. Since MBT 3 needs the C start-up inside #cmd("libc.a"),
    the value must be 2.3.0 or later.],
  [#cmd("cc370")], [the CC/370 the project expects. An older one on the
    workstation draws a warning.],
)

The release workflow builds a release with exactly these two
(@ug-release)\; the build workflow for pull requests builds with the current
development state of CC/370 and LIBC/370, so that a change there that breaks
the project shows before the next release.

== Host Tools <ug-deps-tools>

#idx("[tools]")
Some projects need a program on the workstation besides the toolchain, for
example to build a disk image that the project ships. Such a tool can be
fetched from a GitHub release and pinned like a dependency:

```
[tools]
ufsd-utils = { repo = "mvslovers/ufsd-utils", version = "1.0.1" }
```

#cmd("mbt deps") downloads the archive of the tool for the workstation's
platform and records its checksum in #cmd("mbt.lock"), together with those
of the other platforms, so a lock written on a Mac also checks the
download of a Linux build machine. The project's Lua code reaches the tool
by name (@ug-extend). The release asset and the member of the archive that
is the program follow a naming rule that #cmd("asset") and #cmd("bin")
can change\; the _MBT Reference_ describes both.
