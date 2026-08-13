"""mbt v2 config generator -- read project.toml, emit make variables.

Reads the v2 project.toml format and writes .mbt/config.mk with make
variables for the cc370 toolchain build.  Called once by mk/mbt.mk at
make startup (via $(shell)).

Usage:
    python3 mbtconfig.py [--project project.toml]

Output (stdout in --output=shell mode, or .mbt/config.mk):
    PROJECT_NAME, PROJECT_VERSION, CFLAGS, SRC_DIRS,
    per-module OBJS/ENTRY/LINK_CMD, LIB_*, HEADER_FILES, etc.
"""

import glob
import os
import re
import sys
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from mbt import buildstamp
from mbt.version import to_vrm

# Python 3.11+ has tomllib in stdlib
try:
    import tomllib
except ModuleNotFoundError:
    try:
        import tomli as tomllib
    except ModuleNotFoundError:
        tomllib = None


def _parse_toml(path: str) -> dict:
    """Parse a TOML file, preferring tomllib if available."""
    if tomllib is not None:
        with open(path, "rb") as f:
            return tomllib.load(f)
    import subprocess, json
    code = (
        "import tomllib, json, sys; "
        "print(json.dumps(tomllib.load(open(sys.argv[1],'rb'))))"
    )
    r = subprocess.run(
        [sys.executable, "-c", code, path],
        capture_output=True, text=True,
    )
    if r.returncode != 0:
        print(f"[mbt] ERROR: Cannot parse {path}: {r.stderr}", file=sys.stderr)
        sys.exit(1)
    return json.loads(r.stdout)


class ConfigError(Exception):
    """project.toml is invalid -- abort before anything is built."""


# A load module member name: 1..8 characters from A-Z 0-9 and the national
# characters @ # $, first character not a digit.  Every [[module]] and [[test]]
# name becomes exactly that -- a PDS member, and PGM=<name> in the generated
# JCL.  A longer one builds and deploys without complaint and only fails on
# MVS, as a JCL error that discards the whole job:
#
#   IEF452I MBTTEST  JOB NOT RUN - JCL ERROR
#   IEF642I EXCESSIVE PARAMETER LENGTH IN THE PGM FIELD
#
# so the test matrix then reports FAIL for every step and points at everything
# except the cause.  Reject it here instead (issue #73).
_MEMBER_RE = re.compile(r"^[A-Z@#$][A-Z0-9@#$]{0,7}$")


def _check_member_name(name, kind: str) -> None:
    """Raise ConfigError unless `name` is a usable MVS member name."""
    # isinstance, not just falsiness: `name = 12345678` is a valid TOML int, and
    # len() on it would raise past main()'s ConfigError handler -- a traceback
    # AND a stale config.mk left in place, the exact failure this check removes.
    if not isinstance(name, str) or not name:
        raise ConfigError(f"project.toml: a [[{kind}]] entry has no usable name")
    if len(name) > 8:
        raise ConfigError(
            f'project.toml: {kind} name "{name}" is {len(name)} characters '
            f"-- MVS member names are 8 at most"
        )
    if not _MEMBER_RE.match(name):
        raise ConfigError(
            f'project.toml: {kind} name "{name}" is not a valid MVS member '
            f"name -- use A-Z 0-9 @ # $, not starting with a digit"
        )


def _validate_names(cfg: dict) -> None:
    """Check every [[module]] and [[test]] name before anything is emitted.

    Deliberately ahead of the `mvs = false` skip below: the name is also the
    `--only` key, the host test binary and the matrix column, so one rule
    everywhere means turning a host-only test back on never surprises anyone.
    [lib] name is NOT checked -- that one names a host archive (libufs.a),
    not a member.
    """
    for kind in ("module", "test"):
        for entry in cfg.get(kind, []):
            _check_member_name(entry.get("name"), kind)


def _make_escape(s: str) -> str:
    """Escape characters that are special in GNU Make (# and $)."""
    return s.replace("$", "$$").replace("#", "\\#")


