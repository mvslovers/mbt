#import "../bookmaster/bookmaster.typ": *

= Files and Directories <ref-files>

== In the Project <ref-files-project>

#table(columns: (1.9in, 0.9in, 1fr),
  [Path], [Commit], [Contents],
  [#cmd("mbt.toml")], [yes], [the project file (@ref-toml).],
  [#cmd("mbt.lock")], [yes], [the pinned dependencies, tools and plugins
    (@ref-lock).],
  [#cmd("project.toml"), #cmd("VERSION"), #cmd("Makefile")], [yes], [MBT 2's
    files. #cmd("project.toml") is read when there is no #cmd("mbt.toml").
    #cmd("mbt migrate") reads all three, writes #cmd("mbt.toml"), and removes
    #cmd("project.toml") and #cmd("VERSION").],
  [the files of #cmd("[release] version_files")], [yes], [#cmd("mbt release")
    writes the new version into them and into #cmd("mbt.toml").],
  [#cmd("include/")], [yes], [public headers\; on the include path.],
  [#cmd("test/")], [yes], [the tests: every #cmd(".c") and
    #cmd(".asm") file below it, named by its stem in upper case, unless
    #cmd("[tests] exclude") lists it\; #cmd("test/tso/")#var("name")#cmd(".lua")
    the interactive ones.],
  [#cmd("mbt/init.lua"), #cmd("mbt/lua/")], [yes], [the project's Lua.],
  [#cmd("build/")], [no], [object modules (#var("stem")#cmd(".o"),
    #var("stem")#cmd(".d")), archives, load modules (#var("NAME"),
    #var("NAME")#cmd(".iebcopy")), #cmd("host/") with the workstation's test
    programs, #cmd("cobol/") and #cmd("pli/") (the generated assembler and
    its #cmd(".d") files), #cmd("dist-stage/"), the TRANSMIT files
    #var("project")#cmd(".deploy.xmit") and
    #var("project")#cmd(".test.xmit"), the
    generated jobs and spool files: #cmd("test-runner.jcl"),
    #cmd("test-runner.spool"), #cmd("testlib-receive.spool"),
    #cmd("deploy.spool").],
  [#cmd("dist/")], [no], [the release files of #cmd("mbt package"), and the SMP
    package of #cmd("mbt package") and #cmd("mbt dist").],
  [#cmd(".mbt/")], [no], [MBT's state: #cmd("deps/"), #cmd("tools/"),
    #cmd("plugins/"), #cmd("include/") (#cmd("mbtcheck.h")),
    #cmd("buildstamp.h"), #cmd("cmd/") (the command line of each step and
    the signature of each task), #cmd("deps.local.toml"),
    #cmd("libc370-checked") (the last libc370 check),
    #cmd("migrate-check.toml") and #cmd("migrate-failed.toml") (the files of
    #cmd("mbt migrate")'s check). #cmd("mbt distclean") removes the whole
    directory, #cmd("deps.local.toml") too.],
  [#cmd("compile_commands.json")], [no], [written by #cmd("mbt compiledb").],
)

== In the Home Directory <ref-files-home>

#table(columns: (2.1in, 1fr),
  [Path], [Contents],
  [#cmd("~/.mbt/targets.toml")], [the targets (@ref-targets).
    #cmd("MBT_HOME") moves it. #cmd("mbt target import") creates or extends
    it, readable by you only.],
  [#cmd("~/.mbt/init.lua"), #cmd("lua/")], [your own Lua. #cmd("MBT_HOME")
    moves them.],
  [#cmd("~/.mbt/versions/")], [the versions of MBT that projects pinned.
    #cmd("MBT_HOME") moves them.],
  [#cmd("~/.mbt/v3/cache/")], [downloaded archives by checksum:
    dependencies directly below it, tools in #cmd("tools/"), plugins in
    #cmd("plugins/"). Always under the home directory.],
  [#cmd("~/.mbt/config.toml")], [MBT 2's settings, read for the fallback
    when there is no target.],
)

A project's #cmd(".env") is read for the same fallback.
