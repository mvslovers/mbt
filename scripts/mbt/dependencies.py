"""Dependency resolution via GitHub Releases.

Queries the GitHub Releases API to find the highest
version satisfying each dependency constraint. Downloads
release assets and manages the local cache.
"""

import json
import os
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


def _gh_token() -> str | None:
    """Return GitHub token from environment, or None."""
    return os.environ.get("GITHUB_TOKEN") or os.environ.get("MBT_GITHUB_TOKEN")


def _gh_request(url: str,
                accept: str = "application/vnd.github+json") -> urllib.request.Request:
    """Build a GitHub API request with optional auth."""
    req = urllib.request.Request(url)
    req.add_header("Accept", accept)
    req.add_header("User-Agent", "mbt/1.0.0")
    token = _gh_token()
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    return req


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


def _is_exact_prerelease(constraint: str) -> bool:
    """Return True if constraint is an exact pin to a prerelease version.

    Only a single '=X.Y.Z-pre' constraint qualifies — range operators
    (>=, <) always resolve stable releases only.
    """
    parts = [p.strip() for p in constraint.split(",")]
    if len(parts) != 1:
        return False
    part = parts[0]
    if not part.startswith("=") or part.startswith(">="):
        return False
    try:
        return Version.parse(part[1:]).pre is not None
    except ValueError:
        return False


def _resolve_from_cache(owner: str, repo: str,
                        constraint: str) -> str | None:
    """Check local cache for a version matching the constraint.

    Scans ~/.mbt/cache/{owner}/{repo}/ for cached versions and
    returns the highest match, or None if nothing found.
    """
    cache_base = CACHE_DIR / owner / repo
    if not cache_base.is_dir():
        return None

    allow_prerelease = _is_exact_prerelease(constraint)
    candidates = []
    for entry in cache_base.iterdir():
        if not entry.is_dir():
            continue
        ver_str = entry.name
        try:
            ver = Version.parse(ver_str)
        except ValueError:
            continue
        if ver.pre is not None and not allow_prerelease:
            continue
        if satisfies(ver_str, constraint):
            candidates.append(ver)

    if not candidates:
        return None
    return str(max(candidates))


def _resolve_one(owner: str, repo: str,
                 constraint: str) -> str:
    """Query GitHub API and return highest version matching constraint.

    First checks the local cache. If a matching version is found
    there, uses it without contacting GitHub. Otherwise falls back
    to the GitHub Releases API.

    Stable releases only, unless constraint is an exact prerelease pin
    (e.g. '=1.0.1-dev'), in which case prerelease releases are included.

    Raises:
        DependencyError: If no matching release found or API fails
    """
    # Try local cache first
    cached = _resolve_from_cache(owner, repo, constraint)
    if cached is not None:
        return cached

    url = f"{_GH_API}/repos/{owner}/{repo}/releases"
    req = _gh_request(url)

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

    allow_prerelease = _is_exact_prerelease(constraint)

    # Collect versions that satisfy the constraint
    candidates = []
    for release in releases:
        if release.get("draft"):
            continue
        if release.get("prerelease") and not allow_prerelease:
            continue
        tag = release.get("tag_name", "")
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
                        version: str,
                        force: bool = False) -> Path:
    """Download dependency assets to cache.

    Cache structure:
        ~/.mbt/cache/{owner}/{repo}/{version}/
            package.toml
            {name}-{version}-headers.tar.gz
            {name}-{version}-mvs.tar.gz

    Skips download if cache is already populated, unless force=True.
    Pass force=True for prerelease versions whose tag may be re-pushed.

    Returns:
        Path to cache directory

    Raises:
        DependencyError: If download fails
    """
    import shutil
    cache_dir = CACHE_DIR / owner / repo / version
    cache_populated = (
        cache_dir.exists() and any(cache_dir.iterdir())
    )

    if not force and cache_populated:
        return cache_dir

    # Try to download from GitHub.  If the release doesn't exist
    # (e.g. local-only prerelease), fall back to existing cache.
    tag = f"v{version}"
    url = f"{_GH_API}/repos/{owner}/{repo}/releases/tags/{tag}"
    req = _gh_request(url)

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            release = json.loads(resp.read())
    except (urllib.error.HTTPError, urllib.error.URLError):
        if cache_populated:
            return cache_dir
        raise DependencyError(
            f"Cannot find release {tag} for {owner}/{repo} "
            f"and no local cache available"
        )

    # Download succeeded — refresh cache
    if force and cache_dir.exists():
        shutil.rmtree(cache_dir)
    cache_dir.mkdir(parents=True, exist_ok=True)

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
    req = _gh_request(url, accept="application/octet-stream")
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
    tarball_name = f"{dep_name}-{dep_version}-lib-headers.tar.gz"
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
