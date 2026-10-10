#import "../bookmaster/bookmaster.typ": *

= Deploying <ug-deploy>

#idx("mbt deploy")
To try a program on MVS, its load modules have to be in a load library
there. #cmd("mbt deploy") puts them into the project's development library
on a target (@ug-targets), replacing the members of an earlier deploy,
also while a running server holds the library. This chapter describes
which library that is and why it is a library of its own, what a deploy
does, and how to keep the library from filling up.

== The Development Library <ug-deploy-library>

#idx("development library")#idx("DEV.LINKLIB")
A product installed with SMP (@ug-package) lives in its target library,
such as #cmd("SUMS.LINKLIB"), and that library belongs to SMP: SMP records
which level of each module is in it. A deploy into it would leave SMP
describing a level that is not on disk, without any way to notice. So a
project deploys into a library of its own, by default
#var("NAME")#cmd(".DEV.LINKLIB"):

#tab(caption: [Three libraries, three owners])[
  #table(columns: (1.7in, 0.9in, 1fr),
    [Library], [Owner], [Filled by],
    [#var("NAME")#cmd(".LINKLIB")], [SMP], [the installation job, and only
      that],
    [#var("NAME")#cmd(".DEV.LINKLIB")], [development], [#cmd("mbt deploy")],
    [#var("HLQ")#cmd(".")#var("NAME")#cmd(".")#var("VRM")#cmd(".TESTLIB")],
      [tests], [#cmd("mbt test --mvs") (@ug-test-mvs)\; #cmd("[deploy]
      test_target") names another],
  )
] <ug-deploy-libs>

#cmd("[deploy] target") names another library. A project whose name is
longer than eight characters, and so is no qualifier, must name one: MBT
does not shorten the name of a library a started task has to name in its
JCL. #cmd("--linklib") #var("DSN") deploys into another library for one
run.

The name carries no user's prefix on purpose. A development level is used
by naming the library in the #cmd("STEPLIB") of a job or started task, and
a procedure can only name a fixed data set.

*Put the development library first in the #cmd("STEPLIB")*, ahead of the
SMP library. The first library that has a member wins, so a deploy
overrides the installed level, and removing the DD statement returns to
it. #idx("APF")*Every library in an authorized #cmd("STEPLIB") must be
APF-authorized*, the development library included: a single library that is
not makes the whole task unauthorized, and the failure that follows does not
name the cause. MVS 3.8j reads the APF list at IPL, so add the development
library and the SMP library together.

A development library has no place on a production system. Sitting first
in the #cmd("STEPLIB"), it runs a development build silently.

== What a Deploy Does <ug-deploy-steps>

#idx("mbt deploy", "steps")
#cmd("mbt deploy") deploys the load modules the last build left. Build
first. Then:

+ *Pack.* The modules go into one TSO TRANSMIT file of a load library.
+ *Check the room.* If the library exists, MBT reads how many tracks are
  free and estimates, generously, how many the modules need. When they do
  not fit, it stops before anything is copied (@ug-deploy-space). When it
  cannot measure the space, for a device or space unit it does not know,
  it warns and goes on without the check.
+ *Upload.* The TRANSMIT file goes to MVS through mvsMF, into a staging data
  set #var("hlq")#cmd(".MBT.XMIT.IN").
+ *Receive and copy.* One job receives the file into a staging library,
  #var("hlq")#cmd(".")#var("PROJECT")#cmd(".MBTDPLY"), and copies its
  members with #cmd("IEBCOPY") into the development library, replacing
  members of the same name. The job opens the library #cmd("DISP=SHR"), so
  it works while a running server holds the library.
+ *Verify.* MBT lists the members of the library and checks that every
  deployed module is there. A job that ended cleanly but left a module out
  is an error.
+ *Report.* It shows how full the library is, with a warning from 80 per
  cent on, and removes the staging data sets.

@ug-deploy-log shows the first deploy of a module into a library that did
not exist yet. The project is a library project whose test module was
deployed\; its name, cut to eight characters, names the staging library.

#fig(caption: [A first deploy])[
  #screen(raw(read("../ex/ug-deploy/deploy.txt")))
] <ug-deploy-log>

A second deploy of the same module shows the same lines, without the
allocation, and ends at 46 per cent of the 30 tracks: the old member's
space is still there (@ug-deploy-space).

#cmd("--module") #var("M") deploys only the named module (repeat it for
more). #cmd("--dry-run") packs and stops: it needs no password and logs on
to nothing.

#idx("deploy", "first")
*A library that does not exist yet* is allocated by the deploy job, with
room for several deploys, on the target's volume if the target names one
(#cmd("volume"), @ug-targets-file). MBT then checks that it landed on that
volume, since an APF entry names a data set together with its volume.
Without a volume, the system chooses one, and there is nothing to check.

*A running server keeps the modules it loaded.* A new member in the library
reaches a program the next time it is loaded. A server that loaded its
modules once at start-up runs the new level only after a restart.

== When the Library Fills Up <ug-deploy-space>

#idx("deploy", "space")#idx("compress")
Replacing a member does not free the space of the old one: in a
partitioned data set, it stays dead until the library is compressed. Each
deploy therefore uses more of the library, and the report after each
deploy shows how much. When the next deploy would not fit, MBT stops with
the numbers and the three ways out:

- *Compress the library* while nothing holds it, for example while the
  server is stopped, or in the server's start-up procedure before the
  server itself starts.
- *#cmd("mbt deploy --reallocate")* deletes the library and allocates it
  again, larger, on the same volume, then deploys. The new library is never
  smaller than the old one. It works only while nothing holds the library:
  otherwise the delete fails, and MBT stops with "most likely a running
  server holds it -- stop it first".
- *#cmd("mbt deploy --ignore-space")* copies all the same, for a library
  whose secondary extents will take it.

The free entries of the library's directory cannot be measured from the
workstation\; a library with many members can run out of directory blocks
before it runs out of tracks.

#note[*Libraries from earlier deploys.* Earlier versions of MBT deleted the
library on every deploy and received it afresh, sized to its content, so
such a library is full or nearly so. Its first deploy now needs #cmd("--reallocate"), once, with the
server stopped.]

== When a Deploy Fails <ug-deploy-fail>

#idx("mbt deploy", "errors")
A deploy that fails on MVS ends with return code 4 and says why, also when
it stopped for lack of room before anything was uploaded. An unknown
#cmd("--module") or a project without built modules ends with return code
2, before MVS is reached.

When the job ran but did not deliver, because a module is missing from the
library afterwards or the library landed on another volume, the job's
output is in #cmd("build/deploy.spool"), and the uploaded TRANSMIT file is
left in #var("hlq")#cmd(".MBT.XMIT.IN"), so the RECEIVE can be repeated.

*A failed deploy is not all or nothing.* #cmd("IEBCOPY") replaces one
member after the other, so a job that fails part-way can leave some of the
new modules in the library beside old ones. MBT says so\; look at the
spool before you use the library.
