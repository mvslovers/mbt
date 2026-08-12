"""cc370 sysroot discovery and the libc370 version installed in it.

The sysroot carries no version marker -- libc370's `make install` copies
headers, macros, libc.a and the crt objects and nothing else, and the
installed clibver.h only declares `libc370_version()`. The version is only
reachable through the build stamp baked into the archive, EBCDIC-encoded
because it is a target string constant:

    LIBC370 1.0.2-dev (5c0deeb)

so reading it needs no change to libc370 and no sysroot reinstall.
"""

import re
import shutil
from pathlib import Path

# The stamp is `"LIBC370 " VERSION " (" REV ")"` (libc370 src/clib/@@ver.c),
# but that spelling has moved before -- libc370 5c0deeb is "uppercase the
# build stamp and drop the 'v' before the version". Stay tolerant about case
# and a leading 'v', and let the caller treat "not found" as unknown rather
# than as a failure.
_STAMP_RE = re.compile(
    r"libc370\s+v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)*)", re.IGNORECASE
)

# EBCDIC first: the archive holds target code. Latin-1 as a fallback so a
# host-encoded stamp would still be found if libc370 ever emits one.
_ENCODINGS = ("cp037", "latin-1")


def derive_sysroot() -> Path | None:
    """Locate the cc370 sysroot the same way mk/mbt.mk does.

    cc370 resolves its own headers/libs relative to its binary
    (<bindir>/../cc370); 'cc370 -print-search-dirs' reports the
    configure-time prefix and is wrong for a relocated toolchain.
    Falls back to ~/.local/cc370.

    Returns:
        The sysroot path, or None if neither candidate holds crt0.o
    """
    cc = shutil.which("cc370")
    if cc:
        candidate = Path(cc).resolve().parent.parent / "cc370"
        if (candidate / "lib" / "crt0.o").exists():
            return candidate
    fallback = Path.home() / ".local" / "cc370"
    if (fallback / "lib" / "crt0.o").exists():
        return fallback
    return None


def stamp_version(data: bytes) -> str | None:
    """Extract the libc370 version from raw archive bytes.

    Args:
        data: contents of a libc.a

    Returns:
        The version string ('1.0.2-dev'), or None if no stamp is present
    """
    for encoding in _ENCODINGS:
        match = _STAMP_RE.search(data.decode(encoding, errors="replace"))
        if match:
            return match.group(1)
    return None


def installed_libc370(sysroot: str | Path) -> str | None:
    """Return the libc370 version installed in a sysroot.

    Args:
        sysroot: cc370 sysroot directory

    Returns:
        The version string, or None if libc.a is unreadable or unstamped
    """
    try:
        data = (Path(sysroot) / "lib" / "libc.a").read_bytes()
    except OSError:
        return None
    return stamp_version(data)
