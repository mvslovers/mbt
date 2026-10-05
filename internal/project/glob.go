package project

import (
	"path/filepath"
	"sort"
	"strings"
)

// globSorted expands one pattern the way mbt v2 did (Python's glob.glob,
// sorted): non-recursive, and a wildcard never matches a name that starts
// with a dot unless the pattern component does too.
func globSorted(root, pattern string) []string {
	matches, err := filepath.Glob(filepath.Join(root, pattern))
	if err != nil {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	var out []string
	for _, m := range matches {
		rel, err := filepath.Rel(root, m)
		if err != nil {
			continue
		}
		if hiddenMismatch(parts, strings.Split(filepath.ToSlash(rel), "/")) {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// hiddenMismatch reports whether a matched path has a dot-name where its
// pattern component is a wildcard that does not itself start with a dot.
func hiddenMismatch(pat, got []string) bool {
	if len(pat) != len(got) {
		return false
	}
	for i := range pat {
		if strings.HasPrefix(got[i], ".") && !strings.HasPrefix(pat[i], ".") &&
			strings.ContainsAny(pat[i], "*?[") {
			return true
		}
	}
	return false
}

// resolveSources expands patterns in order, drops everything an exclude
// pattern matches, and removes duplicates keeping the first occurrence --
// exactly v2's _resolve_sources.  Patterns that match nothing are returned so
// the caller can warn.
func resolveSources(root string, patterns, exclude []string) (files, empty []string) {
	for _, p := range patterns {
		m := globSorted(root, p)
		if len(m) == 0 {
			empty = append(empty, p)
		}
		files = append(files, m...)
	}
	ex := map[string]bool{}
	for _, p := range exclude {
		for _, m := range globSorted(root, p) {
			ex[m] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if ex[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out, empty
}
