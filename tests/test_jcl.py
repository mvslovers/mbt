"""Tests for mbt/jcl.py."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "scripts"))

from mbt.jcl import (
    render_template,
    render_syslib_concat,
    render_include_concat,
    jobcard,
)


class TestRenderSyslibConcat(unittest.TestCase):

    def test_single_dataset(self):
        result = render_syslib_concat(["SYS1.MACLIB"])
        self.assertIn("//SYSLIB   DD DSN=SYS1.MACLIB,DISP=SHR", result)

    def test_first_line_has_syslib_dd(self):
        result = render_syslib_concat(["SYS1.MACLIB", "CRENT370.MACLIB"])
        lines = result.split("\n")
        self.assertTrue(lines[0].startswith("//SYSLIB"))

    def test_continuation_lines_blank_name(self):
        result = render_syslib_concat(["A.MACLIB", "B.MACLIB", "C.MACLIB"])
        lines = result.split("\n")
        self.assertEqual(len(lines), 3)
        # Lines 2+ start with "//         DD"
        self.assertTrue(lines[1].startswith("//         DD DSN=B.MACLIB"))
        self.assertTrue(lines[2].startswith("//         DD DSN=C.MACLIB"))

    def test_three_datasets(self):
        result = render_syslib_concat(["A.DS", "B.DS", "C.DS"])
        self.assertIn("A.DS", result)
        self.assertIn("B.DS", result)
        self.assertIn("C.DS", result)

    def test_empty_returns_dummy(self):
        result = render_syslib_concat([])
        self.assertEqual(result, "//SYSLIB   DD DUMMY")

    def test_no_trailing_whitespace_issues(self):
        result = render_syslib_concat(["SYS1.MACLIB"])
        self.assertNotIn("\n\n", result)

    def test_blksize_on_first_dd(self):
        result = render_syslib_concat(
            ["CRENT370.NCALIB", "HTTPD.NCALIB"], blksize=32760
        )
        lines = result.split("\n")
        self.assertIn("DCB=BLKSIZE=32760", lines[0])
        self.assertNotIn("DCB=BLKSIZE", lines[1])

    def test_no_blksize_by_default(self):
        result = render_syslib_concat(["SYS1.MACLIB", "CRENT370.MACLIB"])
        self.assertNotIn("DCB=BLKSIZE", result)


class TestRenderIncludeConcat(unittest.TestCase):

    def test_single_member_with_short_name(self):
        result = render_include_concat(["HELLO"], "SYSLIB")
        self.assertIn("INCLUDE SYSLIB(HELLO)", result)

    def test_multiple_members(self):
        result = render_include_concat(["MOD1", "MOD2", "MOD3"], "SYSLIB")
        self.assertIn("INCLUDE SYSLIB(MOD1)", result)
        self.assertIn("INCLUDE SYSLIB(MOD2)", result)
        self.assertIn("INCLUDE SYSLIB(MOD3)", result)

    def test_dsn_extracts_last_qualifier(self):
        result = render_include_concat(["HELLO"], "IBMUSER.HELLO370.V1R0M0.NCALIB")
        self.assertIn("INCLUDE NCALIB(HELLO)", result)

    def test_leading_space(self):
        # IEWL control statements start with a space
        result = render_include_concat(["HELLO"], "SYSLIB")
        for line in result.split("\n"):
            if line:
                self.assertTrue(line.startswith(" "),
                                f"Expected leading space: {line!r}")

    def test_empty_members(self):
        result = render_include_concat([], "SYSLIB")
        self.assertEqual(result, "")

    def test_one_per_line(self):
        result = render_include_concat(["A", "B"], "SYSLIB")
        lines = [l for l in result.split("\n") if l.strip()]
        self.assertEqual(len(lines), 2)


class TestJobcard(unittest.TestCase):

    def test_contains_jobname(self):
        result = jobcard("MYJOB", "A", "H")
        self.assertIn("//MYJOB", result)

    def test_uppercase_jobname(self):
        result = jobcard("myjob", "A", "H")
        self.assertIn("//MYJOB", result)

    def test_truncates_to_8_chars(self):
        result = jobcard("VERYLONGJOBNAME", "A", "H")
        self.assertIn("//VERYLONG", result)
        # First line identifier should be 8 chars max
        first_line = result.split("\n")[0]
        jn = first_line[2:].split()[0]
        self.assertLessEqual(len(jn), 8)

    def test_contains_jobclass(self):
        result = jobcard("MYJOB", "B", "H")
        self.assertIn("CLASS=B", result)

    def test_contains_msgclass(self):
        result = jobcard("MYJOB", "A", "X")
        self.assertIn("MSGCLASS=X", result)

    def test_contains_job_keyword(self):
        result = jobcard("MYJOB", "A", "H")
        self.assertIn("JOB", result)

    def test_contains_msglevel(self):
        result = jobcard("MYJOB", "A", "H")
        self.assertIn("MSGLEVEL=(1,1)", result)

    def test_custom_description(self):
        result = jobcard("MYJOB", "A", "H", "MYTEST")
        self.assertIn("MYTEST", result)


class TestRenderTemplate(unittest.TestCase):
    """Tests for render_template using actual template files."""

    def test_alloc_template_substitution(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "DSN": "IBMUSER.TEST.NCALIB",
            "UNIT": "SYSDA",
            "SPACE_UNIT": "TRK",
            "SPACE_PRI": "5",
            "SPACE_SEC": "2",
            "SPACE_DIR": "5",
            "DSORG": "PO",
            "RECFM": "FB",
            "LRECL": "80",
            "BLKSIZE": "3120",
        }
        result = render_template("alloc.jcl.tpl", vars_)
        self.assertIn("IBMUSER.TEST.NCALIB", result)
        self.assertIn("IEFBR14", result)
        self.assertIn("DSORG=PO", result)
        self.assertIn("RECFM=FB", result)
        self.assertIn("LRECL=80", result)

    def test_asm_template_substitution(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "MEMBER": "HELLO",
            "SYSLIB_CONCAT": "//SYSLIB   DD DSN=SYS1.MACLIB,DISP=SHR",
            "PUNCH_DSN": "IBMUSER.HELLO370.V1R0M0.OBJECT",
            "ASM_SOURCE": "         END",
        }
        result = render_template("asm.jcl.tpl", vars_)
        self.assertIn("IFOX00", result)
        self.assertIn("HELLO", result)
        self.assertIn("SYS1.MACLIB", result)

    def test_ncallink_template_substitution(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "MEMBER": "HELLO",
            "NCALIB_DSN": "IBMUSER.HELLO370.V1R0M0.NCALIB",
            "PUNCH_DSN": "IBMUSER.HELLO370.V1R0M0.OBJECT",
        }
        result = render_template("ncallink.jcl.tpl", vars_)
        self.assertIn("IEWL", result)
        self.assertIn("NCAL", result)
        self.assertIn("IBMUSER.HELLO370.V1R0M0.NCALIB", result)

    def test_link_template_substitution(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "MODULE_NAME": "HELLO",
            "LINK_OPTIONS": "RENT,REUS,LIST,XREF",
            "SYSLMOD_DSN": "IBMUSER.HELLO370.V1R0M0.LOAD",
            "NCALIB_CONCAT": "//SYSLIB   DD DSN=IBMUSER.HELLO370.V1R0M0.NCALIB,DISP=SHR",
            "INCLUDE_STMTS": " INCLUDE SYSLIB(HELLO)",
            "ENTRY_POINT": "HELLO",
        }
        result = render_template("link.jcl.tpl", vars_)
        self.assertIn("IEWL", result)
        self.assertIn("RENT,REUS,LIST,XREF", result)
        self.assertIn("HELLO", result)
        self.assertIn("ENTRY HELLO", result)

    def test_delete_template(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "DSN": "IBMUSER.TEST.NCALIB",
        }
        result = render_template("delete.jcl.tpl", vars_)
        self.assertIn("IEFBR14", result)
        self.assertIn("IBMUSER.TEST.NCALIB", result)
        self.assertIn("DELETE,DELETE", result)

    def test_copy_template(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "SRC_DSN": "IBMUSER.SRC.NCALIB",
            "DST_DSN": "IBMUSER.DST.NCALIB",
        }
        result = render_template("copy.jcl.tpl", vars_)
        self.assertIn("IEBCOPY", result)
        self.assertIn("IBMUSER.SRC.NCALIB", result)
        self.assertIn("IBMUSER.DST.NCALIB", result)

    def test_receive_template(self):
        vars_ = {
            "JOBCARD": "//MYJOB JOB (A),'MBT',CLASS=A,MSGCLASS=H,MSGLEVEL=(1,1)",
            "XMIT_DSN": "IBMUSER.DEPS.NCALIB.XMIT",
            "TARGET_DSN": "IBMUSER.DEPS.CRENT370.V1R0M0.NCALIB",
        }
        result = render_template("receive.jcl.tpl", vars_)
        self.assertIn("IKJEFT01", result)
        self.assertIn("RECEIVE", result)
        self.assertIn("IBMUSER.DEPS.NCALIB.XMIT", result)
        self.assertIn("IBMUSER.DEPS.CRENT370.V1R0M0.NCALIB", result)

    def test_safe_substitute_leaves_unknown_vars(self):
        # safe_substitute should not raise on undefined vars
        vars_ = {
            "JOBCARD": "//JOB JOB A",
            "DSN": "TEST.DS",
            "UNIT": "SYSDA",
            "SPACE_UNIT": "TRK",
            "SPACE_PRI": "5",
            "SPACE_SEC": "2",
            # SPACE_DIR intentionally omitted
            "DSORG": "PS",
            "RECFM": "FB",
            "LRECL": "80",
            "BLKSIZE": "3120",
        }
        # Should not raise
        result = render_template("alloc.jcl.tpl", vars_)
        self.assertIn("TEST.DS", result)


class TestJclIntegration(unittest.TestCase):
    """Integration tests combining jobcard + render_syslib_concat."""

    def test_asm_jcl_with_jobcard_and_syslib(self):
        jc = jobcard("HELLO", "A", "H", "TEST")
        syslib = render_syslib_concat([
            "IBMUSER.HELLO370.V1R0M0.NCALIB",
            "IBMUSER.DEPS.CRENT370.V1R0M0.MACLIB",
            "SYS1.MACLIB",
        ])
        vars_ = {
            "JOBCARD": jc,
            "MEMBER": "HELLO",
            "SYSLIB_CONCAT": syslib,
            "PUNCH_DSN": "IBMUSER.HELLO370.V1R0M0.OBJECT",
            "ASM_SOURCE": "HELLO    CSECT\n         END",
        }
        result = render_template("asm.jcl.tpl", vars_)
        self.assertIn("//HELLO   JOB", result)
        self.assertIn("SYS1.MACLIB", result)
        self.assertIn("CRENT370", result)
        self.assertIn("IFOX00", result)


if __name__ == "__main__":
    unittest.main()
