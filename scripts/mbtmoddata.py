#!/usr/bin/env python3
"""mbt module-data check -- no writable data in a module that promises not to have any.

cc370 keeps a module's writable data in the module itself: a C static or a
non-const global becomes storage in the CSECT (`@V1 DS XL4`), written with ST.
Two kinds of load module must not have any:

- **rent = true** -- a reentrant module is ONE copy for every task that LINKs
  it.  httpd LINKs its CGI modules from several worker subtasks at once;
  measured on mvsdev (cc370#100) one HTTPDM copy had CDUSE=3, so a static in
  it is unsynchronised state shared by concurrent requests.
- **ac = 1** -- fetched authorized from an APF library, the module lands in
  key-0 storage, and a key-8 task abends S0C4 on the first store into it
  (ufsd#64, httpd#197, ftpd#101).  Whether a module is ever fetched that way
  is the project's knowledge, not mbt's (brexx370's BREXX is AC=1 and keeps
  state in statics on purpose), so this is a warning; ufsd and ftpd enforce
  it themselves.

Only **rent = true** stops the build: it is an explicit promise.  A module
that gets RENT from ld370's default, or is ac = 1, is warned about -- that
default is the defect cc370#100 removes, and the project decides: remove the
data, or declare rent = false.

`__stklen` is not reported: libc370's startup reads it (WXTRN @@STKLEN) as the
stack size, it is a link-time setting that nothing writes.

Checks the C sources of every [[module]] and [[test]] (after `exclude`), and
reports [internal] sources separately: they reach a module by autocall, only
if referenced.  Hand-written assembler is not parsed -- its storage is explicit.

This is a text proxy, not the proof.  It cannot tell a const pointer from a
pointer to const.  The precise check is `cc370 -S` and a store through a
register loaded from `=A(@Vn)`.  Scanner from ufsd/ftpd tools/check-module-data.py
(mbt#91), generalised from AC(1) to RENT.

Exit: 0 clean (warnings allowed), 1 writable data in a rent = true module,
2 configuration error.

Usage: make module-data [MODDATA_ARGS=--all]
       mbtmoddata.py [--project project.toml] [--all] -- <CFLAGS>
"""

import argparse
import glob
import os
import re
import subprocess
import sys

import tomllib


def strip_noise(src):
    """Blank out comments, string/char literals and preprocessor lines.

    Newlines are preserved so reported line numbers stay usable.
    """
    out = []
    i, n = 0, len(src)
    while i < n:
        c = src[i]
        if c == '/' and src[i:i + 2] == '/*':
            j = src.find('*/', i + 2)
            j = n if j < 0 else j + 2
            out.append(''.join(ch if ch == '\n' else ' ' for ch in src[i:j]))
            i = j
            continue
        if c == '/' and src[i:i + 2] == '//':
            j = src.find('\n', i)
            j = n if j < 0 else j
            out.append(' ' * (j - i))
            i = j
            continue
        if c in '"\'':
            quote, j = c, i + 1
            while j < n and src[j] != quote:
                j += 2 if src[j] == '\\' else 1
            out.append('""' + ' ' * max(0, j - i - 1))
            i = j + 1
            continue
        out.append(c)
        i += 1
    text = ''.join(out)
    return '\n'.join('' if l.lstrip().startswith('#') else l
                     for l in text.split('\n'))


