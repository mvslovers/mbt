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
    _validate_aliases(cfg)


def _validate_aliases(cfg: dict) -> None:
    """Check the `aliases` of every [[module]] (issue #112).

    An alias is a directory entry of the same library, so it obeys the member
    name rule and may not repeat any other name in it -- a module's or another
    alias.  ld370 refuses the same library too -- an alias equal to its own
    module when linking, a clash across modules only when --pack builds the
    library for deploy or package -- but without naming the project.toml
    block.  This names it, and at `make`.

    [[test]] takes no aliases: a test is run by its own name, and an ignored
    key would read as though it did something.
    """
    for entry in cfg.get("test", []):
        if "aliases" in entry:
            raise ConfigError(
                f'project.toml: test "{entry.get("name")}" has aliases -- '
                f"only a [[module]] can have them"
            )
    owner = {m["name"]: m["name"] for m in cfg.get("module", [])}
    for mod in cfg.get("module", []):
        aliases = mod.get("aliases", [])
        if not isinstance(aliases, list):
            raise ConfigError(
                f'project.toml: module "{mod["name"]}" aliases must be a list '
                f'of names, e.g. aliases = ["REXX", "RX"]'
            )
        for alias in aliases:
            if not isinstance(alias, str) or not alias:
                raise ConfigError(
                    f'project.toml: module "{mod["name"]}" has an alias that '
                    f"is not a name: {alias!r}"
                )
            _check_member_name(alias, f'module {mod["name"]} alias')
            if alias in owner:
                other = owner[alias]
                what = ("module" if other == alias
                        else f'an alias of module "{other}"')
                raise ConfigError(
                    f'project.toml: alias "{alias}" of module "{mod["name"]}" '
                    f"is already {what} -- one name per library"
                )
            owner[alias] = mod["name"]


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


# The values `startup` accepts.  None is "not set".  The CRT is a member of
# libc.a since libc370 2.3.0 (libc370#159), and ld370 pulls it by the entry
# name since cc370 1.2.0 (cc370#107) -- so a C module names no startfile at
# all, and `startup` survives only for the two cases the archive does not
# cover: false (an own entry, no C runtime) and "crtm" (the nested startup,
# still a separate object).  "crt0" and "crt1" are accepted for the
# transition and mean the same as leaving the key out (#158).
_STARTUP_VALUES = (None, False, "crt0", "crt1", "crtm")
_STARTUP_LEGACY = ("crt0", "crt1")


def _startup_to_link_cmd(startup) -> str:
    """Map a (validated) startup value to a LINK_* macro name.

    LINK_C links no startfile: the CRT comes out of libc.a by the entry name,
    or -- on a sysroot whose libc.a predates libc370 2.3.0 -- from the startfile
    named by MODULE_<key>_STARTFILE (see mk/mbt.mk).
    """
    if startup is False:
        return "LINK_NOCRT"
    if startup == "crtm":
        return "LINK_CRTM"
    return "LINK_C"


def _check_startup(mod: dict, name: str):
    """Return the module's startup value; raise on one mbt does not know.

    An unknown value used to fall back to crt0 silently, so a typo linked a
    different startup than the one asked for.
    """
    startup = mod.get("startup")
    if startup is True or startup not in _STARTUP_VALUES:
        raise ConfigError(
            f"[[module]] {name}: startup = {startup!r} is not a valid value "
            f"(leave it out for a C program; false for an own entry; \"crtm\")")
    dep = mod.get("dep_startup")
    if dep is not None and not isinstance(dep, bool):
        raise ConfigError(
            f"[[module]] {name}: 'dep_startup' must be true or false, "
            f"not {dep!r}")
    return startup


def _collect_src_dirs(sources: list) -> set:
    """Extract unique parent directories from a list of source files."""
    dirs = set()
    for s in sources:
        d = os.path.dirname(s)
        if d:
            dirs.add(d)
    return dirs


# Defaults
DEFAULT_ENTRY   = "@@CRT0"    # standard C entry point; the CRT in libc.a


# Warnings meant for the user.  mk/mbt.mk drops every '[mbt]' line this script
# writes to stderr (they are progress chatter), so a warning would never be
# seen; generate() also emits each one as $(info ...) into config.mk, where
# make prints it on every run.
_WARNINGS: list[str] = []


