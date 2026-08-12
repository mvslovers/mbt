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

Usage:
    python3 mbttoolchain.py [--project project.toml] [--repo NAME]

Output (stdout, one KEY=VALUE per line -- suited to >> "$GITHUB_ENV"):
    CC370_REF=main
    LIBC370_REF=v1.0.2

With --repo NAME the bare ref of that one repository is printed instead.
Log output goes to stderr so stdout stays a clean data channel.
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

from mbt import EXIT_SUCCESS, EXIT_CONFIG

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
    args = parser.parse_args()

    try:
        with open(args.project, "rb") as f:
            cfg = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as e:
        _log_error(f"cannot parse {args.project}: {e}")
        return EXIT_CONFIG

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
