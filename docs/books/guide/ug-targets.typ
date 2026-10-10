#import "../bookmaster/bookmaster.typ": *

= MVS Systems: Targets <ug-targets>

#idx("target")
Deploying and testing on MVS need a system to go to. MBT calls such a
system a _target_ and keeps the targets of a workstation in one file,
#cmd("~/.mbt/targets.toml"), apart from every project. A project therefore
does not say where it is deployed\; the workstation does, and the same
project can go to a test system today and to another tomorrow. This chapter
describes the file, where passwords come from, how a target is chosen, and
how to check that a system can be reached.

== What a Target Is <ug-targets-what>

A target is the set of ways to reach one MVS system:

#deflist(width: 1.3in,
  [mvsMF], [the REST interface MBT deploys and tests through: it uploads
    files, submits jobs and reads their output. Every target has it.],
  [Hercules], [the web interface of the Hercules emulator the system runs
    on. MBT can send operator commands through it when mvsMF cannot
    (@ug-targets-console). Optional.],
  [TN3270], [a terminal session, for the interactive TSO tests
    (@ug-test). Optional.],
  [SSH], [a shell on the machine that runs Hercules. Optional.],
)

Besides these, a target names the high-level qualifier of the data sets
MBT allocates there, and the volume and job classes it uses.

== The File targets.toml <ug-targets-file>

#idx("targets.toml")
Each target is a table #cmd("[target.")#var("name")#cmd("]"), with a table
for each way to reach it:

```
[target.lab]
default  = true            # the target used without --target
hlq      = "IBMUSER"       # default: the mvsMF user
volume   = "PUB001"        # where RECEIVE allocates
jobclass = "A"             # default A
msgclass = "H"             # default H

[target.lab.mvsmf]         # required
url      = "http://lab:8080"
user     = "IBMUSER"
password = { keychain = "mbt/lab" }

[target.lab.hercules]
url      = "http://lab:8181"

[target.lab.tn3270]
host     = "lab"
user     = "IBMUSER"
password = { keychain = "mbt/lab" }

[target.lab.ssh]
host     = "lab"
user     = "hercules"
```

#cmd("volume") names the volume on which MBT allocates the data sets it
receives. Without it, the system chooses. Name one on a system that does
not choose a suitable volume by itself, such as MVS/CE. When MBT allocates
a library on the named volume, it checks afterwards that the library
really is there, because an APF entry names a data set together with its
volume. The file is in #cmd("~/.mbt"), or in the directory that
#cmd("MBT_HOME") names. The _MBT Reference_ lists every key.

== Passwords <ug-targets-password>

#idx("password")#idx("keychain")
A password can be written into the file, but MBT accepts that only while
nobody else can read it (#cmd("chmod 600")). Better, the file says where the
password comes from:

#deflist(width: 2.4in,
  [#cmd("{ env = \"LAB_PASSWORD\" }")], [from an environment variable.],
  [#cmd("{ keychain = \"mbt/lab\" }")], [from the macOS Keychain, or from the
    Secret Service on Linux, under that service name.],
  [#cmd("{ cmd = [\"pass\", \"show\", \"mvs/lab\"] }")], [from the first line
    a program writes. The program is started directly, without a shell.],
)

Store a password in the keychain once, in an interactive terminal: run from
a script, the commands store an empty entry without a word.

```
security add-generic-password -s mbt/lab -a IBMUSER -w     # macOS
secret-tool store --label='mbt lab' service mbt/lab        # Linux
```

An empty entry, an unset variable or a failing command is reported as such,
not as a failed logon. MBT never prints a password: #cmd("mbt target list")
and #cmd("mbt doctor") show only where it comes from.

== Choosing a Target <ug-targets-choose>

#idx("--target")
#cmd("mbt deploy") and #cmd("mbt test --mvs") take #cmd("--target")
#var("name"), and the environment variable #cmd("MBT_TARGET") does the
same. An unknown name is an error that lists the targets there are. The
name #cmd("env") means the target described by environment variables
(@ug-targets-ci). Without a name, MBT takes, in this order:

+ the target described by environment variables, when
  #cmd("MBT_TARGET_MVSMF_URL") is set\;
+ the target marked #cmd("default = true")\;
+ the only target in the file.

A file with several targets and none marked default is an error, which
asks for #cmd("--target") or a default. Only one target may be the
default. Only when there is no #cmd("targets.toml"), or it defines no
target, does MBT fall back to the settings of MBT 2 (@ug-targets-env).

== Checking a Target <ug-targets-check>

#idx("mbt target")
#cmd("mbt target list") shows the targets, the default marked with
#cmd("*"):

#fig(caption: [The targets of a workstation])[
  #screen(raw(read("../ex/ug-targets/list.txt")))
] <ug-targets-list>