def _warn(msg: str) -> None:
    print(f"[mbt] WARNING: {msg}", file=sys.stderr)
    _WARNINGS.append(msg)


def _link_attrs(mod: dict, name: str) -> list[str]:
    """The ld370 flags for a module's RENT / REUS / REFR attributes.

    A declared attribute is passed explicitly in BOTH directions -- rent = true
    gives --rent, rent = false gives --norent -- so the load module does not
    depend on ld370's default.  That default is RENT+REUS today and becomes
    'neither' (what IEWL does) once cc370#100 ships; with both directions
    spelled out, that change alters no build that declared its attributes.
    An undeclared attribute passes nothing and keeps whatever ld370 does.
    REFR has no negative flag in ld370 and is off unless asked for.

    Why it matters: cc370 keeps a module's writable statics in the CSECT, and
    a RENT module LINKed by several tasks at once (httpd's workers) is ONE
    copy -- measured with CDUSE=3 on mvsdev.  rent = true is a promise that
    the module has no writable data; mbtmoddata.py checks it.

    norent / noreus are the old spelling: still accepted, mapped to rent /
    reus = false, with a warning.
    """
    want = {}
    for attr in ("rent", "reus", "refr"):
        if attr in mod:
            if not isinstance(mod[attr], bool):
                raise ConfigError(
                    f"[[module]] {name}: '{attr}' must be true or false, "
                    f"not {mod[attr]!r}")
            want[attr] = mod[attr]
    for old, attr in (("norent", "rent"), ("noreus", "reus")):
        if old not in mod:
            continue
        if attr in want:
            raise ConfigError(
                f"[[module]] {name}: both '{old}' and '{attr}' are set; "
                f"keep '{attr}' only")
        if mod[old]:
            _warn(f"[[module]] {name}: '{old}' is deprecated, "
                  f"write '{attr} = false'")
            want[attr] = False
    flags = []
    for attr in ("rent", "reus", "refr"):
        if attr not in want:
            continue
        if want[attr]:
            flags.append(f"--{attr}")
        elif attr != "refr":
            flags.append(f"--no{attr}")
    return flags


def _emit_module(lines, mod, builddir, all_src_dirs, all_objs, var_prefix):
    """Emit make variables for a single module or test."""
    mod_name = mod["name"]
    entry = mod.get("entry", DEFAULT_ENTRY)
    startup = _check_startup(mod, mod_name)
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
    if link_cmd == "LINK_C":
        # Only read on a sysroot whose libc.a has no @@CRT0 (libc370 < 2.3.0):
        # the startfile such a link still needs.  crt0 unless crt1 was named,
        # exactly what the module linked before #158.
        legacy = startup if startup in _STARTUP_LEGACY else "crt0"
        lines.append(f"MODULE_{key}_STARTFILE := {legacy}")
    # dep_startup = true: @@START comes from a dependency (an httpd CGI
    # launcher), so libc370 is not searched ahead of the dependencies (#62).
    if mod.get("dep_startup") is True:
        lines.append(f"MODULE_{key}_DEP_STARTUP := 1")
    # APF authorization code (SETCODE AC(n)); only emitted when non-zero so
    # modules without it pass no --ac to ld370 (default AC(0)).
    ac = mod.get("ac", 0)
    if ac:
        lines.append(f"MODULE_{key}_AC := {ac}")
    # rent / reus / refr: the load module's attributes, passed to ld370 as
    # flags (cc370#100).
    attrs = _link_attrs(mod, mod_name)
    if attrs:
        lines.append(f"MODULE_{key}_ATTRS := {' '.join(attrs)}")
    # aliases: extra directory entries for the same load module (IEWL ALIAS),
    # passed to ld370 as --alias.  Escaped like _NAME: a '#' may only reach
    # the recipe through variable expansion.
    aliases = mod.get("aliases", [])
    if aliases:
        lines.append(f"MODULE_{key}_ALIASES := "
                     + " ".join(_make_escape(a) for a in aliases))
    lines.append("")


