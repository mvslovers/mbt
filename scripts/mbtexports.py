"""mbt exports — show exported members for a cached dependency.

Reads the cached package.toml for a dependency and prints its
[link] exports list. Requires 'make bootstrap' to have been run.

Usage:
    mbtexports.py --dep mvslovers/lua370
    mbtexports.py --dep mvslovers/lua370 --project project.toml

Exit codes per spec section 11.1
"""

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG, EXIT_DEPENDENCY
from mbt.config import MbtConfig
from mbt.dependencies import load_package_toml
from mbt.lockfile import Lockfile
from mbt.project import ProjectError


MODULE = "mbtexports"


def _log_error(msg: str) -> None:
    print(f"[{MODULE}] ERROR: {msg}", file=sys.stderr)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt exports — show exported members for a dependency"
    )
    parser.add_argument(
        "--dep", required=True,
        help="Dependency key, e.g. mvslovers/lua370",
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)",
    )
    args = parser.parse_args()

    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    dep_key = args.dep
    if dep_key not in config.project.dependencies:
        _log_error(
            f"'{dep_key}' is not declared in [dependencies] "
            f"of {args.project}"
        )
        return EXIT_CONFIG

    lockfile = Lockfile.load(Path(".mbt") / "mvs.lock")
    if not lockfile or dep_key not in lockfile.dependencies:
        _log_error(
            f"'{dep_key}' not in lockfile. Run 'make bootstrap' first."
        )
        return EXIT_DEPENDENCY

    dep_version = lockfile.dependencies[dep_key]
    owner, repo = dep_key.split("/", 1)
    pkg = load_package_toml(owner, repo, dep_version)

    if not pkg:
        _log_error(
            f"package.toml for {dep_key}@{dep_version} not cached. "
            f"Run 'make bootstrap' first."
        )
        return EXIT_DEPENDENCY

    link = pkg.get("link", {})
    if link.get("autocall", True):
        print(f"{dep_key} is autocall-compatible. No exports list.")
        return EXIT_SUCCESS

    exports = link.get("exports", [])
    if not exports:
        print(f"{dep_key}: autocall = false but no exports defined.")
        return EXIT_SUCCESS

    print(f"{dep_key}@{dep_version} exports ({len(exports)} members):")
    for member in exports:
        print(f"  {member}")

    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[{MODULE}] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
