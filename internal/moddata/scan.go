// Package moddata finds writable data in modules that promise to have none.
//
// cc370 keeps a module's writable data in the module itself: a C static or a
// non-const global becomes storage in the CSECT.  A rent = true module is one
// copy for every task that LINKs it, so such data is shared, unsynchronised
// state (cc370#100); an ac = 1 module fetched authorized lands in key-0
// storage and abends S0C4 on the first store.  rent = true with writable data
// stops the build; a module that is RENT only by ld370's default, or ac = 1,
// is warned about.
//
// The scanner is mbt v2's (scripts/mbtmoddata.py), ported line for line so
// both report the same findings: a text proxy over the preprocessed source,
// not the proof (that would be cc370 -S and a store through =A(@Vn)).
package moddata

import (
	"regexp"
	"strings"
)

// stripNoise blanks comments, string/char literals and preprocessor lines,
// keeping every newline so line numbers survive.
func stripNoise(src []rune) []rune {
	var out []rune
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		if c == '/' && i+1 < n && src[i+1] == '*' {
			j := indexFrom(src, []rune("*/"), i+2)
			if j < 0 {
				j = n
			} else {
				j += 2
			}
			for _, ch := range src[i:j] {
				if ch == '\n' {
					out = append(out, '\n')
				} else {
					out = append(out, ' ')
				}
			}
			i = j
			continue
		}
		if c == '/' && i+1 < n && src[i+1] == '/' {
			j := indexFrom(src, []rune("\n"), i)
			if j < 0 {
				j = n
			}
			for k := i; k < j; k++ {
				out = append(out, ' ')
			}
			i = j
			continue
		}
		if c == '"' || c == '\'' {
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' {
					j += 2
				} else {
					j++
				}
			}
			out = append(out, '"', '"')
			for k := 0; k < j-i-1; k++ {
				out = append(out, ' ')
			}
			i = j + 1
			continue
		}
		out = append(out, c)
		i++
	}
	lines := strings.Split(string(out), "\n")
	for k, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t\r\f\v"), "#") {
			lines[k] = ""
		}
	}
	return []rune(strings.Join(lines, "\n"))
}

func indexFrom(s, sub []rune, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		match := true
		for k := range sub {
			if s[i+k] != sub[k] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

type decl struct {
	line  int
	head  string
	depth int
}

var (
	typedefRE = regexp.MustCompile(`\btypedef\b`)
	typeHead  = regexp.MustCompile(`\b(struct|union|enum)(\s+\w+)?\s*$`)
)

func words(s string) string { return strings.Join(strings.Fields(s), " ") }

// declarations yields every statement, at file scope and inside function
// bodies alike: a function-local static is module storage too.  A '{' after
// '=' or ',' opens an initializer, not a block.  The declarator after a
// struct/union/enum body inherits the words before the body, so
// `static const struct { ... } tbl[] = {...};` reads as const; a typedef's
// tail is a type name and dropped.
func declarations(src []rune) []decl {
	var out []decl
	depth, init, line, start := 0, 0, 1, 1
	var buf strings.Builder
	var heads []string
	typetail, tailhead := false, ""
	for _, ch := range src {
		if ch == '\n' {
			line++
		}
		switch {
		case ch == '{':
			b := strings.TrimRight(buf.String(), " \t\n\r\f\v")
			if init > 0 || strings.HasSuffix(b, "=") || strings.HasSuffix(b, ",") {
				init++
				buf.WriteRune(' ')
				continue
			}
			depth++
			heads = append(heads, words(buf.String()))
			buf.Reset()
			start = line
			continue
		case ch == '}':
			if init > 0 {
				init--
				buf.WriteRune(' ')
				continue
			}
			if depth--; depth < 0 {
				depth = 0
			}
			popped := ""
			if len(heads) > 0 {
				popped, heads = heads[len(heads)-1], heads[:len(heads)-1]
			}
			typetail = typedefRE.MatchString(popped)
			tailhead = ""
			if typeHead.MatchString(popped) {
				tailhead = popped
			}
			buf.Reset()
			start = line
			continue
		case ch == ';' && init == 0:
			head := words(buf.String())
			if head != "" && !typetail {
				out = append(out, decl{start, strings.TrimSpace(tailhead + " " + head), depth})
			}
			typetail, tailhead = false, ""
			buf.Reset()
			start = line
			continue
		}
		if strings.TrimSpace(buf.String()) == "" {
			start = line
		}
		buf.WriteRune(ch)
	}
	return out
}

var (
	// A head ending in ')' is a function, unless an initializer `= f(1)`
	// ends it; the parameter list may nest.
	isFunc = regexp.MustCompile(`^[^=]*\)\s*$`)
	// Only the first parenthesis decides: `int (*fp)(int)` is a pointer.
	isFuncPtr = regexp.MustCompile(`^[^(]*\(\s*\*`)
	skippable = regexp.MustCompile(`\b(typedef|extern)\b`)
	isConst   = regexp.MustCompile(`\bconst\b`)
	isTagOnly = regexp.MustCompile(`^(struct|union|enum)\s+\w+$`)
	attrRE    = regexp.MustCompile(`\b__attribute__\s*\(`)
	wordRE    = regexp.MustCompile(`\w`)
	exempt    = regexp.MustCompile(`\b__stklen\b`)
)

// stripAttributes drops GNU __attribute__((...)), whose parentheses nest.
func stripAttributes(head string) string {
	var out strings.Builder
	h := []rune(head)
	i := 0
	for {
		loc := attrRE.FindStringIndex(string(h[i:]))
		if loc == nil {
			out.WriteString(string(h[i:]))
			return words(out.String())
		}
		mStart := len([]rune(string(h[i:])[:loc[0]]))
		mEnd := len([]rune(string(h[i:])[:loc[1]]))
		out.WriteString(string(h[i : i+mStart]))
		j, level := i+mEnd, 1
		for j < len(h) && level > 0 {
			switch h[j] {
			case '(':
				level++
			case ')':
				level--
			}
			j++
		}
		i = j
	}
}

func mutable(head string, depth int) bool {
	head = stripAttributes(head)
	if skippable.MatchString(head) || isConst.MatchString(head) {
		return false
	}
	if depth > 0 && !strings.HasPrefix(head, "static") {
		return false // an ordinary local lives on the stack
	}
	if isTagOnly.MatchString(head) {
		return false
	}
	if isFunc.MatchString(head) && !isFuncPtr.MatchString(head) {
		return false // prototype or definition head
	}
	return wordRE.MatchString(head)
}

// Finding is one writable definition.
type Finding struct {
	File string
	Line int
	Head string
}

// scanText returns the writable definitions in one file's text.
func scanText(file, text string) []Finding {
	var out []Finding
	for _, d := range declarations(stripNoise([]rune(text))) {
		if mutable(d.head, d.depth) && !exempt.MatchString(d.head) {
			out = append(out, Finding{file, d.line, d.head})
		}
	}
	return out
}
