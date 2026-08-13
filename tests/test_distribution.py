"""Tests for mbt/distribution.py -- the SMP4 distribution descriptor.

Most of these guard a specific failure that cost a day in the smptest
prototype (SMP-COOKBOOK.md section 6). They are all pure text checks, so the
whole SMP generator is verifiable without touching MVS -- which matters,
because the failures they cover are the silent kind: SMP reports success and
the module is in the wrong library, or truncated, or never copied at all.
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt import distribution as D


def _cfg(**smp_overrides):
    """A minimal valid [distribution], shaped like ufsd's."""
    smp = {
        "fmid": "TUFS110",
        "system": "Z038",
        "lklib": "UFSD.@VRM@.UFSDLOAD",
        "target": "UFSD.@VRM@.LINKLIB",
        "distlib": "UFSD.@VRM@.AUFSDLOD",
    }
    smp.update(smp_overrides)
    return {
        "distribution": {
            "smp": smp,
            "library": [{"dir": "samplib", "target": "UFSD.@VRM@.SAMPLIB"}],
        }
    }


class ParseTest(unittest.TestCase):
    def test_absent_section_returns_none(self):
        self.assertIsNone(D.parse({"project": {"name": "x"}}, "V1R0M0"))

    def test_vrm_is_expanded_everywhere(self):
        dist = D.parse(_cfg(), "V1R1M1")
        self.assertEqual(dist.smp.target, "UFSD.V1R1M1.LINKLIB")
        self.assertEqual(dist.libraries[0].target, "UFSD.V1R1M1.SAMPLIB")

    def test_ddnames_come_from_the_last_qualifier(self):
        dist = D.parse(_cfg(), "V1R1M1")
        self.assertEqual(dist.smp.lklib_dd, "UFSDLOAD")
        self.assertEqual(dist.smp.target_dd, "LINKLIB")
        self.assertEqual(dist.libraries[0].target_dd, "SAMPLIB")

    def test_unknown_keys_are_fatal(self):
        for where, patch in (
            ("distribution", {"redme": "x"}),
            ("smp", {"targt": "A.B.C"}),
            ("library", {"drin": "samplib"}),
        ):
            cfg = _cfg()
            if where == "distribution":
                cfg["distribution"].update(patch)
            elif where == "smp":
                cfg["distribution"]["smp"].update(patch)
            else:
                cfg["distribution"]["library"][0].update(patch)
            with self.assertRaises(D.DistributionError, msg=where):
                D.parse(cfg, "V1R1M1")

    def test_missing_required_key_is_fatal(self):
        cfg = _cfg()
        del cfg["distribution"]["smp"]["fmid"]
        with self.assertRaises(D.DistributionError):
            D.parse(cfg, "V1R1M1")

    def test_fmid_must_be_seven_alphanumeric_starting_with_a_letter(self):
        for bad in ("TUFS11", "TUFS1100", "1UFS110", "TUFS-10"):
            with self.assertRaises(D.DistributionError, msg=bad):
                D.parse(_cfg(fmid=bad), "V1R1M1")
        self.assertEqual(D.parse(_cfg(fmid="tufs110"), "V1R1M1").smp.fmid,
                         "TUFS110")


class DatasetRoleTest(unittest.TestCase):
    """Which datasets are allocated up front, and which RECEIVE creates.

    Getting this backwards breaks the install: TSO RECEIVE refuses to merge
    into an existing dataset, so pre-allocating a RECEIVE target makes the
    step fail.
    """

    def setUp(self):
        self.dist = D.parse(_cfg(), "V1R1M1")

    def test_only_the_smp_libraries_are_allocated(self):
        self.assertEqual(self.dist.allocated_datasets(),
                         ["UFSD.V1R1M1.LINKLIB", "UFSD.V1R1M1.AUFSDLOD"])

    def test_receive_targets_are_not_allocated(self):
        for dsn in self.dist.received_datasets():
            self.assertNotIn(dsn, self.dist.allocated_datasets())

    def test_staging_library_is_received_first(self):
        # The APPLY reads the load modules out of it.
        self.assertEqual(self.dist.received_datasets()[0],
                         "UFSD.V1R1M1.UFSDLOAD")
        self.assertIn("UFSD.V1R1M1.SAMPLIB", self.dist.received_datasets())


