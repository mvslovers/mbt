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
CLANGD_FLAGS = [
    "-xc",
    "-std=gnu89",
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


def _all_sources(cfg: dict) -> list:
    """Every C source across [[module]], [[test]] and [lib] (deduped)."""
    srcs = []
    for m in cfg.get("module", []) + cfg.get("test", []):
        srcs += _resolve_sources(m.get("sources", []), m.get("exclude", []))
    lib = cfg.get("lib", {})
    if lib:
        srcs += _resolve_sources(lib.get("sources", []))
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
    inc = []
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
