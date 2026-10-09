#import "../bookmaster/bookmaster.typ": *

= targets.toml <ref-targets>

#idx("targets.toml")
#cmd("~/.mbt/targets.toml"), or #cmd("targets.toml") in the directory
#cmd("MBT_HOME") names, describes the MVS systems of a workstation, one table
#cmd("[target.")#var("name")#cmd("]") each. A key not listed here is an
error.

== [target.NAME] <ref-targets-target>

#table(columns: (1.2in, 1.3in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("default")], [#cmd("false")], [the target used without a name. At
    most one.],
  [#cmd("hlq")], [the mvsMF user], [the high-level qualifier of the data
    sets MBT allocates.],
  [#cmd("volume")], [none], [the volume for the libraries MBT allocates
    and receives. Without it, the system chooses.],
  [#cmd("jobclass")], [#cmd("\"A\"")], [the class of the jobs MBT
    submits.],
  [#cmd("msgclass")], [#cmd("\"H\"")], [their output class.],
)

== [target.NAME.mvsmf] <ref-targets-mvsmf>

Required.

#table(columns: (1.2in, 1.3in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("url")], [required], [#cmd("http://")#var("host")#cmd(":")#var("port").
    Only #cmd("http") is supported.],
  [#cmd("user")], [required], [the user ID.],
  [#cmd("password")], [none], [a password (@ref-targets-password).],
)

== [target.NAME.hercules] <ref-targets-hercules>

The web interface of Hercules, for operator commands when mvsMF does not
take them.

#table(columns: (1.2in, 1.3in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("url")], [required], [its URL.],
  [#cmd("user")], [none], [a user, if it asks for one.],
  [#cmd("password")], [none], [its password.],
)

== [target.NAME.tn3270] <ref-targets-tn3270>

For #cmd("mbt test --tso").

#table(columns: (1.2in, 1.3in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("host")], [required], [the host.],
  [#cmd("port")], [#cmd("3270")], [the port.],
  [#cmd("tls")], [#cmd("false")], [use TLS.],
  [#cmd("user")], [none], [the TSO user ID the tests log on with.],
  [#cmd("password")], [none], [its password.],
)

== [target.NAME.ssh] <ref-targets-ssh>

#table(columns: (1.2in, 1.3in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("host")], [required], [the host that runs Hercules.],
  [#cmd("user")], [none], [the user.],
  [#cmd("identity")], [none], [a key file. No passwords: keys or an
    agent.],
  [#cmd("port")], [#cmd("22")], [the port.],
)

== [target.NAME.console] <ref-targets-console>

#table(columns: (1.2in, 1.3in, 1fr),
  [Key], [Default], [Meaning],
  [#cmd("order")], [#cmd("[\"mvsmf\", \"hercules\"]")], [the channels
    for operator commands, in the order they are tried. A channel is
    tried only when the one before certainly did not deliver.],
)

== Passwords <ref-targets-password>

#idx("password", "sources")
#table(columns: (2.4in, 1fr),
  [Value], [The password is],
  [#cmd("\"")#var("text")#cmd("\"")], [#var("text"). Accepted only while
    the file is readable by its owner alone.],
  [#cmd("{ env = \"")#var("VAR")#cmd("\" }")], [the value of the
    environment variable #var("VAR").],
  [#cmd("{ keychain = \"")#var("name")#cmd("\" }")], [the entry
    #var("name") of the macOS Keychain (#cmd("security")) or the Secret
    Service (#cmd("secret-tool")).],
  [#cmd("{ cmd = [\"")#var("prog")#cmd("\", ...] }")], [the first line
    #var("prog") writes, started without a shell.],
)

A source that yields an empty password is an error that names the source.

== Selecting a Target <ref-targets-select>

#idx("target", "selection")
A command that needs MVS takes the first of:

+ #cmd("--target") #var("name"), else the variable #cmd("MBT_TARGET"):
  the target of that name, an error listing the defined ones if there is
  none. The name #cmd("env") selects the target from the environment.
+ The target from the environment (@ref-env-target), when
  #cmd("MBT_TARGET_MVSMF_URL") is set.
+ The target with #cmd("default = true").
+ The only target in the file.

Two or more targets and none the default is an error. Only when the file
is missing or defines no target does MBT use the MBT 2 settings,
#cmd("MBT_MVS_*") and #cmd(".env"), with a warning.
