#import "../bookmaster/bookmaster.typ": *

= The SMP Package <ref-smp>

#idx("SMP", "package")
With #cmd("[distribution]") and #cmd("[smp]") (@ref-toml-smp),
#cmd("mbt package") writes an installation package. #var("n") is the project
name, #var("v") the version, #var("NAME") the name in upper case, #var("P4")
its first four characters.

== Contents <ref-smp-contents>

#var("n")#cmd("-")#var("v")#cmd("-dist.zip") and #cmd("-dist.tar.gz") hold
one directory #var("n")#cmd("-")#var("v")#cmd("/") with:

#table(columns: (2.6in, 1fr),
  [File], [Contents],
  [#cmd("README.md")], [#cmd("[distribution] readme").],
  [#var("n")#cmd("-")#var("v")#cmd("-load.xmit")], [the load modules: a
    TSO TRANSMIT file of a load library.],
  [#var("n")#cmd("-")#var("v")#cmd("-")#var("q")#cmd(".xmit")], [one per
    #cmd("[distribution.library.")#var("dir")#cmd("]"): a TRANSMIT file of a
    text library, #var("q") the last qualifier of its target in lower
    case.],
  [#var("n")#cmd("-")#var("v")#cmd("-alloc.jcl")], [the allocation job.],
  [#var("n")#cmd("-")#var("v")#cmd("-inst.jcl")], [the installation job,
    with the SYSMOD inline.],
  [#cmd("extra") files], [under their own names.],
)

== Data Sets <ref-smp-datasets>

#table(columns: (1.4in, 1.9in, 1fr),
  [Data set], [Default], [Role],
  [staging], [#var("NAME")#cmd(".")#var("P4")#cmd("LOAD") (#cmd("lklib"))],
    [the load modules are received into it\; the #cmd("LKLIB") of the
    #cmd("++MOD")s. Deleted at the end.],
  [target], [#var("NAME")#cmd(".LINKLIB") (#cmd("target"))], [the
    installed modules\; DD name #cmd("LINKLIB").],
  [distribution], [#var("NAME")#cmd(".A")#var("P4")#cmd("LOD")
    (#cmd("distlib"))], [the accepted modules.],
  [text library], [#var("NAME")#cmd(".")#var("DIR")], [received with TSO
    #cmd("RECEIVE"), outside SMP.],
)

The allocation job allocates the target and distribution libraries,
#cmd("RECFM=U"), #cmd("BLKSIZE=15040"), in cylinders, and never deletes
them.

== The Installation Job <ref-smp-job>

#table(columns: (1.1in, 1fr),
  [Step], [Action],
  [#cmd("DELOLD")], [IDCAMS: deletes the staging library and every text
    library\; #cmd("SET MAXCC=0").],
  [#cmd("RECV")#var("n")], [IKJEFT01: #cmd("RECEIVE INDSN(...)
    DATASET(...)") for each TRANSMIT file. The names to edit are marked
    #cmd("CHANGE.ME").],
  [#cmd("RECV")], [SMP #cmd("RECEIVE") of the SYSMOD, inline in the job
    (#cmd("DD DATA,DLM=@@")).],
  [#cmd("APPLYCHK")], [#cmd("APPLY CHECK").],
  [#cmd("APPLY")], [#cmd("APPLY"), only after a clean check.],
  [#cmd("ACCEPT")], [#cmd("ACCEPT"), unless #cmd("accept_fmid = false").
    With #cmd("delete"), it also runs after an #cmd("APPLY") that ended with
    4.],
  [#cmd("CLEANUP")], [IDCAMS: deletes the staging library.],
)

== The SYSMOD <ref-smp-sysmod>

```
++FUNCTION(fmid) .
++VER(system) [DELETE(fmid...)] [PRE(...)] .
++JCLIN .
   an IEBCOPY job: COPY INDD=distlib,OUTDD=LINKLIB, SELECT MEMBER=(...)
++MOD(name) LKLIB(staging) DISTLIB(distlib) [TALIAS(alias...)] .
   ...
```

The #cmd("++JCLIN") is a copy, so SMP copies each module as it was linked
and keeps its attributes, entry point and authorization code. Aliases
travel as #cmd("TALIAS").

#note[*To be confirmed:* the exact form of the #cmd("++VER") with
#cmd("DELETE") and #cmd("prereq").]
