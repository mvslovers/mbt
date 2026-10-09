#import "../bookmaster/bookmaster.typ": *

= Installing MBT <ug-install>

#idx("MBT", "installing")
MBT is a single program without dependencies. It is installed once on the
workstation, and every project there uses it. Besides MBT, a project needs
the CC/370 cross-toolchain with LIBC/370, which MBT runs but does not
install. This chapter shows how to install MBT, what else it needs, and how
to check the result.

== What You Need <ug-install-need>

- *A workstation* running macOS or Linux on an arm64 or amd64 processor, or
  Windows on amd64.
- *CC/370 with LIBC/370*, installed and on the #cmd("PATH"): the commands
  #cmd("cc370"), #cmd("as370"), #cmd("ld370"), #cmd("ar370") and
  #cmd("xmit370"), and the C library in the directory tree of the toolchain.
  Their installation is described in the _CC/370 User's Guide_, Chapter 2.
  MBT uses whatever toolchain it finds on the #cmd("PATH").
- *A C compiler for the workstation*, such as #cmd("cc") or #cmd("gcc"), if
  you want to run the tests of a project on the workstation (@ug-test).
- *Access to GitHub*, when a project has dependencies or pins a version of
  MBT: MBT fetches both from GitHub releases.
- *An MVS 3.8j system with mvsMF*, only to deploy and to test on MVS. The
  build itself needs no MVS.

== Installing a Release <ug-install-release>

#idx("MBT", "release")
MBT is published as a release of the GitHub repository
#cmd("mvslovers/mbt"), with one archive per platform and a list of their
SHA-256 checksums:

#tab(caption: [The archives of an MBT release])[
  #table(columns: (2.6in, 1fr),
    [Archive], [Platform],
    [#cmd("mbt-")#var("version")#cmd("-darwin-arm64.tar.gz")], [macOS on
      Apple silicon],
    [#cmd("mbt-")#var("version")#cmd("-darwin-amd64.tar.gz")], [macOS on
      Intel],
    [#cmd("mbt-")#var("version")#cmd("-linux-amd64.tar.gz")], [Linux on x86-64],
    [#cmd("mbt-")#var("version")#cmd("-linux-arm64.tar.gz")], [Linux on ARM],
    [#cmd("mbt-")#var("version")#cmd("-windows-amd64.zip")], [Windows on x86-64],
    [#cmd("mbt-")#var("version")#cmd("-sha256.txt")], [the checksums of the
      archives],
  )
] <ug-install-assets>

Until MBT 3.0.0 is released, the release to install is the prerelease
#cmd("v3.0.0-dev"). Download the archive for your platform and the
checksums, check the archive, unpack it, and put #cmd("mbt") into a
directory on your #cmd("PATH"). With the GitHub command #cmd("gh"), on a Mac
with Apple silicon:

```
gh release download v3.0.0-dev -R mvslovers/mbt \
   -p 'mbt-3.0.0-dev-darwin-arm64.tar.gz' -p 'mbt-3.0.0-dev-sha256.txt'
shasum -a 256 -c --ignore-missing mbt-3.0.0-dev-sha256.txt
tar -xzf mbt-3.0.0-dev-darwin-arm64.tar.gz
install mbt-3.0.0-dev-darwin-arm64/mbt ~/.local/bin/mbt
```

The archives can also be downloaded from the release page in a browser.
Then check that the shell finds the program:

```
mbt version
```

You install MBT once and never again for a new project. A project that asks
for another version of MBT gets it without your help (@ug-intro-version):
the installed #cmd("mbt") fetches it into #cmd("~/.mbt/versions/") and runs
it.

== The Directory ~/.mbt <ug-install-home>

#idx("~/.mbt")#idx("MBT_HOME")
MBT keeps what belongs to the workstation rather than to a project in the
directory #cmd(".mbt") in your home directory:

#deflist(width: 1.6in,
  [#cmd("targets.toml")], [the MVS systems you work with (@ug-targets).],
  [#cmd("versions/")], [the versions of MBT that projects asked for.],
  [#cmd("init.lua")], [your own extensions, loaded for every project
    (@ug-extend).],
)

The environment variable #cmd("MBT_HOME") names another directory instead.
What MBT keeps for one project, such as the libraries it depends on, is in
the directory #cmd(".mbt") of that project, which is not committed
(@ug-first-files).

== A Token for GitHub <ug-install-token>

#idx("GITHUB_TOKEN")#idx("GitHub", "rate limit")
MBT asks the GitHub API when it fetches dependencies, tools, plugins or
another version of itself. Without a token, GitHub allows 60 requests an
hour, shared by everything on the machine, and with several projects that
limit is soon reached: MBT then reports #cmd("GitHub API rate limit
reached"). Any token lifts the limit, and it needs no permissions. With the
#cmd("gh") command:

```
export GITHUB_TOKEN=$(gh auth token)
```

Put the line into the start-up file of your shell.

== Checking the Installation <ug-install-check>

#idx("mbt doctor")
#cmd("mbt doctor") checks what MBT needs. Run in a project directory with
#cmd("--offline"), it checks the workstation alone: the commands of CC/370,
the toolchain's directory tree with LIBC/370, their versions against what
the project asks for, and the project file. Without #cmd("--offline") it
also logs on to the project's MVS target, read-only. @ug-install-doctor
shows the check in the project of @ug-first.

#fig(caption: [Checking the workstation])[
  #screen(raw(read("../ex/ug-first/doctor.txt")))
] <ug-install-doctor>

Each line names what was found and where. A missing command, a toolchain
older than the project asks for, or an error in #cmd("mbt.toml") is
reported, and #cmd("mbt doctor") then ends with a return code other than 0.