def _make_error_text(msg: str) -> str:
    """Escape a message for use as the body of a Make $(error ...).

    Beyond the usual # and $, parentheses matter here: make matches them
    balanced inside $(error ...), so a stray one in a rejected name would
    truncate the message and leave the rest as a syntax error -- the very
    failure mode this check exists to remove.  $(LPAREN)/$(RPAREN) are
    defined alongside the $(error) line in _error_config_mk().
    """
    # One pass, not two chained .replace() calls: the replacement for '(' ends
    # in ')', which a following pass over ')' would mangle right back.
    return _make_escape(msg).translate(
        {ord("("): "$(LPAREN)", ord(")"): "$(RPAREN)"})


def _error_config_mk(msg: str) -> str:
    """A .mbt/config.mk whose only effect is to stop make with `msg`.

    mk/mbt.mk pulls the generated config.mk in with `-include` and discards
    this script's exit status, so bailing out quietly would leave the
    *previous* config.mk in place and the build would carry on against a
    stale module list.  Overwriting it with a $(error) makes make fail during
    read-in with this exact text, and the next run regenerates the file.
    """
    return (
        "# Auto-generated by mbtconfig.py -- project.toml is invalid\n"
        "LPAREN := (\n"
        "RPAREN := )\n"
        f"$(error [mbt] ERROR: {_make_error_text(msg)})\n"
    )


def _var_key(name: str) -> str:
    """Make-safe identifier for a module/test name.

    MVS member names may contain the national characters # $ @. In a Make
    *variable name* '#' starts a comment and '$' a variable reference, so a
    module name like IRX#HELO cannot be used verbatim as MODULE_<name>_*.
    Map # and $ to '_', which is never a valid MVS member character, so the
    key can never collide with a real member name. The real name is carried
    separately in MODULE_<key>_NAME and used for the output member/file.
    """
    return name.replace("#", "_").replace("$", "_")


def _resolve_sources(patterns: list, exclude: list = None) -> list:
    """Expand glob patterns to actual source files, sorted."""
    files = []
    for pat in patterns:
        matches = sorted(glob.glob(pat))
        if not matches:
            print(f"[mbt] WARNING: pattern '{pat}' matched no files",
                  file=sys.stderr)
        files.extend(matches)
    if exclude:
        exc_files = set()
        for pat in exclude:
            exc_files.update(glob.glob(pat))
        files = [f for f in files if f not in exc_files]
    # Deduplicate while preserving order
    seen = set()
    result = []
    for f in files:
        if f not in seen:
            seen.add(f)
            result.append(f)
    return result


def _src_to_obj(src: str, builddir: str) -> str:
    """Map a source file path to a build object path.

    src/ufsd#cmd.c  -> build/ufsd#cmd.o
    client/libufs.c -> build/libufs.o
    asm/foo.asm     -> build/foo.o
    """
    base = Path(src).stem + ".o"
    return os.path.join(builddir, base)


def _startup_to_link_cmd(startup) -> str:
    """Map startup config value to a LINK_* macro name."""
    if startup is False or startup is None:
        return "LINK_NOCRT"
    mapping = {
        "crt0": "LINK_CRT0",
        "crt1": "LINK_CRT1",
        "crtm": "LINK_CRTM",
    }
    return mapping.get(str(startup), "LINK_CRT0")


def _collect_src_dirs(sources: list) -> set:
    """Extract unique parent directories from a list of source files."""
    dirs = set()
    for s in sources:
        d = os.path.dirname(s)
        if d:
            dirs.add(d)
    return dirs


# Defaults
DEFAULT_ENTRY   = "@@CRT0"    # standard C entry point
DEFAULT_STARTUP = "crt0"      # simple CRT, no threading


