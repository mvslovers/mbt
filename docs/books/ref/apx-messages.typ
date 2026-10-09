#import "../bookmaster/bookmaster.typ": *

= Messages and Return Codes <apx-messages>

== Return Codes <apx-messages-rc>

#idx("return code")
#table(columns: (0.6in, 1.4in, 1fr),
  [Code], [Kind], [When],
  [0], [success], [],
  [1], [build or test], [a compile, assembly or link failed\; a test
    failed on the workstation or on MVS\; an extension failed for a reason
    other than its configuration.],
  [2], [configuration], [an error in #cmd("mbt.toml") or
    #cmd("targets.toml"), a wrong argument, an unknown module, nothing built
    to deploy, two sources of one name, a plugin for another interface, a
    refused release.],
  [3], [dependencies], [a dependency, tool or plugin could not be resolved,
    downloaded or checked\; dependencies not staged.],
  [4], [MVS], [mvsMF could not be reached, a job failed or was not
    finished in time, any failed deploy, the space check, a test job that
    could not run or whose output could not be read.],
  [5], [data set], [#cmd("mbt test --mvs"): a fixture could not be loaded.
    Ahead of 1.],
  [99], [internal], [an error of no known kind.],
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
    [#cmd("writable data in ... (rent = true)")], [make the data
      #cmd("const"), move it, or declare #cmd("rent = false").],
    [#cmd("sources sharing a name: ... -- rename one")], [rename one of the
      two files.],
    [#cmd("... defines N targets and none is default")], [give
      #cmd("--target"), or #cmd("default = true") to one target.],
    [#cmd("password: keychain ... is empty")], [store the password again,
      in an interactive terminal.],
    [#cmd("... has about N of M tracks free")], [compress the library, or
      #cmd("--reallocate") while nothing holds it, or #cmd("--ignore-space").],
    [#cmd("--reallocate: cannot delete ..., most likely a running server
      holds it")], [stop the server first.],
    [#cmd("the deploy job ended clean, but ... lacks ...")], [read
      #cmd("build/deploy.spool").],
    [#cmd("cannot release ...: the tree is at ..., expected ...-dev")],
      [release the version the tree is at.],
    [#cmd("tag v... already exists locally")], [remove the tag an aborted
      run left, as the message shows.],
  )
] <apx-messages-tab>
