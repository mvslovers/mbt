"""mbt doctor — environment verification.

Checks (in order):
1. Python version >= 3.12
2. c2asm370 on PATH
3. make on PATH
4. MVS host reachable (HTTP GET)
5. MVS credentials valid
6. project.toml valid
7. Config source report

Exit codes:
  0 = all checks passed
  2 = one or more checks failed
"""

import sys
import os
import http.client
import shutil
import urllib.request
import urllib.error
import base64
from pathlib import Path

# Force HTTP/1.0 — mvsMF's HTTPD speaks HTTP/1.0 only
http.client.HTTPConnection._http_vsn = 10
http.client.HTTPConnection._http_vsn_str = "HTTP/1.0"

# Add scripts/ dir to path so 'mbt' package is importable
sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG
from mbt.config import MbtConfig, _ENV_MAP
from mbt.project import ProjectError
from mbt.output import format_doctor


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
    print(f"[mbt] WARNING: {name} not found on PATH", file=sys.stderr)
    return False


def check_mvs_host(config: MbtConfig) -> bool:
    """Check if MVS host is reachable via HTTP."""
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
            f"[mbt] WARNING: MVS host not reachable: {host}:{port} — {e}",
            file=sys.stderr,
        )
        return False


def check_mvs_credentials(config: MbtConfig) -> bool:
    """Check MVS credentials against the jobs endpoint."""
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
        # Other HTTP errors may still indicate server is up
        print(f"[mbt] MVS credentials check: HTTP {e.code} for {user}")
        return True
    except Exception as e:
        print(
            f"[mbt] WARNING: Cannot verify MVS credentials: {e}",
            file=sys.stderr,
        )
        return False


def check_project_toml(project_path: str = "project.toml") -> bool:
    """Validate project.toml."""
    try:
        config = MbtConfig(project_path=project_path)
        print(
            f"[mbt] project.toml valid: "
            f"{config.project.name} v{config.project.version}"
        )
        return True
    except FileNotFoundError:
        print(f"[mbt] WARNING: {project_path} not found", file=sys.stderr)
        return False
    except ProjectError as e:
        print(f"[mbt] ERROR: {e}", file=sys.stderr)
        return False


def main() -> int:
    print("[mbt] Running environment checks...")
    results = []

    # Checks that don't need project.toml
    results.append(check_python_version())
    results.append(check_tool("c2asm370"))
    results.append(check_tool("make"))

    # Load config for MVS connectivity checks
    config = None
    try:
        config = MbtConfig(project_path="project.toml")
    except Exception:
        pass

    if config is not None:
        results.append(check_mvs_host(config))
        results.append(check_mvs_credentials(config))
        results.append(True)  # project.toml already validated by MbtConfig above
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
        print("[mbt] WARNING: project.toml not found, skipping MVS checks")
        results.append(check_project_toml())

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
