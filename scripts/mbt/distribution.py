"""SMP4 distribution descriptor -- parse, validate, assemble the SYSMOD.

Turns the `[distribution]` table of a project.toml into the text artifacts an
operator needs to install the product on MVS 3.8j through SMP **Release 4**
(GC28-0673-6) -- not SMP/E: there are no CSI zones, no DDDEFs and no data
element type here.

The delivery model, measured on a live system in the `smptest` prototype:

    ld370 binds on the host, SMP copies on MVS.

`++MOD(x) LKLIB(dd)` combined with a **COPY** style `++JCLIN` (an IEBCOPY step,
not a link-edit step) makes SMP copy the finished load module instead of
re-binding it, so AC, RENT/REUS and the entry point survive exactly as ld370
set them.  Text members (PROC, PARMLIB, sample JCL) travel as `++MAC` steered
by `DISTLIB()`/`SYSLIB()`.

Everything in this module is pure: it takes parsed TOML plus file contents and
returns strings.  File, archive and template handling lives in mbtdist.py.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from pathlib import Path


class DistributionError(Exception):
    """A `[distribution]` block is malformed or would generate invalid MCS."""


# --------------------------------------------------------------------------
# Hard limits.  Every one of these was a day lost in the smptest prototype;
# see SMP-COOKBOOK.md section 6.  They are checked, not documented and hoped for.
# --------------------------------------------------------------------------

#: An MCS statement is read to column 72; 73-80 is the sequence field.  A
#: statement whose terminating '.' falls past the limit is silently swallowed
#: and SMP reads the *next* statement as a continuation of this one.
#:
#: Measured, not assumed: job SMPXINL on mvsdev (SMP 4 level 04.48) received a
#: SYSMOD whose ++VER terminator sat in column 72 with RC 0, and the stored PTS
#: member came back byte-identical.  The smptest cookbook records this limit as
#: 71 -- one column too strict, and strict enough to reject its own committed
#: smp/TSMP100.mcs, whose line 6 is 72 columns and was received successfully.
MAX_MCS_COL = 72

#: A JCL statement, by contrast, is read to column 71 -- 72 is JCL's
#: continuation column.  This is the limit for the JCLIN cards inside a SYSMOD
#: and for the jobs we generate, so everything we emit is held to it.
MAX_CARD_COL = 71

#: F9 -- SMPAPP/SMPREC already define SMPTLIB themselves (the RELFILE work
#: volume).  Reusing it for our staging library collides without a diagnostic.
RESERVED_DDNAMES = frozenset({"SMPTLIB"})

#: F3 -- a longer programmer name on the JOB card is IEF642I EXCESSIVE
#: PARAMETER LENGTH and the job never runs.
MAX_PROGRAMMER_NAME = 20

#: DD names the SMPAPP/SMPREC procedures already carry (all pointing at SYS1.*),
#: read off the procs on a live MVS 3.8j system.  A DD of ours whose name is in
#: here is an **override** and must be written before every added DD, or JCL
#: treats it as a second DD of the same name: both datasets get allocated, SMP
#: uses the procedure's, and every message still reports the ddname we wanted --
#: the mistake is not visible in the log (F1/T9).
SMPAPP_PROC_DDS = frozenset({
    "CMDLIB", "HELP", "MACLIB", "PARMLIB", "UMODLIB", "LINKLIB",
    "LPALIB", "PROCLIB", "SAMPLIB", "ASAMPLIB", "UMODOBJ",
})

#: The single step of both SMP procedures; DD overrides must carry it as a
#: qualifier, and so must a COND referback (F4: an unqualified one is IEF645I).
SMP_PROC_STEP = "HMASMP"

#: Terminator for the //SMPPTFIN DD DATA stream carrying the SYSMOD.
#:
#: The SYSMOD cannot ride a plain `DD *`: its JCLIN is JCL card images
#: starting with '//' in column 1, and any shipped procedure member is too;
#: the inline copy statements end on '/*' cards.  Each of those would cut the
#: stream short.  `DD DATA,DLM=` moves the terminator somewhere the payload
#: never goes -- provided no card in the payload starts with it, which
#: assemble_mcs() verifies rather than assumes.
INSTREAM_DELIMITER = "@@"

_FMID_RE = re.compile(r"^[A-Z][A-Z0-9]{6}$")
_DDNAME_RE = re.compile(r"^[A-Z@#$][A-Z0-9@#$]{0,7}$")
_QUALIFIER_RE = re.compile(r"^[A-Z@#$][A-Z0-9@#$-]{0,7}$")
_MEMBER_RE = re.compile(r"^[A-Z@#$][A-Z0-9@#$]{0,7}$")

_MAX_DSN = 44


# --------------------------------------------------------------------------
# Small helpers
# --------------------------------------------------------------------------

def ddname_of(dsn: str) -> str:
    """The DD name SMP uses for a dataset: its last qualifier.

    Not a convention we invented -- it is how SMP finds the library, and the
    manual states it for target libraries ("the ddnames used for target
    libraries are usually the lowest level qualifiers of the data set names,
    that is, TCAMLIB for SYS1.TCAMLIB") as well as for DLIBs.  The copy step
    rules extend it to the INDD/OUTDD names inside the JCLIN, warning that
    otherwise "SMP will generate incorrect DLIB and SYSLIB names".

    Making it the *only* rule is why the schema names datasets everywhere and
    never a bare ddname.
    """
    return dsn.split(".")[-1]


def member_name(path: str | Path) -> str:
    """Derive an MVS member name from a host file name.

    Same rule xmit370 uses when it packs a directory: the base name with one
    trailing extension removed, upper-cased.  `samplib/ufsdprm0` -> UFSDPRM0,
    `jcl/alloc.jcl` -> ALLOC.
    """
    name = Path(path).stem.upper()
    if not _MEMBER_RE.match(name):
        raise DistributionError(
            f"{path}: '{name}' is not a valid MVS member name "
            f"(1-8 of A-Z 0-9 @ # $, first character not a digit). "
            f"Rename the file."
        )
    return name


def check_dsn(dsn: str, what: str) -> str:
    """Validate a dataset name and the ddname that follows from it."""
    if not dsn:
        raise DistributionError(f"{what}: dataset name is empty")
    if len(dsn) > _MAX_DSN:
        raise DistributionError(
            f"{what}: '{dsn}' is {len(dsn)} characters, the MVS limit is {_MAX_DSN}"
        )
    for qual in dsn.split("."):
        if not _QUALIFIER_RE.match(qual):
            raise DistributionError(
                f"{what}: '{dsn}' has an invalid qualifier '{qual}' "
                f"(1-8 of A-Z 0-9 @ # $, first character not a digit)"
            )
    dd = ddname_of(dsn)
    if not _DDNAME_RE.match(dd):
        raise DistributionError(
            f"{what}: '{dsn}' yields the ddname '{dd}', which is not valid. "
            f"SMP addresses the library by the last qualifier, so it must be "
            f"a usable ddname."
        )
    if dd in RESERVED_DDNAMES:
        raise DistributionError(
            f"{what}: '{dsn}' yields the ddname '{dd}', which the SMPAPP and "
            f"SMPREC procedures already define themselves. The collision would "
            f"be silent -- choose a different last qualifier."
        )
    return dsn


def check_card_text(text: str, what: str) -> None:
    """Reject anything that will not survive an 80-column EBCDIC card deck.

    Three separate failure modes, all of them silent on MVS if they get
    through:

    * past column 71 -- for an MCS statement the '.' terminator falls off the
      card (F2).  Element bodies are held to the same limit: whether SMP4
      copies inline element data beyond column 71 is not established, and a
      wrong guess corrupts a member rather than failing a job.
    * a tab -- expands differently in every transfer path.
    * a byte outside ASCII -- the ASCII->EBCDIC conversion of an upload has no
      mapping for it. A UTF-8 em dash sneaking into samplib is exactly what
      ufsd commit b61fb69 had to clean up.
    """
    for n, line in enumerate(text.splitlines(), 1):
        if "\t" in line:
            raise DistributionError(
                f"{what}: line {n} contains a tab. Expand it -- tabs do not "
                f"survive the transfer to an 80-column card deck."
            )
        for ch in line:
            if not (0x20 <= ord(ch) <= 0x7E):
                raise DistributionError(
                    f"{what}: line {n} contains the non-ASCII or control "
                    f"character {ch!r} (U+{ord(ch):04X}). It has no EBCDIC "
                    f"mapping; use plain ASCII."
                )
        if len(line.rstrip()) > MAX_CARD_COL:
            raise DistributionError(
                f"{what}: line {n} reaches column {len(line.rstrip())}, past "
                f"{MAX_CARD_COL}:\n    {line}\n"
                f"An MCS card is only read to column {MAX_CARD_COL}."
            )


# --------------------------------------------------------------------------
# The descriptor
# --------------------------------------------------------------------------

@dataclass(frozen=True)
class Library:
    """One host directory shipped as its own XMIT, outside SMP.

    Sample material -- a started task procedure, a PARMLIB pattern, a format
    job -- travels as a source PDS built by xmit370 and unpacked by TSO
    RECEIVE, not as `++MAC` elements inside the SYSMOD.

    Two reasons.  The content stays free: an MCS card ends at column 72, so
    routing samples through the SYSMOD would make a *configuration pattern*
    answer to a delivery mechanism (ufsd's UFSDPRM0 had three MOUNT statements
    at 76 columns).  And SMP tracking buys nothing here, because the target
    datasets are versioned: every release lands in its own SAMPLIB, so there
    is no in-place update for an inventory to manage.

    What SMP still owns is the load modules -- which is what PTFs replace and
    what RESTORE needs.
    """

    dir: str
    target: str      # DSN the XMIT is received into

    @property
    def target_dd(self) -> str:
        return ddname_of(self.target)


@dataclass(frozen=True)
class Smp:
    """The SMP4 side of the descriptor."""

    fmid: str
    system: str
    lklib: str       # DSN of the staging load library the APPLY reads
    target: str      # DSN the load modules are installed into
    distlib: str     # DSN of the distribution library backing them
    prereq: tuple[str, ...] = ()
    accept_fmid: bool = True

    @property
    def lklib_dd(self) -> str:
        return ddname_of(self.lklib)

    @property
    def target_dd(self) -> str:
        return ddname_of(self.target)

    @property
    def distlib_dd(self) -> str:
        return ddname_of(self.distlib)


@dataclass(frozen=True)
class Distribution:
    smp: Smp
    libraries: tuple[Library, ...] = ()
    readme: str | None = None
    extra: tuple[str, ...] = ()

    def allocated_datasets(self) -> list[str]:
        """Datasets the allocation job must create up front.

        Only the two SMP writes into: the target library its copy step fills,
        and the distribution library ACCEPT fills.  Everything else arrives by
        TSO RECEIVE, which allocates its own target and *refuses to merge* into
        an existing one -- pre-allocating those would break the install.
        """
        return [self.smp.target, self.smp.distlib]

    def received_datasets(self) -> list[str]:
        """Datasets TSO RECEIVE creates from an XMIT, in install order.

        The staging library first because the APPLY reads the modules from it,
        then one per shipped directory.
        """
        return [self.smp.lklib] + [lib.target for lib in self.libraries]

    def datasets(self) -> list[str]:
        return self.allocated_datasets() + self.received_datasets()


# --------------------------------------------------------------------------
# Parsing
# --------------------------------------------------------------------------

_DIST_KEYS = {"readme", "extra", "smp", "library"}
_SMP_KEYS = {"fmid", "system", "lklib", "target", "distlib", "prereq",
             "accept_fmid"}
_LIB_KEYS = {"dir", "target", "distlib"}


def _reject_unknown(section: dict, known: set[str], where: str) -> None:
    """Fail on a key we do not understand.

    A typo in a dataset name is a job that fails loudly; a typo in a *key* is
    a setting that silently does not apply.  mbttoolchain.resolve() takes the
    same line with [toolchain].
    """
    unknown = sorted(set(section) - known)
    if unknown:
        raise DistributionError(
            f"unknown {where} key(s): {', '.join(unknown)} "
            f"(known: {', '.join(sorted(known))})"
        )


def _require(section: dict, key: str, where: str):
    if key not in section:
        raise DistributionError(f"{where}: '{key}' is required")
    return section[key]


def parse(cfg: dict, vrm: str) -> Distribution | None:
    """Build a Distribution from parsed project.toml, or None if absent.

    `vrm` replaces the `@VRM@` placeholder in every dataset name, so a
    project.toml never has to repeat the version (`UFSD.@VRM@.LINKLIB` ->
    `UFSD.V1R1M1.LINKLIB`).
    """
    dist = cfg.get("distribution")
    if not dist:
        return None
    _reject_unknown(dist, _DIST_KEYS, "[distribution]")

    def expand(dsn: str, what: str) -> str:
        return check_dsn(str(dsn).replace("@VRM@", vrm).upper(), what)

    smp_cfg = _require(dist, "smp", "[distribution]")
    _reject_unknown(smp_cfg, _SMP_KEYS, "[distribution.smp]")

    fmid = str(_require(smp_cfg, "fmid", "[distribution.smp]")).upper()
    if not _FMID_RE.match(fmid):
        raise DistributionError(
            f"[distribution.smp]: fmid '{fmid}' must be exactly 7 characters, "
            f"alphanumeric, starting with a letter (e.g. TUFS110)"
        )

    prereq = tuple(str(p).upper() for p in smp_cfg.get("prereq", []))
    for p in prereq:
        if not _FMID_RE.match(p):
            raise DistributionError(
                f"[distribution.smp]: prereq '{p}' is not a valid 7-character "
                f"SYSMOD id"
            )

    smp = Smp(
        fmid=fmid,
        system=str(_require(smp_cfg, "system", "[distribution.smp]")).upper(),
        lklib=expand(_require(smp_cfg, "lklib", "[distribution.smp]"),
                     "[distribution.smp] lklib"),
        target=expand(_require(smp_cfg, "target", "[distribution.smp]"),
                      "[distribution.smp] target"),
        distlib=expand(_require(smp_cfg, "distlib", "[distribution.smp]"),
                       "[distribution.smp] distlib"),
        prereq=prereq,
        accept_fmid=bool(smp_cfg.get("accept_fmid", True)),
    )

    # The staging library is where the APPLY reads the module *from*; the
    # target is where it copies it *to*. Naming them the same dataset would
    # ask SMP to copy a member onto itself.
    if smp.lklib == smp.target:
        raise DistributionError(
            f"[distribution.smp]: lklib and target are both '{smp.lklib}'. "
            f"The staging library the APPLY reads from must be a different "
            f"dataset than the library it installs into."
        )

    libraries = []
    for lib_cfg in dist.get("library", []):
        _reject_unknown(lib_cfg, _LIB_KEYS, "[[distribution.library]]")
        libraries.append(Library(
            dir=str(_require(lib_cfg, "dir", "[[distribution.library]]")),
            target=expand(_require(lib_cfg, "target", "[[distribution.library]]"),
                          "[[distribution.library]] target"),
        ))

    result = Distribution(
        smp=smp,
        libraries=tuple(libraries),
        readme=dist.get("readme"),
        extra=tuple(dist.get("extra", [])),
    )
    _check_ddname_collisions(result)
    return result


def _check_ddname_collisions(dist: Distribution) -> None:
    """Two of our datasets must never share a ddname.

    They are addressed by ddname only, so a collision means one of them is
    simply not reachable -- and renaming does not help if the *last qualifier*
    is what collides (SYS2.UFSD.LINKLIB and UFSD.V1R1M1.LINKLIB are both
    LINKLIB).
    """
    seen: dict[str, str] = {}
    for dsn in dist.datasets():
        dd = ddname_of(dsn)
        if dd in seen and seen[dd] != dsn:
            raise DistributionError(
                f"'{dsn}' and '{seen[dd]}' both yield the ddname '{dd}'. "
                f"SMP addresses libraries by ddname, so one of them would be "
                f"unreachable. Give them different last qualifiers."
            )
        seen[dd] = dsn


# --------------------------------------------------------------------------
# SYSMOD assembly
# --------------------------------------------------------------------------

def _copy_step(step: str, distlib_dd: str, distlib_dsn: str,
               target_dd: str, target_dsn: str, members: list[str]) -> list[str]:
    """One IEBCOPY step of the JCLIN -- the whole point of the exercise.

    This is a *copy* step, not a link-edit step, and that is what tells SMP the
    load module is copied out of the DLIB rather than re-bound.  A link-edit
    step here would re-bind and silently restore attributes ld370 deliberately
    cleared (a `norent` C module re-marked RENT takes an S0C4 on its first
    static store, and only when entered at the READY prompt -- batch and
    IKJEFT01 CALL both survive it).

    Three rules the step has to obey:
      * the copy control statements must be INLINE behind //SYSIN DD * --
        SMP reads them during the JCLIN scan and cannot follow a dataset
      * ddnames must equal the last qualifier of the dataset name
      * SELECT MEMBER=() must be present, or SMP records the DLIB as *totally*
        copied.  That also caps a DLIB at two target libraries -- a third does
        not fail, it silently overwrites the second SYSLIB sub-entry of the
        DLIB entry.  We emit exactly one target, so it cannot arise; a product
        that ever needs a second (LPALIB, say) must split the DLIB at three.

    No SYSUT3/SYSUT4 here: SMP scans this JCL to learn the build description,
    it never executes it.
    """
    lines = [
        f"//{step:<8} EXEC PGM=IEBCOPY",
        f"//{distlib_dd:<8} DD  DISP=SHR,DSN={distlib_dsn}",
        f"//{target_dd:<8} DD  DISP=SHR,DSN={target_dsn}",
        "//SYSIN    DD  *",
        f"  COPY INDD={distlib_dd},OUTDD={target_dd}",
    ]
    lines.extend(_select_member_cards(members))
    lines.append("/*")
    return lines


def _select_member_cards(members: list[str]) -> list[str]:
    """SELECT MEMBER=(...), wrapped so no card passes column 71."""
    head = "  SELECT MEMBER=("
    lines: list[str] = []
    cur = head
    for i, m in enumerate(members):
        piece = m + ("," if i < len(members) - 1 else ")")
        # +1 keeps a column of slack under the limit
        if len(cur) + len(piece) > MAX_CARD_COL - 1:
            lines.append(cur)
            cur = " " * len(head) + piece
        else:
            cur += piece
    lines.append(cur)
    return lines


def assemble_mcs(dist: Distribution,
                 modules: list[str],
                 product: str,
                 version: str) -> str:
    """Build the complete SYSMOD as card image text.

    It covers the load modules only -- roughly twenty lines. Sample material
    ships as its own XMIT (see Library), so nothing here comes from a file we
    did not write, which is what keeps the inline delivery safe.

    Args:
        dist:    the parsed descriptor
        modules: MVS member names of the load modules, in project.toml order
        product: project name, for the comment block
        version: project version, for the comment block

    Returns:
        The SYSMOD, ready to go inline behind //SMPPTFIN DD DATA,DLM=.
    """
    smp = dist.smp
    out: list[str] = [f"++FUNCTION({smp.fmid}) ."]

    ver = f"++VER({smp.system})"
    if smp.prereq:
        ver += f" REQ({','.join(smp.prereq)})"
    out.append(ver)
    out.append(f"   /* {product} {version}".ljust(65) + "*/")
    out.append("   /* Load modules are copied from the LKLIB, not re-bound.".ljust(65) + "*/ .")

    # ---- JCLIN: the copy step that defines how the modules are installed ----
    out.append("++JCLIN .")
    out.append(f"//{smp.fmid} JOB 1,'{product.upper()[:20]} JCLIN',"
               f"MSGLEVEL=1,CLASS=A")
    out.extend(_copy_step("COPYLOAD", smp.distlib_dd, smp.distlib,
                          smp.target_dd, smp.target, modules))

    # ---- Elements ----
    # Whole load modules only. A single object ++MOD against a copy-defined
    # LMOD would make SMP bind that object alone, with no INCLUDE of the
    # current version -- the module would be destroyed, not updated.
    for mod in modules:
        out.append(f"++MOD({mod}) LKLIB({smp.lklib_dd}) "
                   f"DISTLIB({smp.distlib_dd}) .")

    text = "\n".join(out) + "\n"
    check_card_text(text, f"generated SYSMOD {smp.fmid}")
    for n, line in enumerate(out, 1):
        if line.startswith(INSTREAM_DELIMITER):
            raise DistributionError(
                f"generated SYSMOD {smp.fmid}: line {n} starts with "
                f"'{INSTREAM_DELIMITER}', which terminates the "
                f"//SMPPTFIN DD DATA stream carrying it -- the rest of the "
                f"SYSMOD would be read as JCL:\n    {line}"
            )
    return text


# --------------------------------------------------------------------------
# JCL fragments
# --------------------------------------------------------------------------

def render_apply_dds(dist: Distribution) -> str:
    """The DD block for an APPLY/ACCEPT step, in the only order that works.

    JCL requires every DD *added* to a procedure step to follow all DDs that
    *override* one the procedure already has.  Get it wrong and the override
    is not an override at all: both datasets are allocated under the same
    ddname, SMP uses the procedure's, and every message still names the ddname
    we intended -- so the log does not show the mistake.  The sort below is
    that rule, mechanised: known procedure DDs first, our additions after.
    """
    overrides: list[tuple[str, str]] = []
    additions: list[tuple[str, str]] = []
    # Only the three libraries SMP itself touches: it reads the modules from
    # the staging library, copies them into the target, and ACCEPT files them
    # in the DLIB. A shipped sample library is none of SMP's business.
    for dsn in (dist.smp.target, dist.smp.lklib, dist.smp.distlib):
        dd = ddname_of(dsn)
        (overrides if dd in SMPAPP_PROC_DDS else additions).append((dd, dsn))

    lines = []
    for dd, dsn in overrides + additions:
        lines.append(f"//{SMP_PROC_STEP}.{dd:<8} DD  DISP=SHR,DSN={dsn}")
    return "\n".join(lines)


def render_alloc_dds(dist: Distribution, volume: str | None = None) -> str:
    """DD block for the IEFBR14 allocation step.

    DISP=(NEW,CATLG,DELETE) and no IDCAMS DELETE anywhere: once the FMID has
    been accepted the DLIB holds the accepted copy of every module, and a
    re-run that deleted it would leave the CDS saying "accepted" with an empty
    library behind it.  Space is in cylinders -- a track primary hits the
    16-extent limit long before the volume is full, and the resulting SB37
    reads like a full volume.
    """
    vol = f",VOL=SER={volume}" if volume else ""
    lines = []
    for dsn in dist.allocated_datasets():
        dd = ddname_of(dsn)
        lines.append(f"//{dd:<8} DD  DSN={dsn},DISP=(NEW,CATLG,DELETE),")
        lines.append(f"//            UNIT=SYSDA{vol},SPACE=(CYL,(5,2,20)),")
        lines.append(f"//            DCB=(DSORG=PO,RECFM=U,BLKSIZE=15040)")
    return "\n".join(lines)


#: Prefix of the placeholder dataset names an operator has to replace in a
#: generated install job.  Deliberately not a plausible default: it cannot be
#: mistaken for something that might work, and one search finds every one.
XMIT_EDIT_PREFIX = "CHANGE.ME"


def receive_plan(dist: Distribution, xmit_files: dict[str, str]
                 ) -> list[tuple[str, str, str, str]]:
    """Work out the RECEIVE steps: (step name, target DSN, placeholder, file).

    Order matters: the staging library first, because the APPLY reads the
    load modules out of it.

    Args:
        xmit_files: {target DSN -> name of the shipped XMIT file}, so the job
                    can tell the operator which file belongs on which line.
    """
    plan = []
    for i, dsn in enumerate(dist.received_datasets()):
        dd = ddname_of(dsn)
        plan.append((f"RECV{i + 1}", dsn,
                     f"{XMIT_EDIT_PREFIX}.{dd}", xmit_files.get(dsn, "?")))
    return plan


def render_receive_steps(plan: list[tuple[str, str, str, str]]) -> str:
    """The TSO RECEIVE steps that unpack the shipped XMITs.

    Each is a batch IKJEFT01 step naming its own target on the RECEIVE
    command, so the dataset ends up where the rest of the job expects it --
    the XMIT's own restore name and the submitter's TSO prefix never enter
    into it.  The same shape mbtdeploy.py has run in production.

    A DELETE precedes them because RECEIVE refuses to merge into an existing
    dataset; without it the job would run exactly once.  Only these targets
    are ever deleted -- never the target library, and never the DLIB, which
    after an ACCEPT holds the accepted copy of every module.
    """
    lines = [
        "//* The RECEIVE targets are deleted first: TSO RECEIVE refuses to",
        "//* merge into an existing dataset, so without this the job would",
        "//* run exactly once. SET MAXCC=0 keeps a first run, where none of",
        "//* them exists yet, from failing on the DELETEs.",
        "//*",
        "//DELOLD  EXEC PGM=IDCAMS",
        "//SYSPRINT DD  SYSOUT=*",
        "//SYSIN    DD  *",
    ]
    for _step, dsn, _ph, _f in plan:
        lines.append(f"  DELETE {dsn} NONVSAM SCRATCH PURGE")
    lines += ["  SET MAXCC=0", "/*"]
    for step, dsn, placeholder, fname in plan:
        lines += [
            "//*",
            f"//* {fname}",
            f"//*   -> {dsn}",
            f"//{step:<7} EXEC PGM=IKJEFT01,DYNAMNBR=20",
            "//SYSTSPRT DD  SYSOUT=*",
            "//SYSTSIN  DD  *",
            f"  RECEIVE INDSN('{placeholder}') -",
            f"    DATASET('{dsn}')",
            "/*",
        ]
    return "\n".join(lines)


def jobcard(jobname: str, programmer: str, msgclass: str = "H") -> str:
    """A JOB card for a job the *operator* submits.

    MSGCLASS defaults to H, not A: on a system where class A is purged on
    completion a failed install job becomes unfindable and looks as though it
    never ran.
    """
    jn = jobname[:8].upper()
    if len(programmer) > MAX_PROGRAMMER_NAME:
        raise DistributionError(
            f"job card programmer name '{programmer}' is "
            f"{len(programmer)} characters; more than {MAX_PROGRAMMER_NAME} "
            f"is IEF642I EXCESSIVE PARAMETER LENGTH and the job never runs"
        )
    return (f"//{jn:<8} JOB (SYS),'{programmer}',\n"
            f"//             CLASS=A,MSGCLASS={msgclass},MSGLEVEL=(1,1),\n"
            f"//             REGION=4096K")
