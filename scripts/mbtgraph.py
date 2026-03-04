"""mbt graph — dependency tree display.

Reads lockfile + cached package.toml files to build
and display the full dependency tree:

    httpd v3.3.1-dev
     ├─ crent370 v1.0.0
     ├─ ufs370 v1.0.0
     │   └─ crent370 v1.0.0
     ├─ lua370 v1.0.0
     │   └─ crent370 v1.0.0
     └─ mqtt370 v1.0.0
         ├─ crent370 v1.0.0
         └─ lua370 v1.0.0

Transitive dependencies are read from cached package.toml files
in ~/.mbt/cache/. If a package.toml is not cached, the dependency
is shown as a leaf node.

Usage:
    mbtgraph.py
    mbtgraph.py --project other.toml

Exit codes per spec section 11.1
"""

import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG, EXIT_INTERNAL
from mbt.config import MbtConfig
from mbt.lockfile import Lockfile
from mbt.dependencies import load_package_toml
from mbt.project import ProjectError


MODULE = "mbtgraph"


def _log(msg: str) -> None:
    print(f"[{MODULE}] {msg}")


def _log_error(msg: str) -> None:
    print(f"[{MODULE}] ERROR: {msg}", file=sys.stderr)


def _build_dep_tree(dep_key: str, dep_version: str,
                    pkg_cache: dict[str, dict],
                    visited: set[str] | None = None) -> list[tuple[str, str, list]]:
    """Build a tree of (dep_name, version, children) tuples.

    Args:
        dep_key: "owner/repo" key
        dep_version: Exact resolved version
        pkg_cache: {dep_key: parsed package.toml dict}
        visited: Cycle detection set

    Returns:
        List of (short_name, version, children) where children
        has the same structure recursively.
    """
    if visited is None:
        visited = set()

    # Cycle detection
    cycle_key = f"{dep_key}@{dep_version}"
    if cycle_key in visited:
        return []
    visited = visited | {cycle_key}

    pkg = pkg_cache.get(dep_key, {})
    sub_deps = pkg.get("package", {}).get("dependencies", {})

    children = []
    for sub_key, sub_ver in sub_deps.items():
        sub_name = sub_key.split("/")[-1]
        grandchildren = _build_dep_tree(sub_key, sub_ver, pkg_cache, visited)
        children.append((sub_name, sub_ver, grandchildren))

    return children


def _print_tree(name: str, version: str,
                children: list[tuple[str, str, list]],
                prefix: str = "", is_root: bool = True) -> None:
    """Print a dependency tree with box-drawing characters.

    Args:
        name: Display name
        version: Version string
        children: List of (name, version, children) tuples
        prefix: Line prefix for indentation
        is_root: True for the root node
    """
    if is_root:
        print(f"{name} v{version}")
    for i, (child_name, child_ver, grandchildren) in enumerate(children):
        is_last = (i == len(children) - 1)
        connector = "└─" if is_last else "├─"
        print(f"{prefix} {connector} {child_name} v{child_ver}")
        child_prefix = prefix + ("   " if is_last else " │ ")
        _print_tree(child_name, child_ver, grandchildren,
                    child_prefix, is_root=False)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt graph — display dependency tree"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)"
    )
    args = parser.parse_args()

    # Load config
    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project

    # Load lockfile
    lockfile = Lockfile.load()
    if lockfile is None and project.dependencies:
        _log_error(
            "No lockfile found. Run 'make bootstrap' first."
        )
        return EXIT_CONFIG

    resolved = lockfile.dependencies if lockfile else {}

    # Load package.toml for each resolved dep from cache
    pkg_cache: dict[str, dict] = {}
    for dep_key, dep_version in resolved.items():
        owner, repo = dep_key.split("/", 1)
        pkg = load_package_toml(owner, repo, dep_version)
        if pkg:
            pkg_cache[dep_key] = pkg

    # Build the tree for each direct dependency
    children = []
    for dep_key in project.dependencies:
        dep_version = resolved.get(dep_key, "?")
        dep_name = dep_key.split("/")[-1]
        grandchildren = _build_dep_tree(dep_key, dep_version, pkg_cache)
        children.append((dep_name, dep_version, grandchildren))

    # Print
    _print_tree(project.name, project.version, children)

    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ProjectError as e:
        _log_error(str(e))
        sys.exit(EXIT_CONFIG)
    except Exception as e:
        _log_error(f"Internal error: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(EXIT_INTERNAL)