def declarations(src):
    """Yield (line, text, depth) for every statement, file scope and inside
    function bodies alike -- a function-local `static` is module storage too.

    A '{' right after '=' or ',' opens an initializer, not a block.  The
    declarator that closes a `typedef struct { ... } NAME;` is a type name, not
    data -- but `struct tag { ... } instance;` is data, so only typedef tails
    are dropped.
    """
    depth, init, buf, line, start = 0, 0, '', 1, 1
    heads, typetail, tailhead = [], False, ''
    # The declarator after a struct/union/enum body inherits the words before
    # the body: `static const struct { ... } tbl[] = {...};` declares a const
    # table, and without its head the tail `tbl[] = ...` looked writable.
    for ch in src:
        if ch == '\n':
            line += 1
        if ch == '{':
            if init or buf.rstrip().endswith(('=', ',')):
                init += 1               # aggregate initializer, keep reading
                buf += ' '
                continue
            depth += 1
            heads.append(' '.join(buf.split()))
            buf, start = '', line
            continue
        if ch == '}':
            if init:
                init -= 1
                buf += ' '
                continue
            depth = max(0, depth - 1)
            popped = heads.pop() if heads else ''
            typetail = bool(re.search(r'\btypedef\b', popped))
            # only a TYPE head -- one that ends in struct/union/enum [tag] --
            # passes its words on; a function head with a struct parameter
            # ends in ')' and must not
            tailhead = popped if re.search(
                r'\b(struct|union|enum)(\s+\w+)?\s*$', popped) else ''
            buf, start = '', line
            continue
        if ch == ';' and not init:
            head = ' '.join(buf.split())
            if head and not typetail:
                yield start, (tailhead + ' ' + head).strip(), depth
            typetail, tailhead = False, ''
            buf, start = '', line
            continue
        if not buf.strip():
            start = line
        buf += ch


# A head ending in ')' is a function, unless an initializer `= f(1)` ends it.
# The parameter list may nest: `void f(void (*cb)(int))`.
IS_FUNC = re.compile(r'^[^=]*\)\s*$')
# Only the FIRST parenthesis decides: `int (*fp)(int)` is a pointer, while
# `void f(void (*cb)(int))` is a prototype with a pointer parameter.
IS_FUNC_PTR = re.compile(r'^[^(]*\(\s*\*')
SKIPPABLE = re.compile(r'\b(typedef|extern)\b')
IS_CONST = re.compile(r'\bconst\b')
IS_TAG_ONLY = re.compile(r'^(struct|union|enum)\s+\w+$')


def strip_attributes(head):
    """Drop GNU `__attribute__((...))`, whose parentheses nest.

    `struct tag { ... } __attribute__((aligned(4)));` defines a type and no
    data, but with the attribute left in it no longer looked like a bare tag.
    """
    out, i = '', 0
    while True:
        m = re.search(r'\b__attribute__\s*\(', head[i:])
        if not m:
            return ' '.join((out + head[i:]).split())
        out += head[i:i + m.start()]
        j, level = i + m.end(), 1
        while j < len(head) and level:
            level += {'(': 1, ')': -1}.get(head[j], 0)
            j += 1
        i = j


def mutable(head, depth):
    head = strip_attributes(head)
    if SKIPPABLE.search(head) or IS_CONST.search(head):
        return False
    if depth and not head.startswith('static'):
        return False        # an ordinary local lives on the stack
    if IS_TAG_ONLY.match(head):
        return False
    if IS_FUNC.search(head) and not IS_FUNC_PTR.search(head):
        return False        # prototype or definition head
    return bool(re.search(r'\w', head))


def _c_sources(entry: dict) -> list[str]:
    files = set()
    for pattern in entry.get("sources", []):
        files |= set(glob.glob(pattern))
    for pattern in entry.get("exclude", []):
        files -= set(glob.glob(pattern))
    return sorted(f for f in files if f.endswith(".c"))


# Runtime-convention data a module defines but nothing writes.
_EXEMPT = re.compile(r'\b__stklen\b')


_MARKER = re.compile(r'^#\s*(\d+)\s+"([^"]*)"(.*)$')


