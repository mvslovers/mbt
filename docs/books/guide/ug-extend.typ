#import "../bookmaster/bookmaster.typ": *

= Extending MBT <ug-extend>

#idx("Lua")#idx("extension")
Some projects need a step MBT does not know: a source generated from a
table, a disk image that ships with the product, a server to restart after
a deploy, files to upload. Such steps are written in Lua and run inside
MBT, with a small set of functions that let them act without being able
to change the build. This chapter shows where the Lua lives, the three
kinds of extension, what the Lua code can reach, and how extensions are
shared as plugins.

== Where the Lua Lives <ug-extend-where>

#tab(caption: [Where extensions live])[
  #table(columns: (1.6in, 1fr),
    [File], [For],
    [#cmd("mbt/init.lua")], [the project: what it needs. Further modules go
      into #cmd("mbt/lua/"): #cmd("require(\"webroot\")") loads
      #cmd("mbt/lua/webroot.lua").],
    [#cmd("~/.mbt/init.lua")], [you: what only your workstation needs,
      such as a notice after a release.],
    [a plugin], [several projects: Lua that someone else maintains,
      declared in #cmd("[plugins]") (@ug-extend-plugins).],
  )
] <ug-extend-files>

Only a project with #cmd("mbt.toml") loads Lua. Commit #cmd("mbt/")\; it is
part of the project. At one point of a run, plugins come first, then the
project, then your own file.

== Hooks, Tasks and Commands <ug-extend-kinds>

#idx("hook")
*A hook* runs at a fixed point, before or after one of MBT's commands:

#tab(caption: [Hook points])[
  #table(columns: (2.3in, 1fr),
    [Point], [For example],
    [#cmd("before_build"), #cmd("after_build")], [generate a source\;
      report module sizes],
    [#cmd("before_test"), #cmd("after_test")], [prepare test data\; pass
      the results on. In #cmd("after_test"), #cmd("ctx.kind") says
      #cmd("host"), #cmd("mvs") or #cmd("tso"). #cmd("mbt test --tso")
      runs no #cmd("before_test") hook.],
    [#cmd("before_package"), #cmd("after_package")], [build a file that
      ships in the package],
    [#cmd("before_dist"), #cmd("after_dist")], [add to the installation
      package],
    [#cmd("before_deploy"), #cmd("after_deploy")], [stop a server, start it
      again],
    [#cmd("before_release"), #cmd("after_release")], [refuse a release
      without a changelog section\; announce it],
    [#cmd("on_failure")], [collect what a failed command left],
  )
] <ug-extend-hooks>

```
-- mbt/init.lua: restart the server after a deploy
mbt.hook("after_deploy", function(ctx)
  ctx.console("P HTTPD")
  ctx.console("S HTTPD")
end)
```

An error in a #cmd("before_") hook stops the command before it does
anything. An error in an #cmd("after_") hook undoes nothing, a deploy cannot
be taken back, but the command then fails: a deploy whose restart failed is
not reported as a success.

#idx("task")
*A task* declares its inputs and outputs. It runs before the commands it
names, ahead of their #cmd("before_") hooks and at most once per run: the
first time, when an output is missing or older than an input, when the task
itself, a Lua file or #cmd("[tools]") changed, and every time if it has no
outputs. Its outputs are removed before it runs and after it fails, and a
task that did not write them fails. A task with inputs but no outputs, and
an input that matches no file, are errors:

```
mbt.task {
  name    = "tables",
  before  = { "build" },
  inputs  = { "tools/tables.txt" },
  outputs = { "src/tables.c" },
  run = function(ctx)
    ctx.exec { "python3", "tools/gen.py", ctx.inputs[1], ctx.out[1] }
  end,
}
```

#idx("mbt run")
*A command* of the project's own runs with #cmd("mbt run")
#var("NAME")#cmd(" -- ")#var("args"):

```
mbt.command("upload", function(ctx)
  ctx.exec { ctx.tool("ufsd-utils"), "upload", "build/web.img", "--dsn", "LAB.WEBROOT" }
end)
```

