"""mbt release executor — version bump, tag, and push.

Three scenarios (auto-detected from current vs requested version):

A. Normal dev-to-release (main case):
   Current version has -dev suffix, requested VERSION is the release.
   1. Bump version_files to VERSION, commit "release: vVERSION", tag, push.
   2. Bump to NEXT_VERSION (default: patch+1-dev), commit, push.
   3. Print message to run 'make bootstrap'.

B. Prerelease (--prerelease):
   Current version stays unchanged. Force-push tag v{current}.
   No file modifications, no version bump.

C. Rebuild existing release (tag checkout):
   Current version == VERSION (no -dev suffix). Tag + force-push only.
   No file modifications, no version bump.

Usage:
    mvsrelease.py --version 1.2.0
    mvsrelease.py --version 1.2.0 --next-version 2.0.0-dev
    mvsrelease.py --prerelease

Exit codes per spec section 11.1
"""

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
    return subprocess.run(
        ["git"] + list(args),
        capture_output=True, text=True
    )


def _next_dev_version(v: Version) -> str:
    """Default next-dev: patch+1 with -dev suffix."""
    return f"{v.major}.{v.minor}.{v.patch + 1}-dev"


def _tag_exists_locally(tag: str) -> bool:
    result = _git("tag", "-l", tag)
    return bool(result.stdout.strip())


def _tag_exists_remotely(tag: str) -> bool:
    result = _git("ls-remote", "--tags", "origin", f"refs/tags/{tag}")
    return bool(result.stdout.strip())


def _update_version_in_file(filepath: Path,
                             old_version: str,
                             new_version: str) -> bool:
    """Replace version string in a file. Returns True if modified."""
    if not filepath.exists():
        _log_error(f"Version file not found: {filepath}")
        return False
    content = filepath.read_text(encoding="utf-8")
    new_content = content.replace(old_version, new_version)
    if new_content == content:
        _log_error(f"Version '{old_version}' not found in {filepath}")
        return False
    filepath.write_text(new_content, encoding="utf-8")
    return True


def _bump_version(version_files: list[str],
                  old_version: str,
                  new_version: str) -> bool:
    """Bump version in all version_files. Returns True on success."""
    changed = []
    for vf in version_files:
        vf_path = Path(vf)
        _log(f"Updating {vf_path} ({old_version} -> {new_version})...")
        if _update_version_in_file(vf_path, old_version, new_version):
            changed.append(str(vf_path))
        else:
            return False

    result = _git("add", *changed)
    if result.returncode != 0:
        _log_error(f"git add failed: {result.stderr}")
        return False
    return True


def _git_commit(msg: str) -> bool:
    result = _git("commit", "-m", msg)
    if result.returncode != 0:
        _log_error(f"git commit failed: {result.stderr}")
        return False
    _log(f"Committed: {msg}")
    return True


def _git_tag(tag: str, force: bool = False) -> bool:
    args = ["tag"]
    if force:
        args.append("-f")
    args.append(tag)
    result = _git(*args)
    if result.returncode != 0:
        _log_error(f"git tag failed: {result.stderr}")
        return False
    _log(f"Tagged: {tag}")
    return True


def _git_push_head() -> bool:
    result = _git("push", "origin", "HEAD")
    if result.returncode != 0:
        _log_error(f"git push HEAD failed: {result.stderr}")
        return False
    return True


def _git_push_tag(tag: str, force: bool = False) -> bool:
    args = ["push", "origin"]
    if force:
        args.append("-f")
    args.append(tag)
    result = _git(*args)
    if result.returncode != 0:
        _log_error(f"git push tag {tag} failed: {result.stderr}")
        return False
    _log(f"Pushed {tag} to origin")
    return True


def _scenario_a(project, version_files: list[str],
                release_ver: str, next_ver: str) -> int:
    """Scenario A: dev-to-release bump, tag, push, bump-to-next-dev."""
    current = project.version
    tag = f"v{release_ver}"

    _log(f"Releasing {current} -> {release_ver}, next: {next_ver}")

    # Pre-check: tag must not already exist
    if _tag_exists_locally(tag):
        _log_error(
            f"Tag {tag} already exists locally.\n"
            f"[{MODULE}]        If this is a leftover from an aborted run, "
            f"delete it first:\n"
            f"[{MODULE}]          git tag -d {tag}\n"
            f"[{MODULE}]          git push origin --delete {tag}\n"
            f"[{MODULE}]        Then re-run make release VERSION={release_ver}."
        )
        return EXIT_CONFIG
    if _tag_exists_remotely(tag):
        _log_error(
            f"Tag {tag} already exists on remote.\n"
            f"[{MODULE}]        Delete it first:\n"
            f"[{MODULE}]          git push origin --delete {tag}\n"
            f"[{MODULE}]        Then re-run make release VERSION={release_ver}."
        )
        return EXIT_CONFIG

    # Step 1: bump to release version
    if not _bump_version(version_files, current, release_ver):
        return EXIT_CONFIG
    if not _git_commit(f"release: v{release_ver}"):
        return EXIT_CONFIG
    if not _git_tag(tag):
        return EXIT_CONFIG
    if not _git_push_head():
        return EXIT_CONFIG
    if not _git_push_tag(tag):
        return EXIT_CONFIG

    # Step 2: bump to next-dev version
    if not _bump_version(version_files, release_ver, next_ver):
        return EXIT_CONFIG
    if not _git_commit(f"chore: bump to {next_ver}"):
        return EXIT_CONFIG
    if not _git_push_head():
        return EXIT_CONFIG

    # Step 3: print message
    _log(f"Released {release_ver}. Now on {next_ver}.")
    _log("Run 'make bootstrap' to allocate build datasets.")
    return EXIT_SUCCESS


