"""mbt v2 doctor — environment verification for the cc370 toolchain.

Checks (in order):
1. Python version >= 3.12
2. cc370 / as370 / ld370 / ar370 on PATH
3. Sysroot complete (crt0.o, crt1.o, crtm.o, libc.a)
4. make on PATH
5. MVS host reachable (HTTP GET)          -- needed for `make deploy`
6. MVS credentials valid                  -- needed for `make deploy`
7. project.toml valid + config source report

Exit codes:
  0 = all checks passed
  2 = one or more checks failed

Unlike the host build (compile/link/package), `make deploy` talks to MVS,
so the MVS checks are reported but a host-only workflow can ignore them.
"""

import os
import sys
import shutil
import base64
import http.client
import urllib.request
import urllib.error
from pathlib import Path

# Force HTTP/1.0 — mvsMF's HTTPD speaks HTTP/1.0 only
http.client.HTTPConnection._http_vsn = 10
http.client.HTTPConnection._http_vsn_str = "HTTP/1.0"

# Add scripts/ dir to path so the 'mbt' package is importable
sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG
from mbt.config import MbtConfig, _ENV_MAP
from mbt.output import format_doctor

# Toolchain programs the v2 build invokes (see mk/mbt.mk).
TOOLCHAIN = ["cc370", "as370", "ld370", "ar370"]

# Runtime objects/libraries the linker needs from the sysroot.
SYSROOT_FILES = ["lib/crt0.o", "lib/crt1.o", "lib/crtm.o", "lib/libc.a"]


def check_python_version() -> bool:
    """Check Python version >= 3.12."""
    ok = sys.version_info >= (3, 12)
    version = (
        f"{sys.version_info.major}.{sys.version_info.minor}"
        f".{sys.version_info.micro}"
    )
    if ok:
        print(f"[mbt] Python {version} OK")
    else:
        print(
            f"[mbt] ERROR: Python {version} < 3.12 required",
            file=sys.stderr,
        )
    return ok


def check_tool(name: str) -> bool:
    """Check if a tool is on PATH."""
    path = shutil.which(name)
    if path:
        print(f"[mbt] {name}: {path}")
        return True
    print(f"[mbt] ERROR: {name} not found on PATH", file=sys.stderr)
    return False


def _derive_sysroot() -> Path | None:
    """Locate the cc370 sysroot the same way mk/mbt.mk does.

    cc370 resolves its own headers/libs relative to its binary
    (<bindir>/../cc370); 'cc370 -print-search-dirs' reports the
    configure-time prefix and is wrong for a relocated toolchain.
    Falls back to ~/.local/cc370.
    """
    cc = shutil.which("cc370")
    if cc:
        candidate = (Path(cc).resolve().parent.parent / "cc370")
        if (candidate / "lib" / "crt0.o").exists():
            return candidate
    fallback = Path.home() / ".local" / "cc370"
    if (fallback / "lib" / "crt0.o").exists():
        return fallback
    return None


def check_sysroot() -> bool:
    """Check the cc370 sysroot provides crt objects and libc."""
    sysroot = _derive_sysroot()
    if sysroot is None:
        print(
            "[mbt] ERROR: cc370 sysroot not found "
            "(no lib/crt0.o under <cc370>/../cc370 or ~/.local/cc370)",
            file=sys.stderr,
        )
        return False
    missing = [f for f in SYSROOT_FILES if not (sysroot / f).exists()]
    if missing:
        print(
            f"[mbt] ERROR: sysroot {sysroot} incomplete, missing: "
            f"{', '.join(missing)}",
            file=sys.stderr,
        )
        return False
    print(f"[mbt] sysroot: {sysroot} (crt0/crt1/crtm + libc.a OK)")
    return True


def check_mvs_host(config: MbtConfig) -> bool:
    """Check if MVS host is reachable via HTTP (needed for deploy)."""
    host = config.mvs_host
    port = config.mvs_port
    url = f"http://{host}:{port}/zosmf/info"
    try:
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=5) as resp:
            print(f"[mbt] MVS host reachable: {host}:{port} (HTTP {resp.status})")
            return True
    except urllib.error.HTTPError as e:
        # Any HTTP response means the server is reachable
        print(f"[mbt] MVS host reachable: {host}:{port} (HTTP {e.code})")
        return True
    except Exception as e:
        print(
            f"[mbt] WARNING: MVS host not reachable: {host}:{port} — {e} "
            f"(only needed for 'make deploy')",
            file=sys.stderr,
        )
        return False


def check_mvs_credentials(config: MbtConfig) -> bool:
    """Check MVS credentials against the jobs endpoint (needed for deploy)."""
    host = config.mvs_host
    port = config.mvs_port
    user = config.mvs_user
    password = config.mvs_pass

    auth = base64.b64encode(f"{user}:{password}".encode()).decode()
    url = f"http://{host}:{port}/zosmf/restjobs/jobs"
    try:
        req = urllib.request.Request(url)
        req.add_header("Authorization", f"Basic {auth}")
        req.add_header("Accept", "application/json")
        with urllib.request.urlopen(req, timeout=5) as resp:
            print(f"[mbt] MVS credentials valid: {user}")
            return True
    except urllib.error.HTTPError as e:
        if e.code == 401:
            print(
                f"[mbt] ERROR: MVS credentials invalid for {user} (HTTP 401)",
                file=sys.stderr,
            )
            return False
        # Other HTTP errors may still indicate the server is up
        print(f"[mbt] MVS credentials check: HTTP {e.code} for {user}")
        return True
    except Exception as e:
        print(
            f"[mbt] WARNING: Cannot verify MVS credentials: {e} "
            f"(only needed for 'make deploy')",
            file=sys.stderr,
        )
        return False


def main() -> int:
    import argparse
    parser = argparse.ArgumentParser(description="mbt v2 doctor")
    parser.add_argument("--project", default="project.toml")
    args = parser.parse_args()

    print("[mbt] Running environment checks (v2 / cc370)...")
    results = []

    # Host toolchain checks — these gate the build itself
    results.append(check_python_version())
    for tool in TOOLCHAIN:
        results.append(check_tool(tool))
    results.append(check_sysroot())
    results.append(check_tool("make"))

    # Load config for MVS connectivity checks (needed for `make deploy`)
    config = None
    try:
        config = MbtConfig(project_path=args.project)
    except Exception as e:
        print(f"[mbt] WARNING: cannot load {args.project}: {e}", file=sys.stderr)

    if config is not None:
        results.append(check_mvs_host(config))
        results.append(check_mvs_credentials(config))
        print(
            f"[mbt] project.toml valid: "
            f"{config.project.name} v{config.project.version}"
        )
        # Config source report
        sourced = {
            env_name.replace("MBT_", ""): config.get_sourced(config_key)
            for config_key, env_name in _ENV_MAP.items()
        }
        print(format_doctor(sourced))
    else:
        print(
            "[mbt] WARNING: project.toml not loaded, skipping MVS checks",
            file=sys.stderr,
        )
        results.append(False)

    failed = sum(1 for r in results if not r)
    if failed:
        print(f"[mbt] {failed} check(s) failed", file=sys.stderr)
        return EXIT_CONFIG

    print("[mbt] All checks passed")
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
