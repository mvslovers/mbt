#import "../bookmaster/bookmaster.typ": *

= mbt.lock and deps.local.toml <ref-lock>

== mbt.lock <ref-lock-lock>

#idx("mbt.lock")
#cmd("mbt.lock") is a JSON object in the project directory, written by
#cmd("mbt deps") only when it changes, with its keys sorted. It is
committed. A project without dependencies, tools and plugins has none.

```
{
  "mvslovers/crypto370": {
    "sha256": "f5686240265baf69773e887bb7abd2c3f8da84d77bd377df38f95f4aa879fb6f",
    "version": "1.0.1"
  }
}
```

#table(columns: (2.4in, 1fr),
  [Key], [Entry],
  [#var("owner")#cmd("/")#var("repo")], [a dependency: the version chosen
    and the SHA-256 checksum of
    #var("repo")#cmd("-")#var("version")#cmd("-lib.tar.gz").],
  [#cmd("tool:")#var("name")#cmd("@")#var("os")#cmd("-")#var("arch")],
    [a tool of #cmd("[tools]"), one entry per platform for which GitHub
    publishes a checksum of the asset: the checksum of the asset, not of
    the program in it.],
  [#cmd("plugin:")#var("owner")#cmd("/")#var("repo")], [a plugin: the
    version and the checksum of
    #var("repo")#cmd("-")#var("version")#cmd("-plugin.tar.gz").],
)

Each entry has the two members #cmd("version") and #cmd("sha256"). The
entries of tools and plugins are also written when Lua first asks for a
tool (#cmd("ctx.tool")), not only by #cmd("mbt deps").
#cmd("mbt deps") fails when a locked version is outside its range or a
download has another checksum, and #cmd("mbt deps --update") rewrites the
entries. An entry #cmd("tool:")#var("name") without a platform, written by
an earlier MBT, is replaced.

== .mbt/deps.local.toml <ref-lock-local>

#idx("deps.local.toml")
#cmd(".mbt/deps.local.toml") is not committed. Its table #cmd("[override]")
points dependencies and plugins at working copies:

```
[override]
"mvslovers/crypto370" = { path = "../crypto370" }
```

#cmd("path") is a directory with an #cmd("mbt.toml") or #cmd("project.toml")
that has a #cmd("[lib]"). #cmd("mbt deps") copies its
#cmd("build/")#var("name")#cmd(".a") and the files of its
#cmd("[lib] headers") into #cmd(".mbt/deps/")#var("repo")#cmd("/"), and
fails with #cmd("not built -- run 'mbt build' in") #var("path") when the
archive is missing. #cmd("[lib] name") defaults to the project name\;
headers are copied without their directories, and a missing one is
skipped. #cmd("mbt deps") and #cmd("mbt deps --update") leave the dependency's
entry in #cmd("mbt.lock") as it is.
For a plugin, #cmd("path") is the plugin's directory, with
#cmd("plugin.toml") and #cmd("init.lua").
