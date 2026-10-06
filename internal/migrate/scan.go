// Package migrate rewrites an mbt v2 project.toml into an mbt.toml, schema 3
// (internals/mbt-3-schema.md), keeping the comments.
//
// The file is read twice: decoded, for the values, and scanned line by line,
// for the text.  Every key keeps the comment lines above it and its inline
// comment, and a value that carries over unchanged is copied verbatim --
// a multi-line source list with comments inside it survives as written.
package migrate

import (
	"regexp"
	"strings"
)

// entry is one key = value, with the lines that belong to it.
type entry struct {
	Lead  []string // comment and blank lines above it
	Key   string   // the key as written (possibly quoted or dotted)
	Lines []string // key = value, through the end of the value
}

// block is a table: a header and its entries.
type block struct {
	Lead    []string
	Header  string // the header line as written, "" for the top level
	Path    string // "project", "module", "test.fixture", ...
	Array   bool   // [[...]]
	Entries []*entry
}

type scanned struct {
	Blocks  []*block
	Trailer []string // comment lines after the last entry
}

var headerRE = regexp.MustCompile(`^\s*(\[\[?)\s*([^\]]+?)\s*(\]\]?)\s*(#.*)?$`)
var keyRE = regexp.MustCompile(`^\s*("[^"]*"|'[^']*'|[A-Za-z0-9_.\-"]+)\s*=`)

func scan(text string) scanned {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var s scanned
	cur := &block{}
	s.Blocks = append(s.Blocks, cur)
	var pending []string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			pending = append(pending, l)
			continue
		}
		if m := headerRE.FindStringSubmatch(l); m != nil && !keyRE.MatchString(l) {
			cur = &block{Lead: pending, Header: l, Path: unquotePath(m[2]), Array: m[1] == "[["}
			s.Blocks = append(s.Blocks, cur)
			pending = nil
			continue
		}
		m := keyRE.FindStringSubmatch(l)
		if m == nil {
			// not something a valid TOML file has here; keep it with the table
			pending = append(pending, l)
			continue
		}
		e := &entry{Lead: pending, Key: m[1]}
		pending = nil
		depth := 0
		j := i
		for {
			depth += bracketDelta(valuePart(lines[j], j == i))
			e.Lines = append(e.Lines, lines[j])
			if depth <= 0 || j == len(lines)-1 {
				break
			}
			j++
		}
		i = j
		cur.Entries = append(cur.Entries, e)
	}
	s.Trailer = pending
	return s
}

func unquotePath(p string) string {
	parts := strings.Split(p, ".")
	for i, q := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(q), `"'`)
	}
	return strings.Join(parts, ".")
}

// valuePart is the text after "key =" on a value's first line.
func valuePart(l string, first bool) string {
	if !first {
		return l
	}
	if i := strings.Index(l, "="); i >= 0 {
		return l[i+1:]
	}
	return l
}

// bracketDelta counts [ { against ] } outside strings and comments.
func bracketDelta(s string) int {
	d := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' && q == '"' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#':
			return d
		case c == '[' || c == '{':
			d++
		case c == ']' || c == '}':
			d--
		}
	}
	return d
}

// inlineComment returns the "# ..." at the end of a line, outside strings.
func inlineComment(l string) string {
	var q byte
	for i := 0; i < len(l); i++ {
		c := l[i]
		switch {
		case q != 0:
			if c == '\\' && q == '"' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#':
			return l[i:]
		}
	}
	return ""
}

// rekey replaces the key on an entry's first line, keeping the rest.
func rekey(e *entry, key string) []string {
	out := append([]string{}, e.Lines...)
	l := out[0]
	i := strings.Index(l, e.Key)
	out[0] = l[:i] + key + l[i+len(e.Key):]
	return out
}

func hasComment(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			return true
		}
	}
	return false
}
