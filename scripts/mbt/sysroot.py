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


def libc_dir(sysroot: str | Path) -> Path | None:
    """Where libc370's crt*.o and libc.a are, below a cc370 sysroot.

    In cc370's own tree (<sysroot>/lib), or from cc370 1.2.0 in its second
    sysroot <sysroot>/libc370/lib, searched after the first (cc370#726, #144).
    Mirrors LIBCDIR in mk/mbt.mk.
    """
    for d in (Path(sysroot) / "lib", Path(sysroot) / "libc370" / "lib"):
        if (d / "crt0.o").exists():
            return d
    return None


def derive_sysroot() -> Path | None:
    """Locate the cc370 sysroot the same way mk/mbt.mk does.

    cc370 resolves its own headers/libs relative to its binary
    (<bindir>/../cc370), following symlinks; 'cc370 -print-search-dirs'
    reports the configure-time prefix and is wrong for a relocated toolchain.
    The sysroot counts when libc370 is in it or in its second sysroot.
    Falls back to ~/.local/cc370.

    Returns:
        The sysroot path, or None if no candidate holds crt0.o
    """
    cc = shutil.which("cc370")
    if cc:
        candidate = Path(cc).resolve().parent.parent / "cc370"
        if libc_dir(candidate) is not None:
            return candidate
    fallback = Path.home() / ".local" / "cc370"
    if libc_dir(fallback) is not None:
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
    d = libc_dir(sysroot) or Path(sysroot) / "lib"
    try:
        data = (d / "libc.a").read_bytes()
    except OSError:
        return None
    return stamp_version(data)