def _emit_module(lines, mod, builddir, all_src_dirs, all_objs, var_prefix):
    """Emit make variables for a single module or test."""
    mod_name = mod["name"]
    entry = mod.get("entry", DEFAULT_ENTRY)
    startup = mod.get("startup", DEFAULT_STARTUP)
    sources = _resolve_sources(
        mod.get("sources", []),
        mod.get("exclude", []),
    )
    objs = [_src_to_obj(s, builddir) for s in sources]
    all_src_dirs.update(_collect_src_dirs(sources))
    all_objs.update(objs)

    link_cmd = _startup_to_link_cmd(startup)
    objs_escaped = " ".join(_make_escape(o) for o in objs)

    # key = make-safe identifier (no # or $); name = the real MVS member name
    # (carried in MODULE_<key>_NAME, used for the output member/file). This lets
    # a module name carry national characters like '#' (e.g. IRX#HELO).
    key = _var_key(mod_name)
    lines.append(f"{var_prefix} += {key}")
    lines.append(f"MODULE_{key}_NAME := {_make_escape(mod_name)}")
    lines.append(f"MODULE_{key}_ENTRY := {_make_escape(entry)}")
    lines.append(f"MODULE_{key}_LINK_CMD := {link_cmd}")
    lines.append(f"MODULE_{key}_OBJS := {objs_escaped}")
    lines.append(f"MODULE_{key}_ALIAS := {key.lower()}")
    # APF authorization code (SETCODE AC(n)); only emitted when non-zero so
    # modules without it pass no --ac to ld370 (default AC(0)).
    ac = mod.get("ac", 0)
    if ac:
        lines.append(f"MODULE_{key}_AC := {ac}")
    # norent / noreus: drop the load module's RENT / REUS attribute (the LINK
    # rule passes --norent / --noreus to ld370). A self-modified data module
    # like IRXANCHR must be reusable (REUS) but NOT reentrant -> norent = true.
    if mod.get("norent", False):
        lines.append(f"MODULE_{key}_NORENT := 1")
    if mod.get("noreus", False):
        lines.append(f"MODULE_{key}_NOREUS := 1")
    lines.append("")


