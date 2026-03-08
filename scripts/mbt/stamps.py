"""Incremental build stamps.

SHA256-based change detection for source files. Stamps are stored
in .mbt/stamps/ as small text files containing hex digests.

See spec appendix A.5 for the full design.
"""

import hashlib
from pathlib import Path

STAMP_DIR = Path(".mbt") / "stamps"


def compute_hash(file_path: Path) -> str:
    """SHA256 hex digest of file contents."""
    h = hashlib.sha256()
    h.update(file_path.read_bytes())
    return h.hexdigest()


def read_stamp(member: str, phase: str) -> str | None:
    """Read stamp for member/phase, return hash or None.

    Args:
        member: Module name (e.g. "HELLO")
        phase: Build phase ("compile", "asm")
    """
    stamp_file = STAMP_DIR / f"{member}.{phase}.sha256"
    if stamp_file.is_file():
        return stamp_file.read_text(encoding="utf-8").strip()
    return None


def write_stamp(member: str, phase: str, hash_value: str) -> None:
    """Write stamp for member/phase."""
    STAMP_DIR.mkdir(parents=True, exist_ok=True)
    stamp_file = STAMP_DIR / f"{member}.{phase}.sha256"
    stamp_file.write_text(hash_value, encoding="utf-8")


def needs_build(file_path: Path, member: str, phase: str) -> bool:
    """Return True if file changed since last successful build."""
    current = compute_hash(file_path)
    previous = read_stamp(member, phase)
    return current != previous