class TrapTest(unittest.TestCase):
    """One test per trap the cookbook records."""

    def test_staging_and_target_may_not_be_the_same_dataset(self):
        # Asking SMP to copy a member onto itself.
        with self.assertRaises(D.DistributionError):
            D.parse(_cfg(lklib="UFSD.@VRM@.LINKLIB"), "V1R1M1")

    def test_smptlib_is_rejected(self):
        # F9: SMPAPP/SMPREC already define SMPTLIB; the collision is silent.
        with self.assertRaises(D.DistributionError) as cm:
            D.parse(_cfg(lklib="UFSD.@VRM@.SMPTLIB"), "V1R1M1")
        self.assertIn("SMPTLIB", str(cm.exception))

    def test_two_datasets_may_not_share_a_ddname(self):
        # Renaming the middle qualifier does not help -- the *last* one is
        # what SMP addresses.
        cfg = _cfg()
        cfg["distribution"]["library"][0]["target"] = "SYS2.OTHER.LINKLIB"
        with self.assertRaises(D.DistributionError) as cm:
            D.parse(cfg, "V1R1M1")
        self.assertIn("LINKLIB", str(cm.exception))

    def test_job_card_programmer_name_is_capped(self):
        # F3: longer is IEF642I and the job never runs.
        D.jobcard("UFSDINS", "X" * D.MAX_PROGRAMMER_NAME)
        with self.assertRaises(D.DistributionError):
            D.jobcard("UFSDINS", "X" * (D.MAX_PROGRAMMER_NAME + 1))


class CardTextTest(unittest.TestCase):
    def test_the_generated_limit_is_column_71(self):
        # We emit JCL as well as MCS, and a JCL statement stops at 71 (72 is
        # its continuation column). MCS itself goes to 72 -- measured, see
        # MAX_MCS_COL -- but holding our own output to the stricter of the two
        # costs nothing.
        self.assertEqual(D.MAX_CARD_COL, 71)
        self.assertEqual(D.MAX_MCS_COL, 72)
        D.check_card_text("x" * 71, "t")
        with self.assertRaises(D.DistributionError) as cm:
            D.check_card_text("x" * 72, "t")
        self.assertIn("column 72", str(cm.exception))

    def test_trailing_blanks_do_not_count(self):
        D.check_card_text("x" * 71 + "     ", "t")

    def test_tabs_are_rejected(self):
        with self.assertRaises(D.DistributionError):
            D.check_card_text("//SYSIN\tDD *", "t")

    def test_non_ascii_is_rejected(self):
        # ufsd commit b61fb69 had to strip UTF-8 em dashes out of samplib for
        # exactly this reason.
        with self.assertRaises(D.DistributionError) as cm:
            D.check_card_text("ROOT DSN(UFSD.ROOT) — the root", "t")
        self.assertIn("U+2014", str(cm.exception))


class MemberNameTest(unittest.TestCase):
    def test_derivation_matches_xmit370(self):
        self.assertEqual(D.member_name("samplib/ufsdprm0"), "UFSDPRM0")
        self.assertEqual(D.member_name("jcl/alloc.jcl"), "ALLOC")
        self.assertEqual(D.member_name("samplib/ufsd#cmd"), "UFSD#CMD")

    def test_invalid_names_are_rejected(self):
        for bad in ("samplib/toolongname", "samplib/9lives", "samplib/a-b"):
            with self.assertRaises(D.DistributionError, msg=bad):
                D.member_name(bad)


class AssembleTest(unittest.TestCase):
    def setUp(self):
        self.dist = D.parse(_cfg(), "V1R1M1")
        self.modules = ["UFSD", "UFSDSSIR", "UFSDCLNP", "UFSFMT"]
        self.mcs = D.assemble_mcs(self.dist, self.modules, "ufsd", "1.1.1")

    def test_shape(self):
        self.assertTrue(self.mcs.startswith("++FUNCTION(TUFS110) ."))
        self.assertIn("++VER(Z038)", self.mcs)
        self.assertIn("++JCLIN .", self.mcs)

    def test_one_mod_per_module_via_lklib(self):
        for m in self.modules:
            self.assertIn(
                f"++MOD({m}) LKLIB(UFSDLOAD) DISTLIB(AUFSDLOD) .", self.mcs)

    def test_the_sample_library_is_not_in_the_sysmod(self):
        # It ships as its own XMIT, so nothing in the SYSMOD comes from a file
        # we did not write -- which is what keeps inline delivery safe.
        self.assertNotIn("++MAC", self.mcs)
        self.assertNotIn("SAMPLIB", self.mcs)

    def test_jclin_is_a_copy_step_not_a_linkedit(self):
        # The whole mechanism: a copy step is what makes SMP copy the
        # host-bound module instead of re-binding it.
        self.assertIn("EXEC PGM=IEBCOPY", self.mcs)
        self.assertNotIn("IEWL", self.mcs)
        self.assertIn("COPY INDD=AUFSDLOD,OUTDD=LINKLIB", self.mcs)

    def test_select_member_is_always_present(self):
        # Without it SMP records the DLIB as *totally* copied.
        self.assertEqual(self.mcs.count("SELECT MEMBER=("), 1)
        for m in self.modules:
            self.assertIn(m, self.mcs.split("SELECT MEMBER=(")[1])

    def test_prereq_becomes_req(self):
        dist = D.parse(_cfg(prereq=["TUFS110"]), "V1R1M1")
        mcs = D.assemble_mcs(dist, ["X"], "p", "1.0.0")
        self.assertIn("++VER(Z038) REQ(TUFS110)", mcs)

    def test_long_module_list_wraps_inside_the_limit(self):
        many = [f"MOD{i:05d}" for i in range(40)]
        mcs = D.assemble_mcs(self.dist, many, "ufsd", "1.1.1")
        D.check_card_text(mcs, "wrapped")   # raises if any card is too long
        self.assertIn(")", mcs.split("SELECT MEMBER=(")[1])
        for m in many:
            self.assertIn(m, mcs)

    def test_nothing_starts_with_the_instream_delimiter(self):
        # A card starting with it would end the //SMPPTFIN DD DATA stream
        # mid-SYSMOD and the remainder would be read as JCL.
        for line in self.mcs.splitlines():
            self.assertFalse(line.startswith(D.INSTREAM_DELIMITER), line)


