#import "../bookmaster/bookmaster.typ": *

= Building <ug-build>

#idx("mbt build")
This chapter describes how MBT builds a project: what it compiles and links,
with which options, and how a project describes its load modules, its
library and the code they share. It uses a project with a C program, a C
source and an assembler routine, and a library made of the latter two.

== The Example Project <ug-build-example>

The project #cmd("sums") computes the sum and the mean of some numbers. The
sum is an assembler routine, #cmd("ADDUP"), the mean a C function that
calls it, and a C program prints both:

```
sums/
  mbt.toml
  include/sums.h      declares addup() and mean()
  asm/addup.asm       the sum, in assembler
  src/mean.c          the mean, in C
  app/sums.c          the program SUMS
```

#fig(caption: [mbt.toml of the project sums])[
  #code(read("../ex/ug-build/mbt.toml"), numbers: true)
] <ug-build-toml>

#cmd("addup") and #cmd("mean") are used twice: by the program of this
project, and by other projects, to which #cmd("sums") offers them as a
library. The project file therefore names them twice, in
#cmd("[internal]") for its own load modules and in #cmd("[lib]") for
others. Both are described below.

== What a Build Does <ug-build-steps>

#idx("mbt build", "steps")
@ug-build-v shows the build of everything in this project. #cmd("--all")
builds the library as well as the load modules, and #cmd("-v") shows each
command instead of a short line per step.

#fig(caption: [Building the project sums])[
  #screen(raw(read("../ex/ug-build/build-v.txt")))
] <ug-build-v>

A build has four kinds of steps:

+ *Compiling and assembling.* Every C source is compiled with
  #cmd("cc370"), every #cmd(".asm") or #cmd(".s") source assembled with
  #cmd("as370"), each into an object module in #cmd("build/"). The object
  module is named after the source, without its directory:
  #cmd("app/sums.c") becomes #cmd("build/sums.o").
+ *Archiving.* The object modules of #cmd("[internal]") become the private
  archive #cmd("build/")#var("project")#cmd("int.a"), and those of
  #cmd("[lib]") the library #cmd("build/")#var("name")#cmd(".a"), with
  #cmd("ar370").
+ *Checking.* Before the first link, MBT checks that no module declared
  reentrant holds writable data (@ug-build-moddata).
+ *Linking.* Each load module is linked with #cmd("ld370") from its own
  object modules, the private archive, the libraries of the project's
  dependencies (@ug-deps) and the C library, into #cmd("build/")#var("NAME")
  and #cmd("build/")#var("NAME")#cmd(".iebcopy").

#note[Because the directory is dropped, two sources of the same name, such
as #cmd("src/util.c") and #cmd("lib/util.c"), would make the same object
module, and only one of them is built. Give every source of a project its
own name.]

Independent steps run at the same time, as many as the workstation has
processors\; #cmd("-j") #var("n") sets another number. A step runs again
when one of its inputs changed or a header it includes did, and also when
its command changed, for example because a flag in #cmd("mbt.toml") did. A
build in which nothing changed does nothing.

=== What to Build <ug-build-what>