def _map_lines(out: str, path: str) -> dict[str, str] | None:
    """Split `cc370 -E` output back into the files it came from, each line at
    its own line number (from the line markers): the source itself and every
    header the project or mbt supplies.  A header can define data too --
    mbt's own mbtcheck.h has `static int mbt_run`, which 2.1.1 missed.  System headers (marker flag 3: the libc370 sysroot) and the
    `<built-in>`/`<command line>` pseudo-files are left out."""
    files: dict[str, dict[int, str]] = {}
    cur, num, skip = None, 0, True
    for raw in out.split("\n"):
        m = _MARKER.match(raw)
        if m:
            num, cur = int(m.group(1)), m.group(2)
            skip = cur.startswith("<") or "3" in m.group(3).split()
            continue
        if not skip:
            files.setdefault(cur, {})[num] = raw
        num += 1
    if path not in files:
        return None
    return {f: "\n".join(lines.get(n, "") for n in range(1, max(lines) + 1))
            for f, lines in files.items()}


_PP_CACHE: dict[tuple, dict[str, str] | None] = {}


def _key(path: str, cflags: list[str]) -> tuple:
    return (os.path.abspath(path), tuple(cflags))


def preprocess_all(paths: list[str], cflags: list[str], jobs: int = 8) -> None:
    """Run `cc370 -E` over every path once, `jobs` at a time, into the cache.

    A file is usually linked into several modules; preprocessing it once, and
    the files in parallel, keeps the check from adding seconds to every make.
    (subprocess.Popen, no threads -- the stdlib set mbt allows.)"""
    todo = [p for p in dict.fromkeys(paths) if _key(p, cflags) not in _PP_CACHE]
    while todo:
        batch, todo = todo[:jobs], todo[jobs:]
        procs = []
        for path in batch:
            try:
                procs.append((path, subprocess.Popen(
                    ["cc370", "-E", *cflags, path], stdout=subprocess.PIPE,
                    stderr=subprocess.DEVNULL, text=True, errors="replace")))
            except OSError:
                _PP_CACHE[_key(path, cflags)] = None
        for path, proc in procs:
            out, _ = proc.communicate()
            _PP_CACHE[_key(path, cflags)] = (_map_lines(out, path)
                                             if proc.returncode == 0 else None)


def _preprocessed(path: str, cflags: list[str]) -> dict[str, str] | None:
    """The file as cc370 compiles it for MVS: `cc370 -E` with the project's
    flags, split back into the source and the non-system headers it includes
    (see _map_lines).  A branch the preprocessor drops -- a host
    simulation under `#ifndef __MVS__` -- is not scanned.  None if cc370 is not
    there or fails; the caller then scans the raw text."""
    if _key(path, cflags) not in _PP_CACHE:
        preprocess_all([path], cflags)
    return _PP_CACHE[_key(path, cflags)]


def _findings(path: str, cflags: list[str] | None = None) -> list[tuple[str, int, str]]:
    """(file, line, declaration) for every writable definition the source
    brings into the module -- in the source itself and, preprocessed, in the
    project's and mbt's headers it includes."""
    texts = _preprocessed(path, cflags) if cflags is not None else None
    if texts is None:
        with open(path, encoding="utf-8", errors="replace") as fh:
            texts = {path: fh.read()}
    out = []
    for f, text in texts.items():
        for line, head, depth in declarations(strip_noise(text)):
            if mutable(head, depth) and not _EXEMPT.search(head):
                out.append((f, line, head))
    return out


def _rent(entry: dict):
    """True / False when declared (rent, or legacy norent), else None."""
    if "rent" in entry:
        return bool(entry["rent"])
    if entry.get("norent"):
        return False
    return None


