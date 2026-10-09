#import "../bookmaster/bookmaster.typ": *

= Releasing <ug-release>

#idx("release")
A release of a project is a Git tag, #cmd("v")#var("version"), and the files
that the release workflow builds from it and publishes on the project's
GitHub release page. MBT sets the version, commits and tags\; the workflow
builds and publishes. This chapter describes both, the prereleases that
publish a version still in development, and the workflows that build every
change.

== Versions <ug-release-versions>

#idx("version")
The version is #cmd("[project] version") in #cmd("mbt.toml") and nowhere
else. Between releases it ends in #cmd("-dev"): #cmd("1.4.0-dev") is the
version being worked on that will become 1.4.0. A release removes the
#cmd("-dev"), and the next development version follows it.

#cmd("[release] version_files") names further files in which the version is
written, such as a header with the version as a string\; MBT changes them
together with #cmd("mbt.toml"). A program can also take its version from
#cmd("<buildstamp.h>") (@ug-build-stamp), which needs no such file.

== Releasing: mbt release <ug-release-release>

#idx("mbt release")
On a working tree at #cmd("1.4.0-dev"), with nothing uncommitted:

```
mbt release 1.4.0
```

#cmd("mbt release") does this, and stops at the first step that fails:

+ checks that the tree is clean and at a prerelease of 1.4.0, such as
  #cmd("1.4.0-dev") or #cmd("1.4.0-rc1"), that the tag #cmd("v1.4.0")
  exists neither in the repository nor on #cmd("origin"), and, for a patch
  release with an SMP package, that it has an explicit FMID
  (@ug-package-fmid)\;
+ sets the version to #cmd("1.4.0"), commits it as #cmd("release: v1.4.0"),
  tags the commit #cmd("v1.4.0"), and pushes the commit and the tag\;
+ sets the version to the next development version, #cmd("1.4.1-dev")
  unless #cmd("--next") names another, commits it as
  #cmd("chore: bump to 1.4.1-dev") and pushes it.

Pushing the tag starts the release workflow (@ug-release-ci). Nothing is
undone when a step fails: a failure after the tag leaves the tag, and the
next run says so and how to remove it. Every refusal and every failed Git
step ends with return code 2. Use
#cmd("--next 1.5.0-dev") when the next release is planned as a minor, so
the tree does not carry a patch version that would need an FMID of its
own.

#idx("release", "repository")
In a repository with more than one project, only the project whose project
file is at the root may tag: the tags #cmd("v")#var("version") of the
repository are its tags. A repository with one project file, not at the
root, lets that one tag\; one with several and none at the root lets none.
This holds for #cmd("mbt prerelease") as well.

== Prereleases: mbt prerelease <ug-release-pre>

#idx("mbt prerelease")#idx("prerelease")
#cmd("mbt prerelease") publishes the version in development without
releasing it. On a clean tree, it tags the current commit with the current
version, such as #cmd("v1.4.0-dev"), and pushes the tag. Run again later, it moves the same
tag to the newer commit, and the release workflow publishes the
prerelease again. Nothing in the tree changes.

Another project can then depend on that prerelease with a range that names
it, such as #cmd(">=1.4.0-dev") (@ug-deps-ranges). Because the tag moves,
the archive behind it changes too, and the dependent project's
#cmd("mbt deps") refuses the new one until #cmd("mbt deps --update")
accepts it (@ug-deps-lock).

== The Workflows <ug-release-ci>

#idx("GitHub Actions")#idx("build3.yml")#idx("release3.yml")
MBT provides two reusable GitHub Actions workflows. A project's own
workflow files only call them:

```
# .github/workflows/build.yml
name: Build
on:
  pull_request:
  push:
    branches: [main]
jobs:
  build:
    uses: mvslovers/mbt/.github/workflows/build3.yml@main
    with:
      host_tests: true          # also run mbt test
```

```
# .github/workflows/release.yml
name: Release
on:
  push:
    tags: ["v*"]
jobs:
  release:
    uses: mvslovers/mbt/.github/workflows/release3.yml@main
    permissions:
      contents: write
```

Both install MBT, which then switches to the version
#cmd("[toolchain] mbt") pins.

#deflist(width: 1.2in,
  [#cmd("build3.yml")], [builds every pull request and every change of the
    main branch, against the current development state of CC/370 and
    LIBC/370, so that a change there that breaks the project shows early.
    With #cmd("host_tests: true") it also runs #cmd("mbt test").],
  [#cmd("release3.yml")], [builds a tag with the CC/370 and LIBC/370 that
    #cmd("[toolchain]") names, checks that the tag is #cmd("v") followed by
    #cmd("[project] version"), runs #cmd("mbt package") and publishes the
    files of #cmd("dist/") as the release. A tag with a hyphen, such as
    #cmd("-dev") or #cmd("-rc1"), becomes a prerelease, and a tag pushed
    again replaces its release. A #cmd("[toolchain]") entry that is a
    version means the tag #cmd("v")#var("version") of that project, any
    other string a Git reference\; a missing entry means the main branch,
    so a project without #cmd("[toolchain]") releases against the
    development state.],
)

Once MBT 3.0.0 is released, pin #cmd("uses:") to its tag instead of
#cmd("@main"), so that workflow and tool do not drift apart.


A workflow that deploys or tests on MVS describes its target in
environment variables from the repository's secrets (@ug-targets-ci).

== After the Release <ug-release-after>

The workflow publishes the files with notes that GitHub generates from the
pull requests and commits since the last tag. Before you announce the
release, rework the page for someone who does not know the project yet:
what the release is, what a user notices, how to install it, and then the
section of #cmd("CHANGELOG.md") for the version, which the workflow does
not read.
