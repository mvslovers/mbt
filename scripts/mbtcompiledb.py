"""mbt compiledb — generate clangd compile_commands.json.

Reads project.toml and generates a compilation database so that
clangd can provide diagnostics, completion, and navigation for
cross-compiled C sources.
"""

import sys
import json
import argparse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))

from mbt import EXIT_SUCCESS, EXIT_CONFIG
from mbt.config import MbtConfig
from mbtconfig import build_variables


def main():
    parser = argparse.ArgumentParser(
        description="Generate compile_commands.json for clangd"
    )
    parser.add_argument("--project", default="project.toml",
                        help="Path to project.toml")
    args = parser.parse_args()

    project_path = Path(args.project)
    if not project_path.exists():
        print("[mbt] ERROR: project.toml not found", file=sys.stderr)
        return EXIT_CONFIG

    cfg = MbtConfig(project_path)
    variables = build_variables(cfg)
    project_dir = project_path.parent.resolve()

    includes = variables.get("INCLUDES", "").split()
    includes.insert(0, "-Iinclude")
    cflags = variables.get("CFLAGS", "-S -O1").split()
    c_dirs = variables.get("C_DIRS", "src/").split()

    # clangd-specific flags for MVS cross-compilation
    clangd_flags = [
        "-xc",
        "-std=gnu89",
        "-nostdinc",
        "-U__LP64__",
        "-D__MVS__",
        "-ferror-limit=0",
        "-Wno-comment",
        "-Wno-pragma-pack",
    ]

    entries = []
    for d in c_dirs:
        src_dir = project_dir / d
        if not src_dir.exists():
            continue
        for f in sorted(src_dir.glob("*.c")):
            command_parts = ["cc"]
            command_parts.extend(clangd_flags)
            command_parts.extend(cflags)
            command_parts.extend(includes)
            command_parts.extend(["-c", str(f.relative_to(project_dir))])
            entries.append({
                "directory": str(project_dir),
                "command": " ".join(command_parts),
                "file": str(f.relative_to(project_dir))
            })

    out_path = project_dir / "compile_commands.json"
    with open(out_path, "w") as fh:
        json.dump(entries, fh, indent=2)
        fh.write("\n")

    print("[mbt] Generated %s (%d entries)" % (out_path.name, len(entries)))
    return EXIT_SUCCESS


if __name__ == "__main__":
    sys.exit(main())
