// Package compiledb writes compile_commands.json for clangd -- mbt v2's
// `make compiledb` (scripts/mbtcompiledb.py).
//
// It is a clang database, not a cc370 one: arguments[0] is "clang" and the
// flags are clang's, because tools that run the command (CLion) would get a
// GCC 3.4.6 that rejects them (#118).
package compiledb

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var clangdFlags = []string{"-xc", "--target=powerpc-unknown-eabi", "-std=gnu99", "-nostdinc",
	"-D__MVS__", "-ferror-limit=0", "-Wno-comment", "-Wno-pragma-pack"}

type entry struct {
	Directory string   `json:"directory"`
	Arguments []string `json:"arguments"`
	File      string   `json:"file"`
}

// sysrootInclude: libc370's include dir, in cc370's tree or its second
// sysroot, else ~/.local/cc370.
func sysrootInclude() string {
	var roots []string
	if cc, err := exec.LookPath("cc370"); err == nil {
		if r, err := filepath.EvalSymlinks(cc); err == nil {
			cc = r
		}
		roots = append(roots, filepath.Join(filepath.Dir(filepath.Dir(cc)), "cc370"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".local", "cc370"))
	}
	for _, r := range roots {
		for _, c := range []string{filepath.Join(r, "include"), filepath.Join(r, "libc370", "include")} {
			if _, err := os.Stat(filepath.Join(c, "stdio.h")); err == nil {
				return c
			}
		}
	}
	return ""
}

// Write builds the database for every C source of the project (modules,
// tests, [lib], [internal]; deduplicated, in that order).
func Write(root string, cflags []string, sources []string, mbtInclude string) (int, error) {
	var inc []string
	deps, _ := filepath.Glob(filepath.Join(root, ".mbt", "deps", "*", "include"))
	sort.Strings(deps)
	for _, d := range deps {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			inc = append(inc, "-I", d)
		}
	}
	inc = append(inc, "-I", mbtInclude, "-I", filepath.Join(root, ".mbt"))
	if s := sysrootInclude(); s != "" {
		inc = append(inc, "-I", s)
	}
	seen := map[string]bool{}
	entries := []entry{}
	for _, src := range sources {
		if seen[src] || !strings.HasSuffix(src, ".c") {
			continue
		}
		seen[src] = true
		args := append(append(append(append([]string{"clang"}, clangdFlags...), cflags...), inc...), "-c", src)
		entries = append(entries, entry{root, args, src})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		return 0, err
	}
	return len(entries), os.WriteFile(filepath.Join(root, "compile_commands.json"), buf.Bytes(), 0o644)
}