def _scenario_b(project) -> int:
    """Scenario B: force-push prerelease tag for current version."""
    current = project.version
    tag = f"v{current}"
    _log(f"Prerelease {tag}...")
    if not _git_tag(tag, force=True):
        return EXIT_CONFIG
    if not _git_push_tag(tag, force=True):
        return EXIT_CONFIG
    _log(f"Prerelease {tag} pushed.")
    return EXIT_SUCCESS


def _scenario_c(project, release_ver: str) -> int:
    """Scenario C: rebuild — tag + push only, no file changes."""
    tag = f"v{release_ver}"
    _log(f"Rebuilding {tag}...")
    if not _git_tag(tag, force=True):
        return EXIT_CONFIG
    if not _git_push_tag(tag, force=True):
        return EXIT_CONFIG
    _log(f"Tag {tag} pushed.")
    return EXIT_SUCCESS


def main() -> int:
    parser = argparse.ArgumentParser(
        description="mbt release — version bump, tag, and push"
    )
    parser.add_argument(
        "--project", default="project.toml",
        help="Path to project.toml (default: project.toml)",
    )
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument(
        "--version",
        help="Release version (semver, e.g. 1.2.0)",
    )
    group.add_argument(
        "--prerelease", action="store_true",
        help="Force-push prerelease tag for current version (Scenario B)",
    )
    parser.add_argument(
        "--next-version",
        help="Next dev version after release (default: patch+1-dev)",
    )
    args = parser.parse_args()

    try:
        config = MbtConfig(project_path=args.project)
    except (ProjectError, FileNotFoundError) as e:
        _log_error(str(e))
        return EXIT_CONFIG

    project = config.project

    # Check for clean working tree
    result = _git("status", "--porcelain")
    if result.stdout.strip():
        _log_error("Working tree is not clean. Commit or stash changes first.")
        return EXIT_CONFIG

    # Scenario B: prerelease
    if args.prerelease:
        return _scenario_b(project)

    # Scenarios A and C: --version required
    release_ver = args.version
    try:
        Version.parse(release_ver)
    except ValueError as e:
        _log_error(f"Invalid version '{release_ver}': {e}")
        return EXIT_CONFIG

    current = project.version
    current_v = Version.parse(current)

    version_files = project.release_version_files or [args.project]

    if current == release_ver:
        # Scenario C: already at release version
        return _scenario_c(project, release_ver)

    if current_v.pre is not None:
        # Expect current = release_ver + "-dev" (or similar prerelease)
        release_v = Version.parse(release_ver)
        if (current_v.major, current_v.minor, current_v.patch) != (
                release_v.major, release_v.minor, release_v.patch):
            _log_error(
                f"Cannot release {release_ver}: current version is {current}. "
                f"Expected {release_ver}-dev or run with a matching VERSION."
            )
            return EXIT_CONFIG
        # Scenario A
        if args.next_version:
            next_ver = args.next_version
            try:
                nv = Version.parse(next_ver)
                if nv.pre is None:
                    _log_error(
                        f"NEXT_VERSION '{next_ver}' must be a prerelease "
                        f"(e.g. {next_ver}-dev)"
                    )
                    return EXIT_CONFIG
            except ValueError as e:
                _log_error(f"Invalid NEXT_VERSION '{next_ver}': {e}")
                return EXIT_CONFIG
        else:
            next_ver = _next_dev_version(Version.parse(release_ver))
        return _scenario_a(project, version_files, release_ver, next_ver)

    _log_error(
        f"Cannot release {release_ver}: current version is '{current}'. "
        f"Expected '{release_ver}-dev' (Scenario A) or "
        f"'{release_ver}' (Scenario C)."
    )
    return EXIT_CONFIG


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