def generate(project_file: str = "project.toml", builddir: str = "build") -> str:
    """Generate make variable assignments from project.toml."""
    cfg = _parse_toml(project_file)
    _validate_names(cfg)
    _WARNINGS.clear()
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

    # One warning for the whole project, not one per module: a project names
    # crt1 on every module and test (rexx370 ~60, nsf370 ~70), and a warning
    # per line on every make would bury everything else (#158).
    legacy = [m.get("name", "?") for m in modules + tests
              if m.get("startup") in _STARTUP_LEGACY and m.get("mvs") is not False]
    if legacy:
        shown = ", ".join(legacy[:5]) + (", ..." if len(legacy) > 5 else "")
        _warn(f"{len(legacy)} module(s)/test(s) name startup = \"crt0\" or "
              f"\"crt1\" ({shown}); the CRT comes out of libc.a now -- drop "
              f"the key, with [toolchain] libc370 >= 2.3.0 (#158)")

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

    # -- Distribution (SMP4 installation package) --
    # The presence of the table is the switch. 'package' builds the SMP
    # artifacts only for a project that declares one, so every project without
    # a [distribution] keeps producing exactly what it produced before.
    if cfg.get("distribution"):
        lines.append("# -- Distribution --")
        lines.append("HAS_DISTRIBUTION := 1")
        lines.append("")

    # -- Release config --
    release = cfg.get("release", {})
    if release:
        lines.append("# -- Release --")
        vfiles = release.get("version_files", [])
        lines.append(f"RELEASE_VERSION_FILES := {' '.join(vfiles)}")
        lines.append("")

    if _WARNINGS:
        lines.append("# -- Warnings --")
        # '#' would start a comment, and make 3.81 prints a '\#' escape inside
        # $(info) literally -- so the character comes from a variable.
        lines.append("MBT_HASH := \\#")
        for w in _WARNINGS:
            lines.append("$(info [mbt] WARNING: "
                         + w.replace("$", "$$").replace("#", "$(MBT_HASH)")
                         + ")")
        lines.append("")

    return "\n".join(lines) + "\n"


def archive_symbols(ar: str, archive: str) -> set:
    """The external names an ar370 archive's symbol table defines.

    `ar370 t` lists each member ("  name.o   1520 bytes") and then each
    symbol with its definer ("  @@START   httpcgi.o"); the second shape is
    the one wanted.
    """
    import subprocess
    r = subprocess.run([ar, "t", archive], capture_output=True, text=True)
    if r.returncode != 0:
        raise ConfigError(f"cannot list {archive}: {r.stderr.strip()}")
    names = set()
    for line in r.stdout.splitlines():
        f = line.split()
        if len(f) == 2 and f[1].endswith(".o"):
            names.add(f[0])
    return names


def check_dep_startup(project_file: str, ar: str, archives: list) -> list:
    """Modules that must say where their @@START comes from, and do not.

    libc370 is searched ahead of the dependencies, so a module gets libc's
    @@START unless it sets dep_startup = true (#62).  When a dependency
    archive defines @@START -- httpd's CGI launcher -- a module that says
    nothing would quietly lose that startup and still build green: a CGI
    module with no HTTP header.  So it has to say which it wants.  Tests are
    not asked: they always took libc's @@START.

    Returns one error line per module; empty when all is well.
    """
    defines = [a for a in archives
               if "@@START" in archive_symbols(ar, a)]
    if not defines:
        return []
    cfg = _parse_toml(project_file)
    errors = []
    for mod in cfg.get("module", []):
        name = mod.get("name", "?")
        if mod.get("startup") is False or "dep_startup" in mod:
            continue
        errors.append(
            f"[[module]] {name}: {', '.join(defines)} define(s) @@START; set "
            f"dep_startup = true to use it (CGI module) or dep_startup = false "
            f"for libc370's (#62)")
    return errors


def main():
    parser = argparse.ArgumentParser(description="mbt v2 config generator")
    parser.add_argument("--project", default="project.toml")
    parser.add_argument("--builddir", default="build")
    parser.add_argument("--output", choices=["shell", "file"], default="shell",
                        help="shell: print to stdout; file: write .mbt/config.mk")
    parser.add_argument("--check-dep-startup", nargs="*", metavar="ARCHIVE",
                        help="check that every module says where @@START "
                             "comes from when one of these archives defines it")
    parser.add_argument("--ar", default="ar370")
    args = parser.parse_args()

    if args.check_dep_startup is not None:
        try:
            errors = check_dep_startup(args.project, args.ar,
                                       args.check_dep_startup)
        except ConfigError as e:
            errors = [str(e)]
        for e in errors:
            print(f"[mbt] ERROR: {e}", file=sys.stderr)
        sys.exit(2 if errors else 0)

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
