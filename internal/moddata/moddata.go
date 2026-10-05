package moddata

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var marker = regexp.MustCompile(`^#\s*(\d+)\s+"([^"]*)"(.*)$`)

// mapLines splits `cc370 -E` output back into the files it came from, each
// line at its own line number: the source and every header the project or
// mbt supplies.  System headers (marker flag 3) and <built-in>/<command line>
// are left out.  nil when the source itself does not appear.
func mapLines(out, path string) []fileText {
	files := map[string]map[int]string{}
	var order []string
	cur, num, skip := "", 0, true
	for _, raw := range strings.Split(out, "\n") {
		if m := marker.FindStringSubmatch(raw); m != nil {
			num, _ = strconv.Atoi(m[1])
			cur = m[2]
			skip = strings.HasPrefix(cur, "<")
			for _, f := range strings.Fields(m[3]) {
				if f == "3" {
					skip = true
				}
			}
			continue
		}
		if !skip {
			if files[cur] == nil {
				files[cur] = map[int]string{}
				order = append(order, cur)
			}
			files[cur][num] = raw
		}
		num++
	}
	if _, ok := files[path]; !ok {
		return nil
	}
	var res []fileText
	for _, f := range order {
		lines := files[f]
		max := 0
		for n := range lines {
			if n > max {
				max = n
			}
		}
		parts := make([]string, max)
		for n := 1; n <= max; n++ {
			parts[n-1] = lines[n]
		}
		res = append(res, fileText{f, strings.Join(parts, "\n")})
	}
	return res
}

// fileText is one file of a preprocessed source, in order of appearance.
type fileText struct{ file, text string }

// Unit is what the check needs of a [[module]] or [[test]].
type Unit struct {
	Name     string
	Test     bool
	Rent     *bool // declared rent (or legacy norent = true -> false)
	AC1      bool
	Sources  []string
	Excludes []string
}

// Input is a project as the check sees it.
type Input struct {
	Root     string
	Units    []Unit
	Internal *Unit // [internal], or nil
	CC       string
	CFlags   []string // nil: scan the raw text, no preprocessing
	Jobs     int
}

// Report is (kind, unit, file, line, head) per finding.
type Report struct {
	Why  string
	Unit string
	Finding
}