#deflist(width: 1.7in,
  [#cmd("mbt build")], [the main product: the library of a project of kind
    #cmd("library"), the load modules of any other.],
  [#cmd("mbt build --all")], [the load modules and the library.],
  [#cmd("mbt build --tests")], [also the tests, as load modules for MVS.
    #cmd("mbt test") builds them for the workstation by itself
    (@ug-test).],
  [#cmd("mbt build") #var("NAME")...], [only the named load modules or
    tests. Case does not matter: #cmd("mbt build sums") builds
    #cmd("SUMS").],
)

#idx("mbt clean")#idx("mbt distclean")
#cmd("mbt clean") removes #cmd("build/") and #cmd("dist/").
#cmd("mbt distclean") also removes #cmd(".mbt/"), with the dependencies,
tools and plugins MBT staged there\; #cmd("mbt.lock") stays.

== Compiling <ug-build-compile>

#idx("compile options")
Every C source is compiled with the same options, in this order:

+ MBT's own: #cmd("-O1 -std=gnu99 -Wall -Wextra -Werror"). The code is
  optimized at the level CC/370 is validated at, the dialect is C99 with
  the GNU extensions, and every warning stops the build.
+ #cmd("-I include"), when the project has a directory #cmd("include/"),
  then a #cmd("-I") for each directory in #cmd("[build] include").
+ #cmd("[build] cflags").
+ A #cmd("-I") for the headers of each dependency, for MBT's own headers
  (#cmd(".mbt/include"), with #cmd("<mbtcheck.h>")) and for #cmd(".mbt")
  (with #cmd("<buildstamp.h>")).

```
[build]
include = ["src/private"]            # besides include/
cflags  = ["-DNDEBUG", "-std=gnu89"]
asflags = ["-I", "maclib"]
```

An option in #cmd("cflags") comes after MBT's and wins where the two
disagree: #cmd("-std=gnu89") above compiles C89 again, #cmd("-O0") turns the
optimization off. #cmd("asflags") are passed to #cmd("as370"), which by
itself finds the macros of CC/370 and LIBC/370\; give it the directories of
the project's own macros.

=== The Build Stamp <ug-build-stamp>

#idx("buildstamp.h")#idx("build stamp")
Before it compiles, MBT writes the header #cmd(".mbt/buildstamp.h"). A
program that includes it can say exactly which build it is, as
#cmd("app/sums.c") does:

#fig(caption: [app/sums.c])[
  #code(read("../ex/ug-build/sums.c"), numbers: true)
] <ug-build-sums-c>

#deflist(width: 1.7in,
  [#cmd("MBT_PROJECT")], [the project's name, as a string.],
  [#cmd("MBT_VERSION")], [its version, as a string.],
  [#cmd("MBT_COMMIT")], [the short commit of the checkout, followed by
    #cmd("-dirty") when tracked files have uncommitted changes, or
    #cmd("unknown") outside a Git repository.],
  [#cmd("MBT_COMMIT_DIRTY")], [1 for uncommitted changes, else 0.],
)

The header holds no time, and MBT rewrites it only when one of the values
changed, so an unchanged commit compiles nothing again.

=== Assembler Sources <ug-build-asm>

#idx("assembler source")
A source ending in #cmd(".asm") or #cmd(".s") is assembled with
#cmd("as370"). It is listed like a C source and can be part of a load
module, of #cmd("[internal]") or of #cmd("[lib]"). @ug-build-addup is the
routine of the example. How assembler code and C call each other is
described in the _CC/370 User's Guide_, Chapter 5.

#fig(caption: [asm/addup.asm])[
  #code(read("../ex/ug-build/addup.asm"), numbers: true)
] <ug-build-addup>

== Load Modules <ug-build-modules>

#idx("load module", "declaring")
Each load module is a table #cmd("[module.")#var("NAME")#cmd("]"),
#var("NAME") being the member name, one to eight characters. A name with
#cmd("#"), #cmd("@") or #cmd("$") is written in quotes:
#cmd("[module.\"IRX#HELO\"]").

#tab(caption: [The keys of a load module])[
  #table(columns: (1.1in, 1fr),
    [Key], [Meaning],
    [#cmd("sources")], [the sources of this module alone. A pattern such as
      #cmd("\"src/*.c\"") stands for every file it matches. Required.],
    [#cmd("exclude")], [sources the patterns of #cmd("sources") match but
      the module does not use.],
    [#cmd("rent")], [#cmd("true") when the module is reentrant: one copy is
      shared by every task that runs it. Required.],
    [#cmd("reus")], [#cmd("true") when the module is serially reusable: a
      copy can be used again once a task has finished with it.
      Required.],
    [#cmd("refr")], [#cmd("true") when the module is refreshable: it never
      changes itself. Default #cmd("false").],
    [#cmd("ac")], [the authorization code: #cmd("1") for a program that
      must run APF-authorized. Default #cmd("0").],
    [#cmd("entry")], [the entry point. Default #cmd("@@CRT0"), the start-up
      routine of a C program.],
    [#cmd("startup")], [how the C run time comes in, see below.],
    [#cmd("dep_startup")], [#cmd("true") when a dependency provides the
      start-up routine #cmd("@@START"), as the library of an HTTP server
      does for its CGI modules\; #cmd("false") keeps that of LIBC/370.],
    [#cmd("aliases")], [further names of the module, such as
      #cmd("[\"REXX\", \"RX\"]").],
  )
] <ug-build-module-keys>

#idx("RENT")#idx("REUS")
*#cmd("rent") and #cmd("reus") have no default.* Whether a module may be
shared between tasks is a property of its code, and a wrong guess is not
caught by any build: it shows on MVS, as an abend or as two tasks
overwriting each other's data. So MBT makes every module say it. Most C
programs that keep data in #cmd("static") or global variables are
#cmd("rent = false") and #cmd("reus = false").

#idx("startup")
*The start-up of a C program* comes out of the C library: with the entry
point #cmd("@@CRT0"), the linkage editor finds the start-up routine in
#cmd("libc.a") and with it everything that runs before #cmd("main").
#cmd("startup") changes that:

#deflist(width: 1.4in,
  [(omitted)], [a C program, as above.],
  [#cmd("false")], [no C run time: a module with its own entry point,
    usually in assembler. Set #cmd("entry") to it.],
  [#cmd("\"crtm\"")], [a C module that another C program links to or calls:
    it uses the C run time of its caller instead of starting its own. See
    the _LIBC/370 Programmer's Guide_, "The Start-Up".],
)

== Shared Code: [internal] <ug-build-internal>

#idx("[internal]")
Code that several load modules of the project use belongs into
#cmd("[internal]"). Its sources are built once into a private archive,
which every load module and every test of the project is linked with. The
linkage editor takes from it only the object modules a program refers to,
directly or through another, so a module that does not use a function does
not carry it.

```
[internal]
sources = ["src/*.c", "asm/*.asm"]
exclude = ["src/debug.c"]
```

The archive is not published: it belongs to the project.

== A Library: [lib] <ug-build-lib>

#idx("[lib]")#idx("library", "building")
A project that other projects link with describes its library in
#cmd("[lib]"):

#deflist(width: 1.2in,
  [#cmd("name")], [the library's name. The archive is
    #cmd("build/")#var("name")#cmd(".a").],
  [#cmd("sources")], [the sources of the object modules in it.],
  [#cmd("headers")], [the headers that declare what it offers. They are
    published with it, and a project that depends on it compiles with
    them.],
)

#cmd("mbt build") builds the library for a project of kind
#cmd("library"), #cmd("mbt build --all") for any project. #cmd("mbt
package") publishes it with the project's release, as an archive of
#cmd("lib/") and #cmd("include/") that other projects fetch
(@ug-deps). A library is an archive of object modules, not a load module:
it never goes to MVS as it is, but into the load modules that are linked
with it.

Many projects build the same sources twice, as #cmd("sums") does: in
#cmd("[internal]") for their own programs, and in #cmd("[lib]") for others.

== Writable Data in Reentrant Modules <ug-build-moddata>

#idx("module-data check")#idx("RENT", "writable data")
A module declared #cmd("rent = true") is one copy that every task running
it shares, and MVS may load it into storage that cannot be written to. It
must not keep data it changes in itself. Before it links such a module,
MBT looks through its sources for data that would land in the module
itself: variables defined outside functions, and #cmd("static") variables
inside them, that are not #cmd("const"). In a #cmd("rent = true") module
that is an error. A module with #cmd("ac = 1") is checked too, and gets a
warning, since MVS fetches an authorized module into storage the program
cannot change. Modules that are neither, and tests, are not checked.
@ug-build-moddata-fig shows what happens when #cmd("v") in
#cmd("app/sums.c") loses its #cmd("const").

#fig(caption: [Writable data in a reentrant module])[
  #screen(raw(read("../ex/ug-build/moddata.txt")))
] <ug-build-moddata-fig>

Nothing is linked then. Make the data #cmd("const"), keep it on the stack or
in storage the program obtains, or declare the module #cmd("rent = false").
#cmd("mbt module-data") runs the check alone\; it reports the first three
findings of each module, #cmd("--all") every one.

== An Editor That Knows the Build <ug-build-compiledb>

#idx("mbt compiledb")#idx("compile_commands.json")
#cmd("mbt compiledb") writes #cmd("compile_commands.json"), the list of
compile commands that editors with a C language server, such as
#cmd("clangd"), read. The editor then finds the headers of CC/370, LIBC/370,
the dependencies and the project as the build does. Run it again when the
sources or the options change. The file belongs to the workstation\; add it
to #cmd(".gitignore").
