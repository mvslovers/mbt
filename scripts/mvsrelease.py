"""mbt release executor — version bump, tag, and push.

Local-only workflow:
1. Validate new version is valid semver
2. Update version in all version_files from project.toml
3. git add changed files
4. git commit -m "Release v{version}"
5. git tag v{version}
6. git push origin HEAD --tags

CI handles the actual release build (distclean -> bootstrap ->
build -> link -> package -> GitHub Release creation).

Usage:
    mvsrelease.py 1.2.0
    mvsrelease.py --project other.toml 1.2.0

Exit codes per spec section 11.1
"""

import re
import sys
import argparse
import subprocess
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG, EXIT_INTERNAL
from mbt.config import MbtConfig
from mbt.version import Version
from mbt.project import ProjectError


MODULE = "mvsrelease"


def _log(msg: str) -> None:
    print(f"[{MODULE}] {msg}")


def _log_error(msg: str) -> None:
    print(f"[{MODULE}] ERROR: {msg}", file=sys.stderr)


def _git(*args: str) -> subprocess.CompletedProcess:
    """Run a git command and return the result."""
    result = subprocess.run(
        ["git"] + list(args),
        capture_output=True, text=True
    )
    return result


def _update_version_in_file(filepath: Path,
                            old_version: str,
                            new_version: str) -> bool:
    """Replace version string in a file.

    Handles common patterns:
    - version = "1.0.0"  (TOML)
    - "version": "1.0.0" (JSON)
    - VERSION = 1.0.0    (plain)

    Returns True if file was modified.
    """
    if not filepath.exists():
        _log_error(f"Version file not found: {filepath}")
        return False

    content = filepath.read_text(encoding="utf-8")
    new_content = content.replace(old_version, new_version)
    if new_content == content:
        _log_error(
            f"Version '{old_version}' not found in {filepath}"
        )
        return False

    filepath.write_text(new_content, encoding="utf-8")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt release — version bump, tag, and push"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Show what would be done without making changes",
    )
    parser.add_argument(
        "version",
        help="New version (semver, e.g. 1.2.0)",
    )
    args = parser.parse_args()

    # Load config
    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project
    new_version = args.version

    # Validate new version
    try:
        Version.parse(new_version)
    except ValueError as e:
        _log_error(f"Invalid version '{new_version}': {e}")
        return EXIT_CONFIG

    old_version = project.version
    tag = f"v{new_version}"

    _log(f"Releasing {project.name} {old_version} -> {new_version}")

    # Check for clean working tree
    result = _git("status", "--porcelain")
    if result.stdout.strip():
        _log_error("Working tree is not clean. Commit or stash changes first.")
        return EXIT_CONFIG

    # Check that tag doesn't already exist
    result = _git("tag", "-l", tag)
    if result.stdout.strip():
        _log_error(f"Tag '{tag}' already exists.")
        return EXIT_CONFIG

    # Get version files to update
    version_files = project.release_version_files
    if not version_files:
        version_files = [args.project]

    if args.dry_run:
        _log(f"Would update version in: {', '.join(version_files)}")
        _log(f"Would commit: 'Release {tag}'")
        _log(f"Would tag: {tag}")
        _log("Would push to origin")
        return EXIT_SUCCESS

    # Update version in all version files
    changed_files = []
    for vf in version_files:
        vf_path = Path(vf)
        _log(f"Updating {vf_path}...")
        if _update_version_in_file(vf_path, old_version, new_version):
            changed_files.append(str(vf_path))
        else:
            _log_error(f"Failed to update version in {vf_path}")
            return EXIT_CONFIG

    # Git add
    result = _git("add", *changed_files)
    if result.returncode != 0:
        _log_error(f"git add failed: {result.stderr}")
        return EXIT_CONFIG

    # Git commit
    commit_msg = f"Release {tag}"
    result = _git("commit", "-m", commit_msg)
    if result.returncode != 0:
        _log_error(f"git commit failed: {result.stderr}")
        return EXIT_CONFIG
    _log(f"Committed: {commit_msg}")

    # Git tag
    result = _git("tag", tag)
    if result.returncode != 0:
        _log_error(f"git tag failed: {result.stderr}")
        return EXIT_CONFIG
    _log(f"Tagged: {tag}")

    # Git push
    result = _git("push", "origin", "HEAD", "--tags")
    if result.returncode != 0:
        _log_error(f"git push failed: {result.stderr}")
        return EXIT_CONFIG
    _log(f"Pushed to origin")

    _log(f"Release {tag} complete.")
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
