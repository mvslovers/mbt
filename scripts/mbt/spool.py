"""Reading a job's spool output.

Text-level helpers shared by the executors that submit a job and have to say
what went wrong when it did not come back clean.  Pure string handling -- no
client, no filesystem -- so it stays testable without MVS.
"""

import re

# JES rejected the job at conversion: not one step was attached, so no step has
# an IEF142I/IEF450I/IEF272I line.  Deliberately not anchored on the jobname --
# submit_jcl falls back to "UNKNOWN" when the submit response omits it.
JCL_ERROR_RE = re.compile(
    r"^.*IEF452I\s+(\S+)\s+JOB NOT RUN\s+-\s+JCL ERROR.*$", re.M)

# The converter/interpreter diagnostics that name the reason.  The interpreter
# prints them in a "STMT NO. MESSAGE" table in JESYSMSG,
#
#          26 IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD
#
# so the statement number sits left of the message; keep it, it points into the
# generated JCL.  In JESMSGLG the same message can appear with a JES time / job
# prefix instead, which is not worth reprinting.
_JCL_DIAG_RE = re.compile(r"^(?P<pre>.*?)(?P<msg>IEF6\d\dI\b.*)$", re.M)
_STMT_RE = re.compile(r"^\s*(\d+)\s*$")
MAX_DIAG = 5


def jcl_diagnostics(spool: str) -> list:
    """The IEF6nnI lines from a rejected job, deduplicated and capped.

    Unfiltered on purpose: IEF653I (substituted JCL) and IEF677I (warnings)
    live in the same range and can be noise, but a couple of noisy lines
    inside a JCL-error report cost far less than dropping the one line that
    names the cause.
    """
    out, seen = [], set()
    for m in _JCL_DIAG_RE.finditer(spool):
        line = m.group("msg").rstrip()
        stmt = _STMT_RE.match(m.group("pre"))
        if stmt:
            line = f"{line}    (STMT {stmt.group(1)})"
        if line in seen:
            continue
        seen.add(line)
        out.append(line)
        if len(out) >= MAX_DIAG:
            break
    return out
