#import "bookmaster/bookmaster.typ": *

#show: book.with(
  title: "MBT for MVS 3.8j",
  subtitle: "Reference",
  short-title: "MBT Reference",
  number: "ML02-0002-0",
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

#part("index.html", title: [MBT Reference])[
#titlepage()
#contents()
#figures()

#heading(numbering: none)[About This Book] <about>

This book describes MBT (mbt), the MVS build tool, in full: the
#cmd("mbt") command and its subcommands, the project file #cmd("mbt.toml"),
the files MBT keeps for targets and dependencies, its Lua API, and its
messages. How to use them together is the subject of the companion volume,
the _MBT User's Guide_.

== How This Book Is Organized

#deflist(width: 1.35in,
  [Chapter 1], [“The mbt Command”.],
  [Chapter 2], [“mbt.toml”.],
  [Chapter 3], [“targets.toml”.],
  [Chapter 4], [“mbt.lock and deps.local.toml”.],
  [Chapter 5], [“The Lua API”.],
  [Chapter 6], [“The TSO Test API”.],
  [Chapter 7], [“Environment Variables”.],
  [Chapter 8], [“Files and Directories”.],
  [Chapter 9], [“The SMP Package”.],
  [Appendix A], [“Messages and Return Codes”.],
)

== Related Publications

#deflist(width: 1.35in,
  [ML02-0001], [_MBT User's Guide_: building, testing, deploying, packaging
    and releasing an MVS project with MBT.],
  [ML01-0001], [_CC/370 User's Guide_: the cross-toolchain MBT drives.],
)

#mainmatter()
]
#set page(numbering: "1")

#part("ref-cmd.html", include "ref/ref-cmd.typ")
#part("ref-toml.html", include "ref/ref-toml.typ")
#part("ref-targets.html", include "ref/ref-targets.typ")
#part("ref-lock.html", include "ref/ref-lock.typ")
#part("ref-lua.html", include "ref/ref-lua.typ")
#part("ref-tso.html", include "ref/ref-tso.typ")
#part("ref-env.html", include "ref/ref-env.typ")
#part("ref-files.html", include "ref/ref-files.typ")
#part("ref-smp.html", include "ref/ref-smp.typ")

#show: appendices
#part("apx-messages.html", include "ref/apx-messages.typ")

#part("index-terms.html", title: [Index])[
#heading(numbering: none)[Index]
#make-index()
]
