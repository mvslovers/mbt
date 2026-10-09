#import "../bookmaster/bookmaster.typ": *

= The Lua API <ref-lua>

#idx("Lua", "API")
MBT runs Lua 5.5 from #cmd("mbt/init.lua") of an #cmd("mbt.toml") project,
from #cmd("~/.mbt/init.lua"), and from staged plugins, in this order:
plugins, the project, the user. #cmd("require(\"")#var("name")#cmd("\")")
loads #cmd("mbt/lua/")#var("name")#cmd(".lua") (or #cmd("~/.mbt/lua/")),
#cmd("require(\"")#var("owner")#cmd("/")#var("repo")#cmd("\")") a plugin.
#cmd("dofile"), #cmd("loadfile") and #cmd("package") are not available, nor
are #cmd("os") and #cmd("io"). Each call into Lua runs under a limit of
processor time and memory. The interface has a version, #cmd("mbt.api")\;
this chapter describes version 1.

== The Table mbt <ref-lua-mbt>

#deflist(width: 2.4in,
  [#cmd("mbt.hook(")#var("point")#cmd(", ")#var("fn")#cmd(")")], [runs
    #var("fn(ctx)") at #var("point"): #cmd("before_") or #cmd("after_")
    followed by #cmd("build"), #cmd("test"), #cmd("package"), #cmd("dist"),
    #cmd("deploy") or #cmd("release"), or #cmd("on_failure").],
  [#cmd("mbt.task{")...#cmd("}")], [declares a task (@ref-lua-task).],
  [#cmd("mbt.command(")#var("name")#cmd(", ")#var("fn")#cmd(")")], [makes
    #var("fn(ctx)") the command #cmd("mbt run") #var("name"). Also
    #cmd("mbt.command{ name = ..., description = ..., run = ... }").],
  [#cmd("mbt.api")], [the interface version, #cmd("1").],
  [#cmd("mbt.version")], [the version of MBT.],
)

*Errors.* An error in a #cmd("before_") hook or in a task stops the command
before it runs. An error in an #cmd("after_") hook fails the command and
undoes nothing. An #cmd("on_failure") hook gets #cmd("ctx.result") with
#cmd("command") and #cmd("error")\; its own errors are only warnings.

== Tasks <ref-lua-task>

#idx("task")
```
mbt.task {
  name        = "webroot",
  description = "the web root image",
  before      = { "package", "dist" },
  inputs      = { "static/**" },
  outputs     = { "build/webroot/httpd-webroot.img" },
  run         = function(ctx) ... end,
}
```

#deflist(width: 1.2in,
  [#cmd("name")], [required\; #cmd("mbt run") #var("name") runs it.],
  [#cmd("before")], [the commands it runs ahead of. Without it, only
    #cmd("mbt run") runs it.],
  [#cmd("inputs")], [files, directories and patterns (#cmd("*"),
    #cmd("?"), #cmd("**")).],
  [#cmd("outputs")], [the files it writes.],
  [#cmd("run")], [the function.],
)

A task is skipped while its outputs are newer than every input and nothing
that made them changed: the task itself, the Lua files loaded, the
#cmd("[tools]"). Its outputs are removed before it runs and after it fails.

== The Table ctx <ref-lua-ctx>

#idx("ctx")
#table(columns: (2.3in, 1fr),
  [Member], [Meaning],
  [#cmd("ctx.project")], [a copy: #cmd("name"), #cmd("version"),
    #cmd("modules"), #cmd("tests").],
  [#cmd("ctx.args")], [the arguments after #cmd("--") of #cmd("mbt run").],
  [#cmd("ctx.dry_run")], [#cmd("true") under #cmd("--dry-run").],
  [#cmd("ctx.kind")], [in test hooks: #cmd("host"), #cmd("mvs") or
    #cmd("tso").],
  [#cmd("ctx.result")], [in #cmd("after_") hooks: what the command
    produced, such as the modules built or the tag released.],
  [#cmd("ctx.out"), #cmd("ctx.inputs")], [in a task: its outputs, and the
    files its inputs matched.],
  [#cmd("ctx.log(")#var("s")#cmd(")"), #cmd("ctx.warn(")#var("s")#cmd(")")],
    [a line of output, or a warning.],
  [#cmd("ctx.exec{")#var("argv")...#cmd(", env = {}, check = true}")],
    [runs a program without a shell. Returns its output and exit code.
    #cmd("env") adds environment variables. With #cmd("check = false"), a
    non-zero exit code is returned instead of raised as an error. Under
    #cmd("--dry-run") nothing runs. In a plugin, only the programs its
    #cmd("plugin.toml") names.],
  [#cmd("ctx.tool(")#var("name")#cmd(")")], [the path of a #cmd("[tools]")
    program, fetched and checked if need be.],
  [#cmd("ctx.fs.read(")#var("p")#cmd(")"), #cmd("write(")#var("p")#cmd(", ")#var("s")#cmd(")"),
    #cmd("exists"), #cmd("mkdir"), #cmd("remove"), #cmd("list")], [files,
    inside the project only. #cmd("remove") does not remove the project
    itself.],
  [#cmd("ctx.target()")], [the selected target: its name, hosts, ports and
    users, never a password.],
  [#cmd("ctx.console(")#var("cmd")#cmd(")")], [an operator command through
    the target's console chain. Returns the reply lines and the channel
    that delivered it.],
  [#cmd("ctx.mvs.request{ method, path, body, content_type }")], [a request
    to mvsMF in MBT's session. Returns status and body.],
  [#cmd("ctx.mvs.token()")], [the session token, for another program's
    environment.],
)

#note[*To be confirmed:* the default of #cmd("check") in #cmd("ctx.exec"),
and the members of the table #cmd("ctx.target()") returns.]

== Plugins <ref-lua-plugins>

#idx("plugin")
A plugin is the release asset
#var("repo")#cmd("-")#var("version")#cmd("-plugin.tar.gz") of its
repository, holding:

#deflist(width: 1.2in,
  [#cmd("plugin.toml")], [#cmd("api = 1"), the interface version it is
    written for, and #cmd("exec = [")#var("programs")#cmd("]"), the programs
    it may start.],
  [#cmd("init.lua")], [run when the plugin is loaded\; it returns the
    plugin's module.],
  [#cmd("lua/")], [further modules:
    #cmd("require(\"")#var("owner")#cmd("/")#var("repo")#cmd("/")#var("mod")#cmd("\")").],
)

A plugin without #cmd("api"), without #cmd("init.lua"), or for an interface
version MBT does not offer is a configuration error.
