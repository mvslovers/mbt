"""mbt compiledb (v2) — generate clangd compile_commands.json.

Reads the v2 project.toml, resolves every C source across modules, tests
and the library, and writes a compilation database so clangd can provide
diagnostics, completion and navigation for the cc370 cross-build.

Each entry compiles with cc370 + the project cflags + the cc370 sysroot
include dir (so clangd finds <clibecb.h> etc.), plus clang-friendly flags
to parse the MVS C dialect.
"""

import sys
import json
import shutil
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))   # scripts/ for mbt + siblings

from mbt import EXIT_SUCCESS, EXIT_CONFIG
from mbtconfig import _parse_toml, _resolve_sources

# clang-side flags so clangd parses the cc370 C dialect cleanly.
#
# clang/LLVM has no S/370 backend, so clangd falls back to the host target
# (e.g. little-endian arm64, LP64) -- wrong for the i370/MVS build. We steer
# it as close to the real target as clang allows:
#   --target=s390x-ibm-linux  z/Architecture is the S/370 descendant and is
#                             big-endian like i370, so byte-order-dependent
#                             headers/structs parse correctly.
#   -U__LP64__                s390x is 64-bit; undefining __LP64__ makes LP32
#                             headers (e.g. time64.h) take their 32-bit branch,
#                             matching the real ILP32 i370 build.
#   -std=gnu99                the cc370 build dialect: C99 plus the GNU 'asm'
#                             keyword the crent370 headers use (strict -std=c99
#                             rejects 'asm' and yields hundreds of errors).
CLANGD_FLAGS = [
    "-xc",
    "--target=s390x-ibm-linux",
    "-U__LP64__",
    "-std=gnu99",
    "-nostdinc",
    "-D__MVS__",
    "-ferror-limit=0",
    "-Wno-comment",
    "-Wno-pragma-pack",
]


def _sysroot_include() -> Path | None:
    """The cc370 sysroot include dir (carries clibecb.h, stdio.h, ...)."""
    cc = shutil.which("cc370")
    if cc:
        cand = Path(cc).resolve().parent.parent / "cc370" / "include"
        if cand.is_dir():
            return cand
    fb = Path.home() / ".local" / "cc370" / "include"
    return fb if fb.is_dir() else None


def _dep_includes(project_dir: Path) -> list:
    """Staged dependency headers (.mbt/deps/<repo>/include).

    Mirrors DEP_INCLUDES in mk/mbt.mk so clangd resolves dependency
    headers (e.g. <libufs.h>) the same way the cc370 build does.
    Empty when no dependencies are staged ('make deps' not run).
    """
    inc = []
    for d in sorted((project_dir / ".mbt" / "deps").glob("*/include")):
        if d.is_dir():
            inc += ["-I", str(d)]
    return inc


def _all_sources(cfg: dict) -> list:
    """Every C source across [[module]], [[test]], [lib] and [internal] (deduped)."""
    srcs = []
    for m in cfg.get("module", []) + cfg.get("test", []):
        srcs += _resolve_sources(m.get("sources", []), m.get("exclude", []))
    lib = cfg.get("lib", {})
    if lib:
        srcs += _resolve_sources(lib.get("sources", []))
    # [internal] holds the shared body a multi-module project autocalls; it is
    # the bulk of the source tree, so clangd needs it covered too.
    internal = cfg.get("internal", {})
    if internal:
        srcs += _resolve_sources(
            internal.get("sources", []), internal.get("exclude", [])
        )
    seen, out = set(), []
    for s in srcs:
        if s not in seen and s.endswith(".c"):
            seen.add(s)
            out.append(s)
    return out


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Generate compile_commands.json for clangd (cc370 build)"
    )
    parser.add_argument("--project", default="project.toml")
    args = parser.parse_args()

    project_path = Path(args.project)
    if not project_path.exists():
        print(f"[mbt] ERROR: {args.project} not found", file=sys.stderr)
        return EXIT_CONFIG

    cfg = _parse_toml(str(project_path))
    project_dir = project_path.resolve().parent

    cflags = cfg.get("build", {}).get("cflags", [])
    # Dependency headers first, then the cc370 sysroot (lowest priority) --
    # the same precedence the cc370 build uses (project cflags > deps > sysroot).
    inc = _dep_includes(project_dir)
    # mbt's own headers (mbtcheck.h) -- mirror the -I mbt/include the build adds.
    mbt_inc = Path(__file__).resolve().parent.parent / "include"
    if mbt_inc.is_dir():
        inc += ["-I", str(mbt_inc)]
    sysroot_inc = _sysroot_include()
    if sysroot_inc:
        inc += ["-I", str(sysroot_inc)]

    entries = []
    for src in _all_sources(cfg):
        arguments = ["cc370"] + CLANGD_FLAGS + list(cflags) + inc + ["-c", src]
        entries.append({
            "directory": str(project_dir),
            "arguments": arguments,
            "file": src,
        })

    out_path = project_dir / "compile_commands.json"
    with open(out_path, "w", encoding="utf-8") as fh:
        json.dump(entries, fh, indent=2)
        fh.write("\n")

    print(f"[mbt] Generated {out_path.name} ({len(entries)} entries)")
    return EXIT_SUCCESS


if __name__ == "__main__":
    sys.exit(main())