#cmd("mbt target ping") asks every way of reaching a target whether it
answers, without logging on. An HTTP 401 from mvsMF is the right answer
here: the service is there and wants a logon.

#fig(caption: [Is the system there?])[
  #screen(raw(read("../ex/ug-targets/ping.txt")))
] <ug-targets-ping>

#cmd("mbt target info") logs on to mvsMF and reports what runs there:

#fig(caption: [What runs on the system])[
  #screen(raw(read("../ex/ug-targets/info.txt")))
] <ug-targets-info>

#cmd("--wait") #var("seconds") lets #cmd("ping") and #cmd("info") wait for
a system that is still starting, which is useful in CI. #cmd("mbt doctor")
without #cmd("--offline") logs on to the target too (@ug-install-check).

#idx("mvsMF", "session")
MBT logs on to mvsMF once per run, at the first request that needs it, so
a dry run never logs on. It uses that session for every request and logs
off at the end, also after an error or Ctrl-C.

== Targets in CI <ug-targets-ci>

#idx("MBT_TARGET_MVSMF_URL")#idx("CI", "target")
A CI job has no #cmd("~/.mbt"). It describes its target in environment
variables instead, the password from a secret of the repository:

#deflist(width: 2.3in,
  [#cmd("MBT_TARGET_MVSMF_URL")], [the URL of mvsMF. Setting it defines the
    target.],
  [#cmd("MBT_TARGET_MVSMF_USER")], [the user.],
  [#cmd("MBT_TARGET_MVSMF_PASSWORD")], [the password.],
  [#cmd("MBT_TARGET_HLQ"), #cmd("MBT_TARGET_VOLUME")], [as #cmd("hlq") and
    #cmd("volume") in the file.],
  [#cmd("MBT_TARGET_JOBCLASS"), #cmd("MBT_TARGET_MSGCLASS")], [as
    #cmd("jobclass") and #cmd("msgclass").],
)

The variables #cmd("MBT_TARGET_HERCULES_*") and #cmd("MBT_TARGET_TN3270_*")
add the other ways in the same manner\; the _MBT Reference_ lists them.

== Operator Commands <ug-targets-console>

#idx("mbt target console")#idx("operator command")
#cmd("mbt target console") sends an operator command to a target and shows
the reply:

```
mbt target console lab -- D T
```

The command goes through mvsMF, and, when mvsMF certainly did not take
it, because there was no connection or it answered with an HTTP error,
through the Hercules web interface. #cmd("[target.")#var("name")#cmd(".console]
order") changes the order. A command whose delivery is uncertain, because
it was sent and no answer came, is not sent a second time: it may have run.
A reply from MVS that rejects the command is an answer too, and is not
sent elsewhere.
The project's Lua code can issue operator commands the same way
(@ug-extend).

#note[mvsMF does not check whether a user may issue operator commands: any
user who can log on to it can issue any command through it. Keep that in
mind when you decide who may reach mvsMF.]

== Moving from .env <ug-targets-env>

#idx(".env")#idx("mbt target import")
MBT 2 kept the system in a file #cmd(".env") in each project. MBT 3 still
reads it, and the variables #cmd("MBT_MVS_*"), when no target is defined,
and warns on every run. #cmd("mbt target import") turns such a file into a
target:

```
mbt target import .env --name lab
```

It writes the target into #cmd("~/.mbt/targets.toml"), makes it the
default if it is the first, and makes the file readable by you alone. What
the #cmd(".env") leaves out, such as the port (default 1080) or the job
classes, it takes from where MBT 2 took it, and prints each value with its
source. A password is copied only from the #cmd(".env") itself, never from
the environment. An existing target of the same name is not replaced.
Afterwards the project's #cmd(".env") can be deleted.
