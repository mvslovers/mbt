#import "../bookmaster/bookmaster.typ": *

= Files and Directories <ref-files>

== In the Project <ref-files-project>

#table(columns: (1.9in, 0.9in, 1fr),
  [Path], [Commit], [Contents],
  [#cmd("mbt.toml")], [yes], [the project file (@ref-toml).],
  [#cmd("mbt.lock")], [yes], [the pinned dependencies, tools and plugins
    (@ref-lock).],
  [#cmd("include/")], [yes], [public headers\; on the include path.],
  [#cmd("test/")], [yes], [the tests, found by name\; #cmd("test/tso/*.lua")
    the interactive ones.],
  [#cmd("mbt/init.lua"), #cmd("mbt/lua/")], [yes], [the project's Lua.],
  [#cmd("build/")], [no], [object modules (#var("stem")#cmd(".o"),
    #var("stem")#cmd(".d")), archives, load modules (#var("NAME"),
    #var("NAME")#cmd(".iebcopy")), #cmd("host/") with the workstation's test
    programs, #cmd("dist-stage/"), the deploy's TRANSMIT file, the
    generated jobs and spool files: #cmd("test-runner.jcl"),
    #cmd("test-runner.spool"), #cmd("testlib-receive.spool"),
    #cmd("deploy.spool").],
  [#cmd("dist/")], [no], [the release files of #cmd("mbt package").],
  [#cmd(".mbt/")], [no], [MBT's state: #cmd("deps/"), #cmd("tools/"),
    #cmd("plugins/"), #cmd("include/") (#cmd("mbtcheck.h")),
    #cmd("buildstamp.h"), #cmd("cmd/") (the command line of each step and
    the signature of each task), #cmd("deps.local.toml"), and the files of
    #cmd("mbt migrate")'s check.],
  [#cmd("compile_commands.json")], [no], [written by #cmd("mbt compiledb").],
)

== In the Home Directory <ref-files-home>

#table(columns: (2.1in, 1fr),
  [Path], [Contents],
  [#cmd("~/.mbt/targets.toml")], [the targets (@ref-targets).
    #cmd("MBT_HOME") moves it.],
  [#cmd("~/.mbt/init.lua"), #cmd("lua/")], [your own Lua. #cmd("MBT_HOME")
    moves them.],
  [#cmd("~/.mbt/versions/")], [the versions of MBT that projects pinned.
    #cmd("MBT_HOME") moves them.],
  [#cmd("~/.mbt/v3/cache/"), #cmd("tools/"), #cmd("plugins/")],
    [downloaded archives, by checksum. Always under the home directory.],
  [#cmd("~/.mbt/config.toml")], [MBT 2's settings, read for the fallback
    when there is no target.],
)

A project's #cmd(".env") is read for the same fallback.
