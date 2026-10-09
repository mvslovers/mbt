#import "bookmaster/bookmaster.typ": *

#show: book.with(
  title: "MBT for MVS 3.8j",
  subtitle: "User's Guide",
  short-title: "MBT User's Guide",
  product: "MBT",
  number: "ML02-0001-0",
  date: "October 2026",
  authors: ("Mike Großmann",),
  edition: [
    #text(font: head-font, weight: "bold", size: 11pt)[First Edition (October 2026)]

    This edition applies to Version 3 Release 0 of MBT, the MVS build tool
    (mbt 3.0.0), and to all subsequent releases and modifications until
    otherwise indicated in new editions.

    *Draft.* MBT 3 is still in development, and so is this book: chapters
    still to be written say so.

    Comments on this book may be addressed to the issue tracker of the
    mvslovers/mbt repository on GitHub.

    © Copyright Mike Großmann 2026. All rights reserved.
  ],
)

#part("index.html", title: [MBT User's Guide])[
#titlepage()
#contents()
#figures()

#heading(numbering: none)[About This Book] <about>

This book shows how to build programs for MVS 3.8j with MBT (mbt), the MVS
build tool: how a project is described, how it is built and tested on the
workstation with the CC/370 cross-toolchain, how its load modules reach MVS,
and how a release is packaged for installation with SMP. Every command and
key is described in full in the companion volume, the _MBT Reference_.

== How This Book Is Organized

#deflist(width: 1.35in,
  [Chapter 1], [“Introducing MBT”.],
  [Chapter 2], [“Installing MBT”.],
  [Chapter 3], [“A First Project”.],
  [Chapter 4], [“Building”.],
  [Chapter 5], [“Dependencies”.],
  [Chapter 6], [“MVS Systems: Targets”.],
  [Chapter 7], [“Deploying”.],
  [Chapter 8], [“Testing”.],
  [Chapter 9], [“Packaging and SMP Installation”.],
  [Chapter 10], [“Releasing”.],
  [Chapter 11], [“Extending MBT”.],
  [Appendix A], [“Migrating from MBT 2”.],
  [Appendix B], [“Glossary”.],
)

== Related Publications

#deflist(width: 1.35in,
  [ML02-0002], [_MBT Reference_: every command, every key of
    #cmd("mbt.toml"), the files MBT reads and writes, and its Lua API.],
  [ML01-0001], [_CC/370 User's Guide_: the cross-toolchain MBT drives.],
)

#mainmatter()
]
#set page(numbering: "1")

#part("ug-intro.html", include "guide/ug-intro.typ")
#part("ug-install.html", include "guide/ug-install.typ")
#part("ug-first.html", include "guide/ug-first.typ")
#part("ug-build.html", include "guide/ug-build.typ")
#part("ug-deps.html", include "guide/ug-deps.typ")
#part("ug-targets.html", include "guide/ug-targets.typ")
#part("ug-deploy.html", include "guide/ug-deploy.typ")
#part("ug-test.html", include "guide/ug-test.typ")
#part("ug-package.html", include "guide/ug-package.typ")
#part("ug-release.html", include "guide/ug-release.typ")
#part("ug-extend.html", include "guide/ug-extend.typ")

#show: appendices
#part("ug-migrate.html", include "guide/ug-migrate.typ")
#part("ug-glossary.html", include "guide/ug-glossary.typ")

#part("index-terms.html", title: [Index])[
#heading(numbering: none)[Index]
#make-index()
]
