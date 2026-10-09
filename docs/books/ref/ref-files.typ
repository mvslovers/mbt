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
    programs, the generated jobs and spool files such as
    #cmd("test-runner.jcl") and #cmd("deploy.spool").],
  [#cmd("dist/")], [no], [the release files of #cmd("mbt package").],
  [#cmd(".mbt/")], [no], [MBT's state: #cmd("deps/"), #cmd("tools/"),
    #cmd("plugins/"), #cmd("include/") (#cmd("mbtcheck.h")),
    #cmd("buildstamp.h"), #cmd("cmd/") (the command line of each step),
    #cmd("deps.local.toml").],
  [#cmd("compile_commands.json")], [no], [written by #cmd("mbt compiledb").],
)

== In the Home Directory <ref-files-home>

#cmd("~/.mbt"), or the directory #cmd("MBT_HOME") names:

#table(columns: (1.9in, 1fr),
  [Path], [Contents],
  [#cmd("targets.toml")], [the targets (@ref-targets).],
  [#cmd("init.lua"), #cmd("lua/")], [your own Lua.],
  [#cmd("versions/")], [the versions of MBT that projects pinned.],
  [#cmd("cache/")], [downloaded archives, by checksum.],
)
