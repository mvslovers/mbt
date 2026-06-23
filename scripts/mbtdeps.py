"""mbt v2 deps -- download + stage dependency libraries (headers + .a).

For each [dependencies] entry, resolve the version, download the
{repo}-{version}-lib.tar.gz release asset, record its SHA256 in
mbt.lock, and extract it to .mbt/deps/{repo}/ (include/ + lib/).
The host build then compiles against .mbt/deps/*/include and links
.mbt/deps/*/lib/*.a (wired in mk/mbt.mk).

  make deps                  use mbt.lock if present (verify SHA),
                             otherwise resolve the ranges; download + stage
  make deps ARGS=--update    re-resolve the ranges and rewrite the lock

The SHA256 in the lock is the real pin: a re-pushed prerelease (moving
-dev tag) changes the asset, the SHA mismatches, and 'make deps' fails
until you re-run with --update.
"""

import sys
import json
import shutil
import hashlib
import tarfile
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

from mbt import EXIT_SUCCESS, EXIT_CONFIG, EXIT_DEPENDENCY
from mbt.dependencies import _resolve_one, download_dependency, DependencyError
from mbt.version import Version

DEPS_DIR = Path(".mbt/deps")                   # staged artifacts (gitignored)
LOCK_PATH = Path("mbt.lock")                   # committed pin (project root)
OVERRIDE_PATH = Path(".mbt/deps.local.toml")   # local dev overrides (gitignored)


def _log(msg: str) -> None:
    print(f"[mbt] {msg}")


def _log_error(msg: str) -> None:
    print(f"[mbt] ERROR: {msg}", file=sys.stderr)


def _sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(65536), b""):
            h.update(block)
    return h.hexdigest()


def _stage_lib(tarball: Path, dest: Path) -> None:
    """Extract {repo}-{ver}-lib.tar.gz into dest, stripping the top dir."""
    if dest.exists():
        shutil.rmtree(dest)
    dest.mkdir(parents=True)
    with tarfile.open(tarball) as tar:
        for member in tar.getmembers():
            parts = Path(member.name).parts
            if len(parts) <= 1:           # skip the top "{repo}-{ver}/" entry
                continue
            rel = Path(*parts[1:])
            if rel.is_absolute() or ".." in rel.parts:
                continue                  # path-traversal guard
            member.name = str(rel)
            tar.extract(member, dest)


def _stage_path_override(path: str, dest: Path) -> str:
    """Stage a dependency from a local working copy (path override).

    Uses the dep project's [lib] build output (build/<name>.a) and its
    declared headers; skips GitHub and the SHA lock entirely. The dep
    must be built ('make lib') in the override path first. Returns the
    library name.
    """
    root = Path(path)
    proj = root / "project.toml"
    if not proj.is_file():
        raise ValueError(f"no project.toml in override path {path}")
    with open(proj, "rb") as f:
        cfg = tomllib.load(f)
    lib = cfg.get("lib")
    if not lib:
        raise ValueError(f"{path} has no [lib] section to consume")
    libname = lib.get("name") or cfg.get("project", {}).get("name", "lib")
    lib_a = root / "build" / f"{libname}.a"
    if not lib_a.is_file():
        raise ValueError(f"{lib_a} not built -- run 'make lib' in {path}")
    if dest.exists():
        shutil.rmtree(dest)
    (dest / "include").mkdir(parents=True)
    (dest / "lib").mkdir(parents=True)
    shutil.copy2(lib_a, dest / "lib" / lib_a.name)
    for h in lib.get("headers", []):
        src = root / h
        if src.is_file():
            shutil.copy2(src, dest / "include" / Path(h).name)
    return libname


def main() -> int:
    parser = argparse.ArgumentParser(description="mbt v2 deps")
    parser.add_argument("--project", default="project.toml")
    parser.add_argument("--update", action="store_true",
                        help="re-resolve ranges and rewrite the lock")
    args = parser.parse_args()

    try:
        with open(args.project, "rb") as f:
            cfg = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        _log_error(f"cannot parse {args.project}: {e}")
        return EXIT_CONFIG

    declared = cfg.get("dependencies", {})
    if not declared:
        _log("Dependencies: none declared")
        return EXIT_SUCCESS

    lock = {}
    if LOCK_PATH.exists() and not args.update:
        try:
            lock = json.loads(LOCK_PATH.read_text())
        except (OSError, json.JSONDecodeError):
            lock = {}

    overrides = {}
    if OVERRIDE_PATH.exists():
        try:
            with open(OVERRIDE_PATH, "rb") as f:
                overrides = tomllib.load(f).get("override", {})
        except (OSError, tomllib.TOMLDecodeError):
            overrides = {}
    if overrides:
        _log(f"Local overrides ({OVERRIDE_PATH}): {', '.join(overrides)}")

    DEPS_DIR.mkdir(parents=True, exist_ok=True)
    new_lock = {}

    for dep_key, constraint in declared.items():
        if "/" not in dep_key:
            _log_error(f"bad dependency key {dep_key!r} (want owner/repo)")
            return EXIT_CONFIG
        owner, repo = dep_key.split("/", 1)
        locked = lock.get(dep_key)

        # local path override: build against a working copy, skip GitHub +
        # the SHA lock (the committed lock keeps its GitHub pin).
        ovr = overrides.get(dep_key)
        if ovr and ovr.get("path"):
            try:
                libname = _stage_path_override(ovr["path"], DEPS_DIR / repo)
            except (OSError, ValueError, tomllib.TOMLDecodeError) as e:
                _log_error(f"{dep_key}: path override failed: {e}")
                return EXIT_DEPENDENCY
            _log(f"{dep_key} -> LOCAL {ovr['path']} ({libname}.a, override)")
            if locked:
                new_lock[dep_key] = locked          # keep the committed pin
            continue

        # version: from lock (default) or freshly resolved (--update / no lock)
        if locked and not args.update:
            version = locked["version"]
        else:
            try:
                version = _resolve_one(owner, repo, constraint)
            except DependencyError as e:
                _log_error(str(e))
                return EXIT_DEPENDENCY
        is_pre = Version.parse(version).pre is not None
        _log(f"{dep_key} {constraint} -> {version}")

        # download (force for prereleases -- the tag may have moved)
        try:
            cache = download_dependency(owner, repo, version, force=is_pre)
        except DependencyError as e:
            _log_error(str(e))
            return EXIT_DEPENDENCY

        lib = cache / f"{repo}-{version}-lib.tar.gz"
        if not lib.is_file():
            _log_error(
                f"{dep_key} {version}: no {lib.name} in the release "
                f"(dependency has no library artifact)"
            )
            return EXIT_DEPENDENCY

        sha = _sha256(lib)
        if (locked and not args.update
                and locked.get("sha256") and locked["sha256"] != sha):
            _log_error(
                f"{dep_key} {version}: lib SHA changed "
                f"({locked['sha256'][:12]} -> {sha[:12]}). A re-pushed "
                f"prerelease? Run 'make deps ARGS=--update' to accept."
            )
            return EXIT_DEPENDENCY

        dest = DEPS_DIR / repo
        _stage_lib(lib, dest)
        _log(f"  staged -> {dest} (sha {sha[:12]})")
        new_lock[dep_key] = {"version": version, "sha256": sha}

    LOCK_PATH.write_text(
        json.dumps(new_lock, indent=2, sort_keys=True) + "\n"
    )
    _log(f"Locked {len(new_lock)} dependency(ies) -> {LOCK_PATH}")
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
