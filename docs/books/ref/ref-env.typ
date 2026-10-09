#import "../bookmaster/bookmaster.typ": *

= Environment Variables <ref-env>

#idx("environment variables")
MBT reads these environment variables. None is needed for an ordinary build
on a workstation with a #cmd("targets.toml").

== MBT Itself <ref-env-mbt>

#table(columns: (1.9in, 1fr),
  [Variable], [Meaning],
  [#cmd("MBT_HOME")], [the directory used instead of #cmd("~/.mbt").],
  [#cmd("MBT_NO_SWITCH")], [#cmd("1"): run the started #cmd("mbt") even
    when the project pins another version.],
  [#cmd("GITHUB_TOKEN")], [a token for the GitHub API, which lifts the
    limit of 60 requests an hour. #cmd("MBT_GITHUB_TOKEN") is read when
    #cmd("GITHUB_TOKEN") is not set.],
  [#cmd("SOURCE_DATE_EPOCH")], [seconds since 1970: the time stamp of the
    files in the archives #cmd("mbt package") writes, for reproducible
    archives.],
)

== The Toolchain <ref-env-tools>

#table(columns: (1.9in, 1fr),
  [Variable], [Meaning],
  [#cmd("MBT_CC"), #cmd("MBT_AS"), #cmd("MBT_LD"), #cmd("MBT_AR")], [the
    commands used instead of #cmd("cc370"), #cmd("as370"), #cmd("ld370")
    and #cmd("ar370").],
  [#cmd("CC")], [the C compiler of the workstation for #cmd("mbt test").
    Default #cmd("cc").],
  [#cmd("ASMDATE"), #cmd("ASMTIME")], [read by #cmd("as370"): the date and
    time written into object modules.],
  [#cmd("LDDATE"), #cmd("LDTIME")], [read by #cmd("ld370"): the date and
    time written into load modules.],
)

#cmd("ASMDATE") and the others belong to CC/370\; set them to make two
builds byte-identical (_MBT User's Guide_, "Migrating from MBT 2").

== The Target <ref-env-target>

#idx("MBT_TARGET")
#table(columns: (2.5in, 1fr),
  [Variable], [Meaning],
  [#cmd("MBT_TARGET")], [the name of the target, as #cmd("--target").],
  [#cmd("MBT_TARGET_MVSMF_URL")], [defines a target from the environment,
    named #cmd("env"), with this mvsMF URL.],
  [#cmd("MBT_TARGET_MVSMF_USER"), #cmd("MBT_TARGET_MVSMF_PASSWORD")], [its
    user and password.],
  [#cmd("MBT_TARGET_HLQ"), #cmd("MBT_TARGET_VOLUME")], [#cmd("hlq") and
    #cmd("volume").],
  [#cmd("MBT_TARGET_JOBCLASS"), #cmd("MBT_TARGET_MSGCLASS")],
    [#cmd("jobclass") and #cmd("msgclass").],
  [#cmd("MBT_TARGET_HERCULES_URL"), #cmd("_USER"), #cmd("_PASSWORD")],
    [the Hercules web interface.],
  [#cmd("MBT_TARGET_TN3270_HOST"), #cmd("_PORT"), #cmd("_USER"),
    #cmd("_PASSWORD")], [the TN3270 access.],
)

== Waiting for MVS <ref-env-timeouts>

#table(columns: (1.9in, 1fr),
  [Variable], [Meaning],
  [#cmd("MBT_DEPLOY_TIMEOUT")], [seconds to wait for the deploy job.
    Default: 300, plus 60 for each megabyte of the TRANSMIT file.],
  [#cmd("MBT_TEST_TIMEOUT")], [seconds to wait for the job of
    #cmd("mbt test --mvs"). Default: 10 per step, at least 120.],
)

== MBT 2 Settings <ref-env-mbt2>

Without a #cmd("targets.toml") that defines a target, MBT uses the settings
of MBT 2, with a warning: #cmd("MBT_MVS_HOST"), #cmd("MBT_MVS_PORT"),
#cmd("MBT_MVS_USER"), #cmd("MBT_MVS_PASS"), #cmd("MBT_MVS_HLQ"),
#cmd("MBT_MVS_DEPS_VOLUME"), #cmd("MBT_JES_JOBCLASS") and
#cmd("MBT_JES_MSGCLASS"), from the environment or a #cmd(".env") file of the
project. #cmd("mbt target import") turns them into a target.
