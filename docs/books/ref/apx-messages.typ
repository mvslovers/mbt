#import "../bookmaster/bookmaster.typ": *

= Messages and Return Codes <apx-messages>

== Return Codes <apx-messages-rc>

#idx("return code")
#table(columns: (0.6in, 1.4in, 1fr),
  [Code], [Kind], [When],
  [0], [success], [],
  [1], [build or test], [a compile, assembly or link failed\; LIBC/370 is
    older than #cmd("[toolchain]") asks\; packing for a deploy failed\; a
    test failed on the workstation, on MVS or under TSO\; an extension
    failed for a reason other than its configuration.],
  [2], [configuration], [an error in #cmd("mbt.toml") or
    #cmd("targets.toml"), a wrong argument, an unknown #cmd("--module") or build name,
    two sources of one name, a fixture that is wrong, a plugin for another
    interface, a failed #cmd("mbt doctor"), a launcher pin that is no
    version, any failed #cmd("mbt release") or #cmd("prerelease").],
  [3], [dependencies], [a dependency, tool or plugin could not be resolved,
    downloaded or checked\; dependencies not staged\; a #cmd("[tools]")
    table that is wrong\; a pinned MBT that cannot be fetched.],
  [4], [MVS], [mvsMF could not be reached, a job failed or was not
    finished in time, a deploy failed on MVS or stopped at the space check,
    a test job that could not run or whose output could not be read.],
  [5], [data set], [#cmd("mbt test --mvs"): a fixture could not be loaded.
    Ahead of 1.],
  [99], [internal], [an error of no known kind\; #cmd("mbt migrate") found
    a difference it did not intend.],
  [130], [interrupted], [Ctrl-C or a termination signal, after logging off
    mvsMF.],
)

== Messages <apx-messages-msgs>

MBT writes its messages with the prefix #cmd("[mbt]"): progress lines,
#cmd("WARNING:") for what does not stop it, and #cmd("ERROR:") for what
does, followed by indented lines with the details and, where there is one,
the way out.

#tab(caption: [Some messages and what to do])[
  #table(columns: (2.5in, 1fr),
    [Message], [Do],
    [#cmd("dependencies not staged: ... -- run 'mbt deps'")], [run
      #cmd("mbt deps").],
    [#cmd("locked '...' no longer fits '...' -- run 'mbt deps --update'")],
      [run #cmd("mbt deps --update") if the new range is meant.],
    [#cmd("GitHub API rate limit reached")], [set #cmd("GITHUB_TOKEN").],
    [#cmd("ERROR: writable data in ... (rent = true): ")#var("file")#cmd(":")#var("line")],
      [make the data #cmd("const"), move it, or declare
      #cmd("rent = false"). The same text with #cmd("(ac = 1 ...)") or
      #cmd("(RENT by ld370's default)") is a warning.],
    [#cmd("mbt.toml: unknown key '...' in [...]")], [the key is misspelt or
      not one MBT knows: correct it, or take it out
      (@ref-toml).],
    [#cmd("mbt.toml: [[...]] is the v2 spelling -- write one table per entry, [....NAME]")],
      [write one table per entry, for a library
      #cmd("[distribution.library.DIR]") (@ref-toml-distlib).],
    [#cmd("mbt.toml: [[...]] is an array of tables -- write one table, [...]")],
      [the table is written with double brackets, or as an inline array
      (#cmd("x = [...]"), which gives the same message): write it with single
      brackets, as the message shows.],
    [#cmd("mbt.toml: ... is a string, not a table -- write [...]")], [the key
      is a value where MBT expects a table (the type is a string, an integer,
      a float or a boolean, or, for a date-time, "a value of another type"):
      write it as a table, as the message shows.],
    [#cmd("mbt.toml: [...] holds one table per entry, [....NAME]; '...' is ...")],
      [an entry of #cmd("[module]") or #cmd("[test]") is a value, not a
      table: write #cmd("[module.NAME]") or #cmd("[test.NAME]").],
    [#cmd("mbt.toml: [distribution.library] holds one table per directory, [distribution.library.DIR]; '...' is ...")],
      [a directory under #cmd("[distribution.library]") is a value, not a
      table: write #cmd("[distribution.library.DIR]")
      (@ref-toml-distlib).],
    [#cmd("mbt.toml: [tso.NAME] must be a table")], [an entry of
      #cmd("[tso]") is a value, not a table: write #cmd("[tso.NAME]").],
    [#cmd("mbt.toml: [tso.NAME] fixtures must be a table: { KEY = [\"file\", ...] }")],
      [write #cmd("fixtures") as an inline table that maps each key to a
      list of files.],
    [#cmd("mbt.toml: [dependencies] \"...\" is ..., not a version range -- write \"...\" = \"<range>\"")],
      [give the dependency as a version range string
      (@ref-toml-deps).],
    [#cmd("test NAME: parm is N chars; M is the most the batch EXEC card holds (JCL ends at column 71)")],
      [#cmd("mbt test --mvs") (exit 2, before anything is uploaded): the
      #cmd("parm") does not fit the batch step even on a continuation card
      (49 characters). Shorten it.],
    [#cmd("test NAME: parm is N chars; M is the most the TSO CALL holds (TSO reads columns 1-72)")],
      [#cmd("mbt test --mvs") (exit 2, before anything is uploaded): the
      TSO argument does not fit the CALL command (69 characters). Shorten
      it, or give the batch step its own value with
      #cmd("{ batch = ..., tso = ... }").],
    [#cmd("mbt.toml: test.NAME.parm is ... -- write a string, or a table [test.NAME.parm] with batch and tso")],
      [write #cmd("parm") as one string, or as a table with the keys
      #cmd("batch") and #cmd("tso").],
    [#cmd("mbt.toml: [smp] fmid is explicit, so delete must be too: ...")],
      [name the FMID this release replaces, or write #cmd("delete = []")
      for a product's first level.],
    [#cmd("version ... is a prerelease: the derived FMID ... is the release's, ...")],
      [set #cmd("[smp] fmid") to a throwaway id, and #cmd("delete")
      (@ref-smp-prerelease).],
    [#cmd("sources sharing a name: ... -- rename one")], [rename one of the
      two files.],
    [#cmd("... defines N targets and none is default")], [give
      #cmd("--target"), or #cmd("default = true") to one target.],
    [#cmd("password: keychain ... is empty")], [store the password again,
      in an interactive terminal.],
    [#cmd("... has about N of M tracks free")], [compress the library, or
      #cmd("--reallocate") while nothing holds it, or #cmd("--ignore-space").],
    [#cmd("deploy job ... ran out of space (SD37 in step MERGE, ...)")],
      [compress the library while nothing holds it, or #cmd("--reallocate");
      then deploy again. The library may hold some of the new members until
      a deploy completes.],
    [#cmd("deploy failed: --reallocate: cannot delete ..., most likely a
      running server holds it -- stop it first")], [stop the server
      first.],
    [#cmd("--reallocate: ... has no room for N+M tracks ... -- nothing was
      deleted")], [free space on the volume, or compress the library while
      nothing holds it.],
    [#cmd("deploy job ... -- ... does not exist: the job did not allocate
      it")], [read the job's messages, then deploy again: a plain
      #cmd("mbt deploy") allocates the library.],
    [#cmd("the deploy job ended clean, but ... lacks ...")], [read
      #cmd("build/deploy.spool").],
    [#cmd("cannot release ...: the tree is at ..., expected ...-dev")],
      [release the version the tree is at.],
    [#cmd("tag v... already exists locally")], [remove the tag an aborted
      run left, as the message shows.],
  )
] <apx-messages-tab>
