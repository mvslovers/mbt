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


def _constraint_allows_prerelease(constraint: str) -> bool:
    """Return True if the constraint itself names a prerelease bound.

    Prereleases are eligible when any part of the constraint references a
    prerelease version -- exact ('=1.0.0-dev') or a range bound
    ('>=1.0.0-dev'). A purely stable constraint ('>=1.0.0') resolves
    stable releases only (prereleases excluded), matching the usual
    semver convention "you only get prereleases if you ask for them".
    """
    for part in constraint.split(","):
        ver = part.strip().lstrip("><=~^ ")
        try:
            if Version.parse(ver).pre is not None:
                return True
        except ValueError:
            continue
    return False


def version_allowed(version_str: str, constraint: str) -> bool:
    """Return True if the resolver may pick version_str for constraint.

    Stricter than satisfies(): a prerelease is only eligible when the
    constraint itself names one (see _constraint_allows_prerelease), so
    '1.5.0-dev' satisfies '>=1.0.0' but is not allowed by it.

    Raises:
        ValueError: If version_str or the constraint cannot be parsed
    """
    if (Version.parse(version_str).pre is not None
            and not _constraint_allows_prerelease(constraint)):
        return False
    return satisfies(version_str, constraint)


def _resolve_from_cache(owner: str, repo: str,
                        constraint: str) -> str | None:
    """Check local cache for a version matching the constraint.

    Scans ~/.mbt/cache/{owner}/{repo}/ for cached versions and
    returns the highest match, or None if nothing found.
    """
    cache_base = CACHE_DIR / owner / repo
    if not cache_base.is_dir():
        return None

    candidates = []
    for entry in cache_base.iterdir():
        if not entry.is_dir():
            continue
        ver_str = entry.name
        try:
            if version_allowed(ver_str, constraint):
                candidates.append(Version.parse(ver_str))
        except ValueError:
            continue

    if not candidates:
        return None
    return str(max(candidates))


def _resolve_offline(owner: str, repo: str, constraint: str,
                     reason: str, warn) -> str:
    """Resolve from the local cache because GitHub could not be asked.

    The cache only holds what this machine happened to download, so the
    answer may be older than the newest matching release -- hence the
    warning.

    Raises:
        DependencyError: If no cached version satisfies the constraint
    """
    cached = _resolve_from_cache(owner, repo, constraint)
    if cached is None:
        raise DependencyError(
            f"{reason} for {owner}/{repo}, and no cached version "
            f"satisfies {constraint!r}"
        )
    if warn is not None:
        warn(f"{reason}: resolved {owner}/{repo} {constraint} -> {cached} "
             f"from the local cache; a newer release may exist")
    return cached


def _resolve_one(owner: str, repo: str,
                 constraint: str, warn=None) -> str:
    """Query GitHub API and return highest version matching constraint.

    GitHub is authoritative: the local cache holds only what this machine
    happened to download, so resolving from it first made a range pick
    the highest *cached* version and let two machines disagree (issue
    #125). The cache is consulted only when GitHub cannot answer --
    unreachable, HTTP 5xx, or rate-limited (403/429) -- and then
    warn(message) is called when given. Any other HTTP error (404: no
    such repo) is fatal.

    Stable releases only, unless constraint is an exact prerelease pin
    (e.g. '=1.0.1-dev'), in which case prerelease releases are included.

    Raises:
        DependencyError: If no matching release found or API fails
    """
    url = f"{_GH_API}/repos/{owner}/{repo}/releases?per_page=100"
    req = _gh_request(url)

    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            releases = json.loads(resp.read())
    except urllib.error.HTTPError as e:
        if e.code in (403, 429) or e.code >= 500:
            return _resolve_offline(owner, repo, constraint,
                                    f"GitHub API HTTP {e.code}", warn)
        raise DependencyError(
            f"GitHub API error for {owner}/{repo}: HTTP {e.code}"
        )
    except urllib.error.URLError as e:
        return _resolve_offline(owner, repo, constraint,
                                f"Cannot reach GitHub API ({e.reason})",
                                warn)

    allow_prerelease = _constraint_allows_prerelease(constraint)

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
                        force: bool = False,
                        warn=None) -> Path:
    """Download dependency assets to cache.

    Cache structure:
        ~/.mbt/cache/{owner}/{repo}/{version}/
            package.toml
            {name}-{version}-headers.tar.gz
            {name}-{version}-mvs.tar.gz

    Skips download if cache is already populated, unless force=True.
    Pass force=True for prerelease versions whose tag may be re-pushed.

    If the release is gone upstream (HTTP 404) but the cache still holds
    it, the cache is used and warn(message) is called when given: the
    build succeeds here and fails wherever that cache is absent (CI).
    An unreachable GitHub (offline) falls back to the cache silently.

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
    except urllib.error.HTTPError as e:
        if cache_populated:
            if e.code == 404 and warn is not None:
                warn(f"{owner}/{repo} {version}: release {tag} no longer "
                     f"exists on GitHub; using the local cache "
                     f"({cache_dir}). A build without that cache (CI) "
                     f"will fail.")
            return cache_dir
        raise DependencyError(
            f"Cannot find release {tag} for {owner}/{repo} "
            f"and no local cache available"
        )
    except urllib.error.URLError:
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
        download_url = asset.get("url", "") or asset.get("browser_download_url", "")
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
