"""mbt v2 toolchain -- resolve the cc370/libc370 git refs a build checks out.

Reads the optional [toolchain] section of project.toml and prints one git ref
per toolchain repository, so a release is built against a declared version
instead of whatever the default branch happened to be at that minute:

    [toolchain]
    libc370 = "1.0.2"   # bare version -> tag v1.0.2
    cc370   = "main"    # cc370 has no releases yet

Both keys are optional and both default to 'main', so a project that declares
nothing keeps today's floating behaviour and the consumers can be migrated one
at a time.

Only release.yml consumes this.  build.yml stays on 'main' deliberately: a
floating toolchain on PRs is the early warning that catches a cc370 or libc370
regression before it reaches a consumer.  Pinning it there would hide the break
until someone bumps the version (issue #67).

The same declaration is also the build's requirement on the *installed*
libc370, because it states one thing -- "this project is built with libc370
X.Y.Z" -- that CI reproduces exactly and a working copy need only satisfy:

    make, make lib, make test    sysroot >= the declared version
    make release / prerelease    sysroot == the declared version

Nothing is checked when no version is declared, or when the value is a
branch/SHA rather than a version: there is then nothing to compare, so an
existing project changes behaviour only once it pins deliberately.  Note that
the exact check gates the *maintainer's* sysroot -- the published artifact is
built by release.yml against the pin, which is what makes a release correct.

Usage:
    python3 mbttoolchain.py [--project project.toml] [--repo NAME]
    python3 mbttoolchain.py --check [--exact] [--sysroot DIR]

Output (stdout, one KEY=VALUE per line -- suited to >> "$GITHUB_ENV"):
    CC370_REF=main
    LIBC370_REF=v1.0.2

With --repo NAME the bare ref of that one repository is printed instead.
Errors and warnings go to stderr so stdout stays a clean data channel.
"""

import re
import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

try:
    import tomllib
except ModuleNotFoundError:
    import tomli as tomllib

from mbt import EXIT_SUCCESS, EXIT_BUILD, EXIT_CONFIG
from mbt.version import Version
from mbt.sysroot import derive_sysroot, installed_libc370

# Toolchain repositories, in the order they are built (cc370 first: libc370
# is compiled *with* it).  Both live under github.com/mvslovers.
REPOS = ("cc370", "libc370")

DEFAULT_REF = "main"

# A bare semver: '1.0.2', '1.0.2-dev', '1.0.2-rc1', '1.0.2+build'.
_SEMVER_RE = re.compile(r"^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)*$")


class ToolchainError(Exception):
    """Raised when the [toolchain] section is invalid."""
    pass


def _log_error(msg: str) -> None:
    print(f"[mbt] ERROR: {msg}", file=sys.stderr)


def _log_warn(msg: str) -> None:
    print(f"[mbt] WARNING: {msg}", file=sys.stderr)


def is_version(value: str) -> bool:
    """True if a [toolchain] value names a version rather than a git ref."""
    return bool(_SEMVER_RE.match(value.strip()))


def declared(cfg: dict, repo: str) -> str | None:
    """Return the raw [toolchain] value for a repository, or None.

    Unlike resolve(), this is the value as written -- '1.0.2', not the
    'v1.0.2' tag it names -- because the build compares it as a version.
    """
    value = cfg.get("toolchain", {}).get(repo)
    return value.strip() if isinstance(value, str) and value.strip() else None


def libc370_status(cfg: dict, sysroot, exact: bool = False) -> tuple[str, str]:
    """Compare the libc370 installed in a sysroot against the declaration.

    The single place the comparison is made: the build gates on it and
    `make doctor` reports it, so the two can never disagree.

    Args:
        cfg: parsed project.toml
        sysroot: cc370 sysroot directory, or None if it could not be found
        exact: require an exact match (release) rather than >= (build)

    Returns:
        (status, message) where status is
          'ok'       the declaration is satisfied
          'skip'     nothing to compare (no version declared, or a git ref)
          'unknown'  a version is declared but the stamp could not be read
          'fail'     the installed runtime violates the declaration
    """
    want = declared(cfg, "libc370")
    have = installed_libc370(sysroot) if sysroot is not None else None

    # No version to compare against: a project that declares nothing, or one
    # tracking a branch/SHA. Still worth reporting what is installed.
    if want is None or not is_version(want):
        why = "no version declared" if want is None else f"declared as '{want}'"
        if have is None:
            return "skip", f"libc370: no version readable from the sysroot ({why})"
        return "skip", f"libc370 {have} in {sysroot} ({why}, nothing to check)"

    if sysroot is None:
        return "skip", "cc370 sysroot not found; skipping the libc370 check"
    if have is None:
        # A stamp that cannot be read must never fail a build: libc370's
        # spelling of it has changed before, and every project in the
        # ecosystem would stop building the day it changes again.
        return "unknown", (
            f"cannot read the libc370 version from {sysroot}/lib/libc.a; "
            f"skipping the [toolchain] check (want {want})"
        )

    have_v, want_v = Version.parse(have), Version.parse(want)
    if (have_v == want_v) if exact else (have_v >= want_v):
        return "ok", f"libc370 {have} in {sysroot} ({'==' if exact else '>='} {want})"

    if exact:
        return "fail", (
            f"libc370 in the sysroot is {have}, but project.toml pins {want} -- "
            f"a release must be built against the pinned runtime; reinstall "
            f"{sysroot} from libc370 v{want}"
        )
    return "fail", (
        f"libc370 in the sysroot is {have}, but project.toml requires "
        f">= {want}; reinstall {sysroot} from libc370 v{want} or newer "
        f"('make install' in the libc370 checkout)"
    )


