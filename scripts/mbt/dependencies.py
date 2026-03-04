"""Dependency resolution via GitHub Releases.

Queries the GitHub Releases API to find the highest
version satisfying each dependency constraint. Downloads
release assets and manages the local cache.
"""

import json
import tarfile
import tomllib
import urllib.request
import urllib.error
from pathlib import Path
from dataclasses import dataclass, field

from .version import Version, satisfies

CACHE_DIR = Path.home() / ".mbt" / "cache"

# GitHub API base URL
_GH_API = "https://api.github.com"


class DependencyError(Exception):
    """Raised when dependency resolution or download fails."""
    pass


@dataclass
class ResolvedDependency:
    """A resolved dependency with download URLs."""
    owner: str
    repo: str
    version: str
    assets: dict[str, str] = field(default_factory=dict)
    # {"package.toml": url, "name-ver-headers.tar.gz": url, ...}


def resolve_dependencies(
    declared: dict[str, str],
    lockfile=None,
    update: bool = False
) -> dict[str, str]:
    """Resolve dependency versions.

    If lockfile exists and update=False, use pinned versions.
    Otherwise, query GitHub API for latest matching versions.

    Args:
        declared: {"mvslovers/crent370": ">=1.0.0", ...}
        lockfile: Existing Lockfile instance (may be None)
        update: If True, ignore lockfile and re-resolve

    Returns:
        {"mvslovers/crent370": "1.0.0", ...} exact versions

    Raises:
        DependencyError: If resolution fails
    """
    if lockfile is not None and not update:
        # Use pinned versions from lockfile
        return dict(lockfile.dependencies)

    resolved = {}
    for dep_key, constraint in declared.items():
        owner, repo = dep_key.split("/", 1)
        version = _resolve_one(owner, repo, constraint)
        resolved[dep_key] = version
    return resolved


def _resolve_one(owner: str, repo: str,
                 constraint: str) -> str:
    """Query GitHub API and return highest version matching constraint.

    Raises:
        DependencyError: If no matching release found or API fails
    """
    url = f"{_GH_API}/repos/{owner}/{repo}/releases"
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "mbt/1.0.0")

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            releases = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise DependencyError(
            f"GitHub API error for {owner}/{repo}: HTTP {e.code}"
        )
    except urllib.error.URLError as e:
        raise DependencyError(
            f"Cannot reach GitHub API for {owner}/{repo}: {e.reason}"
        )

    # Collect versions that satisfy the constraint
    candidates = []
    for release in releases:
        if release.get("draft") or release.get("prerelease"):
            continue
        tag = release.get("tag_name", "")
        # Strip leading "v" from tag name
        ver_str = tag.lstrip("v")
        try:
            ver = Version.parse(ver_str)
        except ValueError:
            continue
        if satisfies(ver_str, constraint):
            candidates.append(ver)

    if not candidates:
        raise DependencyError(
            f"No release of {owner}/{repo} satisfies {constraint!r}"
        )

    # Return highest matching version
    best = max(candidates)
    return str(best)


def download_dependency(owner: str, repo: str,
                        version: str) -> Path:
    """Download dependency assets to cache.

    Cache structure:
        ~/.mbt/cache/{owner}/{repo}/{version}/
            package.toml
            {name}-{version}-headers.tar.gz
            {name}-{version}-mvs.tar.gz

    Skips download if cache is already populated.

    Returns:
        Path to cache directory

    Raises:
        DependencyError: If download fails
    """
    cache_dir = CACHE_DIR / owner / repo / version
    if cache_dir.exists() and any(cache_dir.iterdir()):
        return cache_dir

    cache_dir.mkdir(parents=True, exist_ok=True)

    # Fetch release by tag
    tag = f"v{version}"
    url = f"{_GH_API}/repos/{owner}/{repo}/releases/tags/{tag}"
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("User-Agent", "mbt/1.0.0")

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            release = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        raise DependencyError(
            f"Cannot find release {tag} for {owner}/{repo}: HTTP {e.code}"
        )
    except urllib.error.URLError as e:
        raise DependencyError(
            f"Cannot reach GitHub for {owner}/{repo}: {e.reason}"
        )

    assets = release.get("assets", [])
    for asset in assets:
        name = asset.get("name", "")
        download_url = asset.get("browser_download_url", "")
        if not name or not download_url:
            continue
        _download_file(download_url, cache_dir / name)

    return cache_dir


def _download_file(url: str, dest: Path) -> None:
    """Download a file from url to dest path.

    Raises:
        DependencyError: If download fails
    """
    req = urllib.request.Request(url)
    req.add_header("User-Agent", "mbt/1.0.0")
    req.add_header("Accept", "application/octet-stream")
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            data = resp.read()
    except urllib.error.HTTPError as e:
        raise DependencyError(
            f"Download failed for {url}: HTTP {e.code}"
        )
    except urllib.error.URLError as e:
        raise DependencyError(
            f"Download failed for {url}: {e.reason}"
        )
    dest.write_bytes(data)


def extract_headers(cache_dir: Path,
                    dep_name: str,
                    dep_version: str) -> Path:
    """Extract headers tarball to contrib/.

    Looks for {dep_name}-{dep_version}-headers.tar.gz in cache_dir.
    Extracts to: contrib/{dep_name}-{dep_version}/include/

    Args:
        cache_dir: Path to the cached dependency directory
        dep_name: Dependency short name, e.g. "crent370"
        dep_version: Exact version string, e.g. "1.0.0"

    Returns:
        Path to include directory (contrib/{name}-{ver}/include/)

    Raises:
        DependencyError: If tarball not found
    """
    tarball_name = f"{dep_name}-{dep_version}-headers.tar.gz"
    tarball = cache_dir / tarball_name
    if not tarball.exists():
        raise DependencyError(
            f"Headers tarball not found: {tarball}"
        )

    dest_dir = Path("contrib") / f"{dep_name}-{dep_version}"

    # Skip if already correctly extracted
    include_dir = dest_dir / "include"
    if include_dir.is_dir() and any(include_dir.iterdir()):
        return include_dir

    # Clean up any partial or wrongly-nested extraction
    if dest_dir.exists():
        import shutil
        shutil.rmtree(dest_dir)
    dest_dir.mkdir(parents=True, exist_ok=True)

    with tarfile.open(tarball, "r:gz") as tf:
        members = tf.getmembers()
        # Detect top-level directory prefix inside the archive
        prefix = None
        for m in members:
            parts = Path(m.name).parts
            if parts:
                prefix = parts[0]
                break
        # Extract stripping the top-level prefix
        for m in members:
            parts = Path(m.name).parts
            if prefix and parts and parts[0] == prefix:
                if len(parts) == 1:
                    continue  # skip the prefix dir entry itself
                m.name = str(Path(*parts[1:]))
            tf.extract(m, path=dest_dir)

    if not include_dir.exists():
        include_dir.mkdir(parents=True, exist_ok=True)
    return include_dir


def load_package_toml(owner: str, repo: str,
                      version: str) -> dict:
    """Load package.toml from cache.

    Returns parsed TOML dict, or empty dict if not found.
    """
    path = CACHE_DIR / owner / repo / version / "package.toml"
    if not path.exists():
        return {}
    with open(path, "rb") as f:
        return tomllib.load(f)