def generate(project_file: str = "project.toml", builddir: str = "build") -> str:
    """Generate make variable assignments from project.toml."""
    cfg = _parse_toml(project_file)
    _validate_names(cfg)
    lines = [
        "# Auto-generated by mbtconfig.py -- do not edit",
        f"# Source: {project_file}",
        "",
    ]

    # -- Project metadata --
    project = cfg.get("project", {})
    name = project.get("name", "unknown")
    version = project.get("version", "0.0.0")
    ptype = project.get("type", "application")

    lines.append(f"PROJECT_NAME := {name}")
    lines.append(f"PROJECT_VERSION := {version}")
    lines.append(f"PROJECT_TYPE := {ptype}")
    # VRM (e.g. V1R0M0D) for the LINKLIB DSN embedded in the release XMIT.
    # Only 'package' needs it; a malformed version stays undefined rather
    # than crashing every 'make'.
    try:
        lines.append(f"PROJECT_VRM := {to_vrm(version)}")
    except ValueError:
        pass
    lines.append("")

    # -- Build flags --
    build = cfg.get("build", {})
    cflags = build.get("cflags", [])
    asflags = build.get("asflags", [])

    if cflags:
        lines.append(f"CFLAGS += {' '.join(cflags)}")
    if asflags:
        lines.append(f"ASFLAGS += {' '.join(asflags)}")
    lines.append("")

    # -- Collect all source dirs for VPATH --
    all_src_dirs = set()
    all_objs = set()

    # -- Modules --
    modules = cfg.get("module", [])
    if modules:
        lines.append("# -- Modules --")
    for mod in modules:
        _emit_module(lines, mod, builddir, all_src_dirs, all_objs, "MODULES")

    # -- Tests --
    tests = cfg.get("test", [])
    if tests:
        lines.append("# -- Tests --")
    for test in tests:
        # Explicit host-only opt-out (the mirror of `host = false`): a test
        # whose fixtures only resolve on the host (e.g. a corpus loaded from a
        # host-relative path) has nothing to build for MVS. `mvs = false` drops
        # it from TESTS here so it is never cross-compiled/linked and never
        # appears in a `make test`/`test-mvs` run; test-host is unaffected (it
        # reads project.toml directly, not this generated file).
        if test.get("mvs") is False:
            name = test.get("name", "?")
            print(f"[mbt] SKIP {name} (mvs = false, host-only)", file=sys.stderr)
            continue
        _emit_module(lines, test, builddir, all_src_dirs, all_objs, "TESTS")

    # -- Library --
    lib = cfg.get("lib", {})
    if lib:
        lines.append("# -- Library --")
        lib_name = lib.get("name", name)
        sources = _resolve_sources(lib.get("sources", []))
        objs = [_src_to_obj(s, builddir) for s in sources]
        headers = lib.get("headers", [])
        all_src_dirs.update(_collect_src_dirs(sources))
        all_objs.update(objs)

        objs_escaped = " ".join(_make_escape(o) for o in objs)
        lines.append(f"LIB_NAME := {lib_name}")
        lines.append(f"LIB_OBJS := {objs_escaped}")
        lines.append(f"LIB_HEADERS := {' '.join(headers)}")
        lines.append("")

    # -- Internal autocall archive --
    # A project-private archive of objects that every module (and test)
    # autocalls.  For multi-module projects whose modules share a body of code
    # but cannot glob it into each module -- e.g. each module root defines its
    # own main(), so globbing all sources into every module is doubly-defined.
    # Each module lists only its root source(s); the linker pulls the shared
    # rest from this archive by autocall (referenced members only -> smaller
    # load modules, important on MVS).  Unlike [lib] (a public deliverable,
    # shipped in the release tarball), the internal archive is never shipped.
    internal = cfg.get("internal", {})
    if internal:
        int_sources = _resolve_sources(
            internal.get("sources", []),
            internal.get("exclude", []),
        )
        int_objs = [_src_to_obj(s, builddir) for s in int_sources]
        all_src_dirs.update(_collect_src_dirs(int_sources))
        all_objs.update(int_objs)

        int_archive = os.path.join(builddir, f"{name}int.a")
        int_objs_escaped = " ".join(_make_escape(o) for o in int_objs)
        lines.append("# -- Internal autocall archive --")
        lines.append(f"INTERNAL_ARCHIVE := {int_archive}")
        lines.append(f"INTERNAL_OBJS := {int_objs_escaped}")
        lines.append("")

    # -- Source directories for vpath --
    if all_src_dirs:
        lines.append("# -- Source paths --")
        lines.append(f"SRC_DIRS := {' '.join(sorted(all_src_dirs))}")
        lines.append("")

    # -- All objects (for clean target) --
    lines.append("# -- All objects --")
    lines.append(
        f"ALL_OBJS := "
        f"{' '.join(_make_escape(o) for o in sorted(all_objs))}"
    )
    lines.append("")

    # -- Release config --
    release = cfg.get("release", {})
    if release:
        lines.append("# -- Release --")
        vfiles = release.get("version_files", [])
        lines.append(f"RELEASE_VERSION_FILES := {' '.join(vfiles)}")
        lines.append("")

    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description="mbt v2 config generator")
    parser.add_argument("--project", default="project.toml")
    parser.add_argument("--builddir", default="build")
    parser.add_argument("--output", choices=["shell", "file"], default="shell",
                        help="shell: print to stdout; file: write .mbt/config.mk")
    args = parser.parse_args()

    if not os.path.exists(args.project):
        print(f"[mbt] ERROR: {args.project} not found", file=sys.stderr)
        sys.exit(1)

    try:
        content = generate(args.project, args.builddir)
    except ConfigError as e:
        print(f"[mbt] ERROR: {e}", file=sys.stderr)
        if args.output == "file":
            os.makedirs(".mbt", exist_ok=True)
            Path(".mbt/config.mk").write_text(_error_config_mk(str(e)))
        sys.exit(2)   # 2 = configuration error (spec 11.1)

    # Build provenance header (.mbt/buildstamp.h) -- see mbt/buildstamp.py.
    # Written every run but only *rewritten* when the commit/version changed,
    # so it never touches the mtime for nothing. Re-parsing project.toml here
    # keeps generate() a pure string function (its tests call it directly).
    meta = _parse_toml(args.project).get("project", {})
    buildstamp.generate(
        meta.get("name", "unknown"),
        meta.get("version", "0.0.0"),
    )

    if args.output == "file":
        os.makedirs(".mbt", exist_ok=True)
        Path(".mbt/config.mk").write_text(content)
    else:
        print(content, end="")


if __name__ == "__main__":
    main()