#cmd("mbt run") alone lists the tasks and commands. #cmd("mbt run")
#var("NAME") runs a task even when it is up to date. #cmd("--dry-run") runs
the Lua but starts no program and sends no request to MVS that changes
anything\; the code sees #cmd("ctx.dry_run").

== What the Code Can Reach <ug-extend-ctx>

#idx("ctx")
Every hook, task and command gets one table, #cmd("ctx"). The Lua code has
no other way to the outside: no #cmd("os"), no #cmd("io"), no network of its
own.

#tab(caption: [The functions and values of ctx])[
  #table(columns: (1.9in, 1fr),
    [Name], [What it is],
    [#cmd("ctx.project")], [the project's name, version, modules and tests,
      as a copy.],
    [#cmd("ctx.exec{")#var("argv")#cmd("}")], [runs a program. The arguments
      are a list, and no shell is involved, so a value with blanks stays
      one argument. A non-zero exit code is an error unless
      #cmd("check = false") is given\; #cmd("env = {...}") adds environment
      variables. Under #cmd("mbt run") the program writes to the terminal\;
      in a hook its output is returned.],
    [#cmd("ctx.tool(")#var("name")#cmd(")")], [the path of a tool from
      #cmd("[tools]"), fetched and checked if need be (@ug-deps-tools).],
    [#cmd("ctx.fs")], [#cmd("read"), #cmd("write"), #cmd("exists"),
      #cmd("mkdir"), #cmd("remove") and #cmd("list"), inside the project
      only.],
    [#cmd("ctx.target()")], [the selected MVS system: hosts, ports, users,
      never a password.],
    [#cmd("ctx.console(")#var("cmd")#cmd(")")], [an operator command
      (@ug-targets-console)\; returns the reply lines and the channel that
      delivered it.],
    [#cmd("ctx.mvs.request{")...#cmd("}")], [a request to mvsMF in MBT's
      session: method, path, body\; returns status and body.],
    [#cmd("ctx.mvs.token()")], [the session token, to hand to another
      program in its environment.],
    [#cmd("ctx.out"), #cmd("ctx.inputs")], [in a task: its outputs, and the
      files its inputs matched.],
    [#cmd("ctx.result")], [in an #cmd("after_") hook: what the command
      produced\; in #cmd("on_failure"): #cmd("command") and #cmd("error").],
    [#cmd("ctx.args"), #cmd("ctx.kind"), #cmd("ctx.dry_run")], [the
      arguments of #cmd("mbt run"), the kind of test run, the dry run.],
    [#cmd("ctx.log"), #cmd("ctx.warn")], [write a line of MBT's output.],
  )
] <ug-extend-ctx-tab>

Each call into Lua runs with a limit on the work it may do and on its
memory (256 MiB). Programs started with #cmd("ctx.exec") are not limited.
#cmd("print") writes to MBT's output.

#idx("extension", "rules")
*Extensions act, they do not change the build.* They can write files, run
programs, issue commands and stop a command. Compile options, sources and
module attributes change only in #cmd("mbt.toml"), so a build can always
be explained from the project file.

== Plugins <ug-extend-plugins>

#idx("plugin")#idx("[plugins]")
Lua that several projects need is published as a plugin: a GitHub release
of its own repository, declared and pinned like a dependency:

```
[plugins]
"mvslovers/mbt-ufs" = "^0.1"
```

```
local ufs = require("mvslovers/mbt-ufs")
ufs.webroot { from = "static", image = "build/webroot/web.img" }
```

#cmd("mbt deps") resolves the range, fetches the release's
#var("repo")#cmd("-")#var("version")#cmd("-plugin.tar.gz"), records its
checksum in #cmd("mbt.lock") and stages it in #cmd(".mbt/plugins/"). A build
uses only what is staged. The archive holds #cmd("plugin.toml"),
#cmd("init.lua") and #cmd("lua/"). #cmd("plugin.toml") names the version of
the interface the plugin was written for, and the programs it may start:
#cmd("ctx.exec") from a plugin's code runs nothing else. A plugin written
for an interface MBT does not provide is refused with a message.

#cmd(".mbt/deps.local.toml") can point a plugin at a working copy, as it
does for a dependency (@ug-deps-local).