// cSources: the unit's .c files, globbed as a set and sorted (v2's own rule
// here, not the build's ordered list).
func cSources(root string, srcs, excl []string) []string {
	set := map[string]bool{}
	for _, p := range srcs {
		m, _ := filepath.Glob(filepath.Join(root, p))
		for _, f := range m {
			if r, err := filepath.Rel(root, f); err == nil && !hidden(p, r) {
				set[filepath.ToSlash(r)] = true
			}
		}
	}
	for _, p := range excl {
		m, _ := filepath.Glob(filepath.Join(root, p))
		for _, f := range m {
			if r, err := filepath.Rel(root, f); err == nil {
				delete(set, filepath.ToSlash(r))
			}
		}
	}
	var out []string
	for f := range set {
		if strings.HasSuffix(f, ".c") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func hidden(pattern, rel string) bool {
	pp := strings.Split(filepath.ToSlash(pattern), "/")
	rp := strings.Split(rel, "/")
	if len(pp) != len(rp) {
		return false
	}
	for i := range pp {
		if strings.HasPrefix(rp[i], ".") && !strings.HasPrefix(pp[i], ".") && strings.ContainsAny(pp[i], "*?[") {
			return true
		}
	}
	return false
}

type scanner struct {
	in    Input
	mu    sync.Mutex
	cache map[string][]Finding
}

func (s *scanner) preprocess(paths []string) {
	jobs := s.in.Jobs
	if jobs <= 0 {
		jobs = 8
	}
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for _, p := range paths {
		s.mu.Lock()
		_, done := s.cache[p]
		if !done {
			s.cache[p] = nil
		}
		s.mu.Unlock()
		if done {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			f := s.scan(p)
			s.mu.Lock()
			s.cache[p] = f
			s.mu.Unlock()
		}(p)
	}
	wg.Wait()
}

// scan: the file as cc370 compiles it for MVS when CFLAGS are given (a host
// branch under #ifndef __MVS__ is not counted), the raw text otherwise or
// when cc370 fails.
func (s *scanner) scan(p string) []Finding {
	var texts []fileText
	if s.in.CFlags != nil {
		args := append([]string{"-E"}, s.in.CFlags...)
		cmd := exec.Command(s.in.CC, append(args, p)...)
		cmd.Dir = s.in.Root
		cmd.Stderr = io.Discard
		if out, err := cmd.Output(); err == nil {
			texts = mapLines(string(out), p)
		}
		if texts == nil {
			// v2 fell back silently, which hid a broken scan once: say so.
			fmt.Fprintf(os.Stderr, "[mbt] WARNING: module-data: cc370 -E failed for %s; scanning its raw text (host-only branches included)\n", p)
		}
	}
	if texts == nil {
		data, err := os.ReadFile(filepath.Join(s.in.Root, p))
		if err != nil {
			return nil
		}
		texts = []fileText{{p, strings.ToValidUTF8(string(data), "�")}}
	}
	var out []Finding
	for _, t := range texts {
		out = append(out, scanText(t.file, t.text)...)
	}
	return out
}

// Check returns errors and warnings, as v2's check().
func Check(in Input) (errs, warns []Report) {
	s := &scanner{in: in, cache: map[string][]Finding{}}
	var all []string
	for _, u := range in.Units {
		all = append(all, cSources(in.Root, u.Sources, u.Excludes)...)
	}
	if in.Internal != nil {
		all = append(all, cSources(in.Root, in.Internal.Sources, in.Internal.Excludes)...)
	}
	s.preprocess(all)

	for _, u := range in.Units {
		var why string
		fatal := false
		switch {
		case u.Rent != nil && !*u.Rent && !u.AC1:
			continue
		case u.Rent != nil && *u.Rent:
			why, fatal = "rent = true", true
		case u.AC1:
			why = "ac = 1: key-0 storage if fetched authorized"
		case u.Test:
			continue // tests are not LINKed concurrently; only declared ones
		default:
			why = "RENT by ld370's default"
		}
		seen := map[string]bool{}
		for _, p := range cSources(in.Root, u.Sources, u.Excludes) {
			for _, f := range s.cache[p] {
				k := fmt.Sprintf("%s:%d", f.File, f.Line)
				if seen[k] {
					continue // a header included by several sources
				}
				seen[k] = true
				r := Report{why, u.Name, f}
				if fatal {
					errs = append(errs, r)
				} else {
					warns = append(warns, r)
				}
			}
		}
	}
	// [internal] objects reach a module by autocall, only when referenced,
	// which this scan cannot see: report them whenever a module may be RENT
	// (attributing them through the load map is mbt#152).
	maybeRent := false
	for _, u := range in.Units {
		if !u.Test && (u.Rent == nil || *u.Rent) {
			maybeRent = true
		}
	}
	if in.Internal != nil && maybeRent {
		seen := map[string]bool{}
		for _, p := range cSources(in.Root, in.Internal.Sources, in.Internal.Excludes) {
			for _, f := range s.cache[p] {
				k := fmt.Sprintf("%s:%d", f.File, f.Line)
				if !seen[k] {
					seen[k] = true
					warns = append(warns, Report{"[internal], linked by autocall if referenced", "-", f})
				}
			}
		}
	}
	return errs, warns
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// Print writes the report as v2 did: warnings grouped per module, three per
// module unless all, errors in full.  It returns true when the build must
// stop.
func Print(w io.Writer, errs, warns []Report, all bool, hint string) bool {
	type key struct{ why, unit string }
	var order []key
	groups := map[key][]Report{}
	for _, r := range warns {
		k := key{r.Why, r.Unit}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	for _, k := range order {
		items := groups[k]
		shown := items
		if !all && len(shown) > 3 {
			shown = shown[:3]
		}
		for _, r := range shown {
			fmt.Fprintf(w, "[mbt] WARNING: writable data in %s (%s): %s:%d: %s\n", r.Unit, r.Why, r.File, r.Line, truncate(r.Head, 80))
		}
		if len(items) > len(shown) {
			fmt.Fprintf(w, "[mbt] WARNING:   ... %d more in %s; '%s' lists them\n", len(items)-len(shown), k.unit, hint)
		}
	}
	for _, r := range errs {
		fmt.Fprintf(w, "[mbt] ERROR: writable data in %s (%s): %s:%d: %s\n", r.Unit, r.Why, r.File, r.Line, truncate(r.Head, 80))
	}
	if len(errs) > 0 {
		fmt.Fprintln(w, "[mbt] A rent = true module is one copy shared by every task that LINKs it, so it may hold no writable data. Move it onto the heap or the stack, make it const, or declare rent = false (cc370#100).")
		return true
	}
	whys := map[string]bool{}
	for _, r := range warns {
		whys[r.Why] = true
	}
	if whys["RENT by ld370's default"] {
		fmt.Fprintln(w, "[mbt] A module that is RENT only by ld370's default: remove the data and declare rent = true, or declare rent = false (cc370#100).")
	}
	for k := range whys {
		if strings.HasPrefix(k, "ac = 1") {
			fmt.Fprintln(w, "[mbt] An ac = 1 module fetched authorized from an APF library is key-0 storage, and a store into it abends S0C4 -- fine only if it never runs that way.")
			break
		}
	}
	return false
}

// FromRaw reads the units the check looks at from a parsed v2 project.toml:
// every [[module]] and [[test]], a host-only one (mvs = false) included, as
// v2 did; rent from `rent`, or false from the legacy `norent = true`.
func FromRaw(root string, raw map[string]any) Input {
	in := Input{Root: root}
	for _, kind := range []string{"module", "test"} {
		for _, t := range rawTables(raw[kind]) {
			u := Unit{Test: kind == "test", Sources: rawStrs(t["sources"]), Excludes: rawStrs(t["exclude"])}
			u.Name, _ = t["name"].(string)
			if u.Name == "" {
				u.Name = "?"
			}
			if r, ok := t["rent"]; ok {
				b := truthy(r)
				u.Rent = &b
			} else if truthy(t["norent"]) {
				f := false
				u.Rent = &f
			}
			if ac, ok := t["ac"].(int64); ok && ac == 1 {
				u.AC1 = true
			}
			in.Units = append(in.Units, u)
		}
	}
	if it, ok := raw["internal"].(map[string]any); ok && len(it) > 0 {
		in.Internal = &Unit{Name: "-", Sources: rawStrs(it["sources"]), Excludes: rawStrs(it["exclude"])}
	}
	return in
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case string:
		return x != ""
	}
	return v != nil
}

func rawTables(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		var out []map[string]any
		for _, e := range x {
			if t, ok := e.(map[string]any); ok {
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

func rawStrs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
