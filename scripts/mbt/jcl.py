"""JCL template rendering.

Uses string.Template for variable substitution and
Python helper functions for dynamic sections (SYSLIB concat).

Templates are .tpl files in mbt/templates/jcl/.
Variables use $NAME or ${NAME} syntax.
"""

from string import Template
from pathlib import Path


# Template directory relative to this file:
# scripts/mbt/jcl.py → ../../templates/jcl/
_TEMPLATE_DIR = Path(__file__).parent.parent.parent / "templates" / "jcl"


def render_template(template_name: str,
                    variables: dict[str, str]) -> str:
    """Render a JCL template with variables.

    Args:
        template_name: e.g. "asm.jcl.tpl"
        variables: Template variables (string.Template safe_substitute)

    Returns:
        Rendered JCL text
    """
    tpl_path = _TEMPLATE_DIR / template_name
    tpl = Template(tpl_path.read_text(encoding="utf-8"))
    return tpl.safe_substitute(variables)


def render_syslib_concat(datasets: list[str]) -> str:
    """Generate SYSLIB DD concatenation JCL fragment.

    Args:
        datasets: List of fully qualified dataset names

    Returns:
        JCL fragment:
          //SYSLIB   DD DSN=first,DISP=SHR
          //         DD DSN=second,DISP=SHR

        When empty:
          //SYSLIB   DD DUMMY
    """
    if not datasets:
        return "//SYSLIB   DD DUMMY"
    lines = [f"//SYSLIB   DD DSN={datasets[0]},DISP=SHR"]
    for dsn in datasets[1:]:
        lines.append(f"//         DD DSN={dsn},DISP=SHR")
    return "\n".join(lines)


def render_include_concat(members: list[str],
                          dsn: str) -> str:
    """Generate INCLUDE statements for IEWL linkedit.

    Args:
        members: List of member names to include
        dsn: Dataset name (last qualifier used as IEWL DD alias)
              or a DD name directly (if no dots present)

    Returns:
        JCL control card fragment:
          INCLUDE NCALIB(MEMBER1)
          INCLUDE NCALIB(MEMBER2)

    Note:
        The DD alias is derived from the last qualifier of dsn.
        e.g. "IBMUSER.HELLO370.V1R0M0.NCALIB" → alias "NCALIB"
        or   "SYSLIB" → alias "SYSLIB"
    """
    # Derive DD alias: last qualifier of DSN, or whole string if no dots
    alias = dsn.split(".")[-1] if "." in dsn else dsn
    lines = []
    for member in members:
        lines.append(f" INCLUDE {alias}({member})")
    return "\n".join(lines)


def jobcard(jobname: str, jobclass: str,
            msgclass: str, description: str = "MBT"
            ) -> str:
    """Generate a standard JOB card.

    Jobname is truncated to 8 characters (MVS limit).

    Returns:
        Multi-line JOB card string (no trailing newline)
    """
    jn = jobname[:8].upper()
    # JCL: name field is cols 3-10 (8 chars). If < 8 chars, ljust(8) provides
    # natural padding; if exactly 8 chars, must add explicit separator space.
    jn_field = jn.ljust(8) if len(jn) < 8 else jn + " "
    return (
        f"//{jn_field}JOB ({jobclass}),'{description}',\n"
        f"//          CLASS={jobclass},"
        f"MSGCLASS={msgclass},\n"
        f"//          MSGLEVEL=(1,1)"
    )
