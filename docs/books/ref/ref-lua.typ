#import "../bookmaster/bookmaster.typ": *

= The Lua API <ref-lua>

#idx("Lua", "API")
MBT runs Lua 5.5 from #cmd("mbt/init.lua") of an #cmd("mbt.toml") project,
from #cmd("~/.mbt/init.lua"), and from staged plugins, in this order:
plugins, the project, the user. #cmd("require(\"")#var("name")#cmd("\")")
loads #cmd("mbt/lua/")#var("name")#cmd(".lua") (or #cmd("~/.mbt/lua/")),
#cmd("require(\"")#var("owner")#cmd("/")#var("repo")#cmd("\")") a plugin.
#cmd("dofile"), #cmd("loadfile") and #cmd("package") are not available, nor
are #cmd("os"), #cmd("io") and #cmd("debug"). #cmd("print") writes to MBT's
output. Each call into Lua runs under a limit of computation steps and of
256 MiB of memory\; there is no limit of wall-clock time, and programs
started with #cmd("ctx.exec") are not limited. The interface has a version, #cmd("mbt.api")\;
this chapter describes version 1.

== The Table mbt <ref-lua-mbt>

#deflist(width: 2.4in,
  [#cmd("mbt.hook(")#var("point")#cmd(", ")#var("fn")#cmd(")")], [runs
    #var("fn(ctx)") at #var("point"): #cmd("before_") or #cmd("after_")
    followed by #cmd("build"), #cmd("test"), #cmd("package"), #cmd("dist"),
    #cmd("deploy") or #cmd("release"), or #cmd("on_failure").],
  [#cmd("mbt.task{")...#cmd("}")], [declares a task (@ref-lua-task).],
  [#cmd("mbt.command(")#var("name")#cmd(", ")#var("fn")#cmd(")")], [makes
    #var("fn(ctx)") the command #cmd("mbt run") #var("name").],
  [#cmd("mbt.command{")...#cmd("}")], [the same as a table:
    #cmd("name"), #cmd("run"), and a #cmd("description") that
    #cmd("mbt run") lists beside the name. Only this form carries one.],
  [#cmd("mbt.api")], [the interface version, #cmd("1").],
  [#cmd("mbt.version")], [the version of MBT.],
)

#cmd("mbt.hook") with an unknown point is an error that lists the points\;
a command needs a name and a function, and a name may be used once.
#cmd("require") takes names in lower case, dots for directories
(#cmd("a.b") is #cmd("mbt/lua/a/b.lua")).

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

A task's #cmd("description") is shown by #cmd("mbt run"). Errors in a task
declaration: #cmd("before") naming no command of the list above, a path
outside the project, a name used twice or by a command. Under
#cmd("--dry-run") a task runs, but its outputs are left in place.

Tasks run before the #cmd("before_") hooks of a command, each at most once
per run. A task with inputs but no outputs, or an input pattern that
matches nothing, is an error. A task runs on the first run and, without
outputs, every time. Otherwise it is skipped while its outputs are newer
than every input and nothing
that made them changed: the task itself, the Lua files loaded, the
#cmd("[tools]"). Its outputs are removed before it runs and after it fails.

== The Table ctx <ref-lua-ctx>

#idx("ctx")
#table(columns: (2.3in, 1fr),
  [Member], [Meaning],
  [#cmd("ctx.project")], [a copy: #cmd("name"), #cmd("version"),
    #cmd("modules"), #cmd("tests"), #cmd("deploy_target"):
    #cmd("[deploy] target")\; in an #cmd("mbt.toml") with load modules and
    no target, #var("NAME")#cmd(".DEV.LINKLIB")\; #cmd("nil") when neither
    applies.],
  [#cmd("ctx.args")], [the arguments after #cmd("--") of #cmd("mbt run").],
  [#cmd("ctx.dry_run")], [#cmd("true") under #cmd("mbt run --dry-run") and
    #cmd("mbt deploy --dry-run").],
  [#cmd("ctx.kind")], [in #cmd("after_test"): #cmd("host"), #cmd("mvs") or
    #cmd("tso").],
  [#cmd("ctx.result")], [in #cmd("after_") hooks: what the command produced
    -- #cmd("build"): #cmd("modules"), #cmd("tests")\; #cmd("package"),
    #cmd("dist"): #cmd("artifacts")\; #cmd("release"): #cmd("version"),
    #cmd("tag"), #cmd("prerelease")\; #cmd("test"): #cmd("kind"),
    #cmd("passed")\; #cmd("deploy"): #cmd("library"), #cmd("modules"),
    #cmd("dry_run"). In #cmd("on_failure"): #cmd("command") and
    #cmd("error").],
  [#cmd("ctx.out"), #cmd("ctx.inputs")], [in a task: its outputs, and the
    files its inputs matched.],
  [#cmd("ctx.log(")#var("s")#cmd(")"), #cmd("ctx.warn(")#var("s")#cmd(")")],
    [a line of output, or a warning.],
  [#cmd("ctx.exec{")#var("argv")...#cmd(", env = {}, check = true}")],
    [runs a program without a shell. #cmd("check") defaults to
    #cmd("true"): a non-zero exit code raises an error\; with
    #cmd("check = false") it is returned. Returns the output and the exit
    code\; the output is standard output and standard error together. Under
    #cmd("mbt run") the program uses the terminal and the output returned
    is empty. The program runs in the project directory. #cmd("env") adds environment variables. Under
    #cmd("--dry-run") nothing runs. In a plugin, only the programs its
    #cmd("plugin.toml") names, by the base name of the first argument.],
  [#cmd("ctx.tool(")#var("name")#cmd(")")], [the path of a #cmd("[tools]")
    program, fetched and checked if need be.],
  [#cmd("ctx.fs.read(")#var("p")#cmd(")"), #cmd("write(")#var("p")#cmd(", ")#var("s")#cmd(")"),
    #cmd("exists"), #cmd("mkdir"), #cmd("remove"), #cmd("list")], [files,
    inside the project only: an absolute path, #cmd("..") or a link
    leading outside is an error. #cmd("list(")#var("pattern")#cmd(")")
    returns files and directories, #cmd("exists") a boolean.
    #cmd("remove") does not remove the project itself. Under
    #cmd("--dry-run"), #cmd("write"), #cmd("mkdir") and #cmd("remove") do
    nothing.],
  [#cmd("ctx.target()")], [the selected target, defaults applied, never a
    password: #cmd("name"), #cmd("hlq"), #cmd("volume"), #cmd("jobclass"),
    #cmd("msgclass"), #cmd("console"), #cmd("mvsmf = {url, user}"), and
    where the target has them #cmd("hercules = {url, user}"),
    #cmd("ssh = {host, user, port}"), #cmd("tn3270 = {host, port, tls,
    user}"). An error when the command has no target.],
  [#cmd("ctx.console(")#var("cmd")#cmd(")")], [an operator command through
    the target's console chain. Returns the reply lines and the channel
    that delivered it\; under #cmd("--dry-run"), an empty list.],
  [#cmd("ctx.mvs.request{ method, path, body, content_type }")], [a request
    to mvsMF in MBT's session. #cmd("method") defaults to #cmd("GET");
    #cmd("path") starts with #cmd("/") and lies below #cmd("/zosmf");
    #cmd("content_type") defaults to JSON when there is a body. Returns
    status and body. An HTTP error status is returned\; only a failed
    connection raises an error.],
  [#cmd("ctx.mvs.token()")], [the session token, for another program's
    environment.],
)

*Dry run.* Under #cmd("--dry-run"), #cmd("ctx.exec") runs nothing and
#cmd("ctx.mvs.request") sends only #cmd("GET") requests. #cmd("ctx.tool")
still fetches its tool, and #cmd("ctx.mvs.token") still logs on.

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