class ReceiveTest(unittest.TestCase):
    def setUp(self):
        self.dist = D.parse(_cfg(), "V1R1M1")
        self.plan = D.receive_plan(self.dist, {
            "UFSD.V1R1M1.UFSDLOAD": "ufsd-1.1.1-load.xmit",
            "UFSD.V1R1M1.SAMPLIB": "ufsd-1.1.1-samplib.xmit",
        })
        self.jcl = D.render_receive_steps(self.plan)

    def test_each_step_has_its_own_placeholder(self):
        # One shared placeholder would leave the operator guessing which line
        # means which file.
        placeholders = [ph for _s, _d, ph, _f in self.plan]
        self.assertEqual(len(placeholders), len(set(placeholders)))
        for ph in placeholders:
            self.assertTrue(ph.startswith(D.XMIT_EDIT_PREFIX), ph)

    def test_targets_are_deleted_before_they_are_received(self):
        # RECEIVE refuses to merge, so without the DELETE the job runs once.
        del_at = self.jcl.index("DELETE")
        recv_at = self.jcl.index("RECEIVE INDSN")
        self.assertLess(del_at, recv_at)
        self.assertIn("SET MAXCC=0", self.jcl)

    def test_the_smp_libraries_are_never_deleted(self):
        # F5: after an ACCEPT the DLIB holds the accepted copy, and the target
        # library holds the installed modules. Deleting either would leave the
        # inventory reporting an install that is no longer there.
        for dsn in self.dist.allocated_datasets():
            self.assertNotIn(f"DELETE {dsn}", self.jcl)

    def test_receive_names_its_own_target(self):
        # So the dataset lands where the rest of the job expects it, whatever
        # the XMIT's own restore name or the submitter's TSO prefix say.
        for _step, dsn, _ph, _f in self.plan:
            self.assertIn(f"DATASET('{dsn}')", self.jcl)

    def test_fits_on_a_card(self):
        D.check_card_text(self.jcl, "receive steps")


class ApplyDDTest(unittest.TestCase):
    def setUp(self):
        self.dist = D.parse(_cfg(), "V1R1M1")
        self.dds = D.render_apply_dds(self.dist)

    def test_overrides_precede_additions(self):
        # F1/T9: an added DD before an overriding one makes the override stop
        # being an override -- silently, with the log still naming the ddname
        # we wanted.
        kinds = [ln.split(".")[1].split()[0] in D.SMPAPP_PROC_DDS
                 for ln in self.dds.splitlines()]
        self.assertEqual(kinds, sorted(kinds, reverse=True),
                         f"overrides must come first:\n{self.dds}")

    def test_every_dd_is_qualified_with_the_proc_step(self):
        for line in self.dds.splitlines():
            self.assertTrue(line.startswith(f"//{D.SMP_PROC_STEP}."), line)

    def test_covers_exactly_the_three_libraries_smp_touches(self):
        for dsn in (self.dist.smp.target, self.dist.smp.lklib,
                    self.dist.smp.distlib):
            self.assertIn(dsn, self.dds)
        # the sample library is none of SMP's business
        self.assertNotIn(self.dist.libraries[0].target, self.dds)


class AllocDDTest(unittest.TestCase):
    def setUp(self):
        self.jcl = D.render_alloc_dds(D.parse(_cfg(), "V1R1M1"))

    def test_never_scratches_and_allocates_in_cylinders(self):
        # F5: after an ACCEPT the DLIB holds the accepted copy -- a re-run
        # that scratched it would leave the inventory lying. (The DELETE in
        # DISP= is the abnormal disposition, not an IDCAMS DELETE.)
        # F8: a track primary hits the 16-extent limit as an SB37.
        self.assertNotIn("SCRATCH", self.jcl)
        self.assertNotIn("IDCAMS", self.jcl)
        self.assertIn("DISP=(NEW,CATLG,DELETE)", self.jcl)
        self.assertIn("SPACE=(CYL,", self.jcl)
        self.assertNotIn("SPACE=(TRK,", self.jcl)

    def test_allocates_load_libraries(self):
        self.assertEqual(self.jcl.count("RECFM=U,BLKSIZE=15040"), 2)

    def test_fits_on_a_card(self):
        D.check_card_text(self.jcl, "alloc DDs")


if __name__ == "__main__":
    unittest.main()