def check(project: dict, cflags: list[str] | None = None) -> tuple[list, list]:
    """Return (errors, warnings), each a list of (kind, name, path, line, text)."""
    errors, warnings = [], []
    entries = ([("module", m) for m in project.get("module", [])]
               + [("test", t) for t in project.get("test", [])])
    if cflags is not None:
        preprocess_all([p for _, e in entries for p in _c_sources(e)]
                       + _c_sources(project.get("internal", {})), cflags)
    for kind, entry in entries:
        name = entry.get("name", "?")
        rent = _rent(entry)
        ac1 = entry.get("ac", 0) == 1
        if rent is False and not ac1:
            continue
        if rent is True:
            why, fatal = "rent = true", True
        elif ac1:
            why, fatal = "ac = 1: key-0 storage if fetched authorized", False
        elif kind == "test":
            continue        # tests are not LINKed concurrently; only declared ones
        else:
            why, fatal = "RENT by ld370's default", False
        seen = set()
        for path in _c_sources(entry):
            for f, line, head in _findings(path, cflags):
                if (f, line) in seen:
                    continue        # a header included by several sources
                seen.add((f, line))
                (errors if fatal else warnings).append(
                    (why, name, f, line, head))
    # [internal] objects reach a module by autocall, only when referenced --
    # which this scan cannot see before the link.  So they are reported
    # whenever some module may be RENT, not only when another finding exists
    # (2.1.1 was silent for httpd, whose HTTPD links nine of them).  Which
    # module really links which object is a load-map question (mbt#152).
    internal = project.get("internal", {})
    maybe_rent = any(_rent(e) is not False for _, e in entries
                     if _ == "module")
    if internal and maybe_rent:
        seen = set()
        for path in _c_sources(internal):
            for f, line, head in _findings(path, cflags):
                if (f, line) not in seen:
                    seen.add((f, line))
                    warnings.append(("[internal], linked by autocall if "
                                     "referenced", "-", f, line, head))
    return errors, warnings


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--project", default="project.toml")
    ap.add_argument("cflags", nargs="*",
                    help="the project's CFLAGS, after '--': files are then "
                         "preprocessed with cc370 -E and only the MVS branch "
                         "is scanned")
    ap.add_argument("--all", action="store_true",
                    help="list every warning, not the first three per module")
    args = ap.parse_args()
    try:
        with open(args.project, "rb") as fh:
            project = tomllib.load(fh)
    except (OSError, tomllib.TOMLDecodeError) as e:
        print(f"[mbt] ERROR: {args.project}: {e}", file=sys.stderr)
        return 2
    if not args.cflags:
        # Called by hand, without the project's CFLAGS: nothing is
        # preprocessed, so host-only branches count (rexx370 on 2.1.1).
        print("[mbt] WARNING: no CFLAGS given -- scanning the raw sources, "
              "host-only (#ifndef __MVS__) branches included. "
              "'make module-data' passes them.", file=sys.stderr)
    errors, warnings = check(project, args.cflags if args.cflags else None)
    # Warnings are grouped per module and capped, so a project that keeps
    # state in a non-RENT module on purpose does not drown every build log;
    # --all lists them.  Errors are always listed in full.
    groups: dict = {}
    for w in warnings:
        groups.setdefault((w[0], w[1]), []).append(w)
    for (why, name), items in groups.items():
        shown = items if args.all else items[:3]
        for _, _, path, line, head in shown:
            print(f"[mbt] WARNING: writable data in {name} ({why}): "
                  f"{path}:{line}: {head[:80]}", file=sys.stderr)
        if len(items) > len(shown):
            print(f"[mbt] WARNING:   ... {len(items) - len(shown)} more in "
                  f"{name}; 'make module-data MODDATA_ARGS=--all' lists them",
                  file=sys.stderr)
    for why, name, path, line, head in errors:
        print(f"[mbt] ERROR: writable data in {name} ({why}): "
              f"{path}:{line}: {head[:80]}", file=sys.stderr)
    if errors:
        print("[mbt] A rent = true module is one copy shared by every task "
              "that LINKs it, so it may hold no writable data. Move it onto "
              "the heap or the stack, make it const, or declare rent = false "
              "(cc370#100).", file=sys.stderr)
        return 1
    whys = {w[0] for w in warnings}
    if "RENT by ld370's default" in whys:
        print("[mbt] A module that is RENT only by ld370's default: remove the "
              "data and declare rent = true, or declare rent = false "
              "(cc370#100).", file=sys.stderr)
    if any(w.startswith("ac = 1") for w in whys):
        print("[mbt] An ac = 1 module fetched authorized from an APF library "
              "is key-0 storage, and a store into it abends S0C4 -- fine only "
              "if it never runs that way.", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