def check_libc370(cfg: dict, sysroot, exact: bool = False) -> int:
    """Gate a build on libc370_status().

    Returns:
        EXIT_SUCCESS when the check passes, is skipped or cannot be made;
        EXIT_BUILD when the installed runtime violates the declaration
    """
    status, message = libc370_status(cfg, sysroot, exact)
    if status == "fail":
        _log_error(message)
        return EXIT_BUILD
    if status == "unknown":
        _log_warn(message)
    elif status == "ok":
        print(f"[mbt] {message}")
    # 'skip' stays silent during a build: most projects declare no toolchain,
    # and a line on every build would be noise. `make doctor` reports it.
    return EXIT_SUCCESS


def git_ref(value: str) -> str:
    """Map a [toolchain] value to a git ref.

    A bare semver ('1.0.2') names a release and becomes its tag ('v1.0.2') --
    that is how the ecosystem tags, and writing the version is what a project
    means.  Every other form is already a git ref (a branch, a 'v'-prefixed
    tag, a commit SHA) and is used verbatim.
    """
    value = value.strip()
    return f"v{value}" if _SEMVER_RE.match(value) else value


def env_key(repo: str) -> str:
    """Environment variable name carrying a repository's ref."""
    return f"{repo.upper()}_REF"


def resolve(cfg: dict) -> dict[str, str]:
    """Return {repo: git ref} for every toolchain repository.

    Args:
        cfg: parsed project.toml

    Returns:
        A ref for each entry of REPOS, defaulting to 'main'

    Raises:
        ToolchainError: on an unknown key or a non-string/empty value
    """
    section = cfg.get("toolchain", {})
    if not isinstance(section, dict):
        raise ToolchainError("[toolchain] must be a table")

    # An unknown key is fatal on purpose.  Ignoring it would leave the release
    # silently on 'main' -- exactly the unpinned build the section exists to
    # prevent -- and a typo would only surface in the startup banner of an
    # already published load module.
    unknown = sorted(set(section) - set(REPOS))
    if unknown:
        raise ToolchainError(
            f"unknown [toolchain] key(s): {', '.join(unknown)} "
            f"(known: {', '.join(REPOS)})"
        )

    refs = {}
    for repo in REPOS:
        value = section.get(repo, DEFAULT_REF)
        if not isinstance(value, str) or not value.strip():
            raise ToolchainError(
                f"[toolchain] {repo} must be a non-empty string, got {value!r}"
            )
        refs[repo] = git_ref(value)
    return refs


def main() -> int:
    parser = argparse.ArgumentParser(description="mbt v2 toolchain refs")
    parser.add_argument("--project", default="project.toml")
    parser.add_argument("--repo", choices=REPOS,
                        help="print only this repository's ref, bare")
    parser.add_argument("--check", action="store_true",
                        help="check the installed libc370 against [toolchain]")
    parser.add_argument("--exact", action="store_true",
                        help="with --check: require an exact match (release)")
    parser.add_argument("--sysroot",
                        help="with --check: sysroot to inspect (default: derived)")
    args = parser.parse_args()

    try:
        with open(args.project, "rb") as f:
            cfg = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        _log_error(f"cannot parse {args.project}: {e}")
        return EXIT_CONFIG

    if args.check:
        # Validate the section even when only libc370 is compared, so a typo
        # is caught by the build too and not just by the release workflow.
        try:
            resolve(cfg)
        except ToolchainError as e:
            _log_error(str(e))
            return EXIT_CONFIG
        sysroot = args.sysroot or derive_sysroot()
        return check_libc370(cfg, sysroot, exact=args.exact)

    try:
        refs = resolve(cfg)
    except ToolchainError as e:
        _log_error(str(e))
        return EXIT_CONFIG

    if args.repo:
        print(refs[args.repo])
        return EXIT_SUCCESS

    for repo in REPOS:
        print(f"{env_key(repo)}={refs[repo]}")
    return EXIT_SUCCESS


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as e:
        print(f"[mbt] ERROR: Internal error: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(99)
