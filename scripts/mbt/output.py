"""Output formatters for mbtconfig.

Supports multiple output formats:
- shell: KEY=VALUE lines for Make $(eval ...)
- json:  JSON object for debugging/scripting
- doctor: human-readable diagnostics
"""

import json
import sys


def format_shell(variables: dict[str, str]) -> str:
    """Format as shell variable assignments.

    Output is designed for Make's $(eval ...) function:
        PROJECT_NAME=httpd
        PROJECT_VERSION=3.3.1-dev
        BUILD_DS_NCALIB=IBMUSER.HTTPD.V3R3M1.NCALIB

    Rules:
    - No quoting (Make doesn't need it for simple values)
    - Lists are space-separated
    - One variable per line
    """
    lines = []
    for key, value in variables.items():
        if isinstance(value, list):
            value = " ".join(str(v) for v in value)
        lines.append(f"{key}={value}")
    return "\n".join(lines)


def format_json(variables: dict) -> str:
    """Format as JSON for debugging."""
    return json.dumps(variables, indent=2)


def format_doctor(sourced_values: dict) -> str:
    """Format config diagnostics showing value sources.

    Example output:
        [mbt] Configuration:
          MVS_HOST     = localhost        [default]
          MVS_PORT     = 1080             [~/.mbt/config.toml]
          MVS_HLQ      = CIUSER          [env]
    """
    lines = ["[mbt] Configuration:"]
    if not sourced_values:
        return "\n".join(lines)
    max_key_len = max(len(k) for k in sourced_values)
    for key, cs in sourced_values.items():
        padded_key = key.ljust(max_key_len)
        lines.append(f"  {padded_key} = {cs.value:<20} [{cs.source}]")
    return "\n".join(lines)
