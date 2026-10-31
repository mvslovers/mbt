package dist

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// smpDDNames are the ddnames the SMPAPP and SMPREC procedures of MVS/CE and
// TK5 define, with the ones SMP itself reads. A TXLIB card under one of these
// would be a second DD of the same name in the step and SMP would use the
// procedure's (the same silent failure the DD order below guards against), so
// a member called like one gets a ddname of its own.
var smpDDNames = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`
		ACMDLIB AGENLIB AHELP AIMAGE ALPALIB AMACLIB AMODGEN AOS00 AOS03 AOS04
		AOS05 AOS06 AOS07 AOS11 AOS12 AOS20 AOS21 AOS24 AOS26 AOS29 AOS32 AOSA0
		AOSA1 AOSB0 AOSB3 AOSBN AOSC2 AOSC5 AOSC6 AOSCA AOSCD AOSCE AOSD0 AOSD7
		AOSD8 AOSG0 AOSH1 AOSH3 AOST3 AOST4 AOSU0 APARMLIB APROCLIB ASAMPLIB
		ASMPRINT ATCAMMAC ATSOMAC AUADS CMDLIB CMPPRINT COPPRINT E37PRINT HASPSRC
		HELP IMAGE IMAGELIB LINKLIB LKDPRINT LPALIB MACLIB NUCLEUS PARMLIB PROCLIB
		SAMPLIB SMPACDS SMPACRQ SMPCDS SMPCNTL SMPCRQ SMPLOG SMPMTS SMPOUT SMPPTFIN
		SMPPTS SMPSCDS SMPSTS SMPTLIB SMPWRK1 SMPWRK2 SMPWRK3 SMPWRK4 SMPWRK5
		SVCLIB SYSLIB SYSPRINT SYSUT1 SYSUT2 SYSUT3 SYSUT4 TCOMMAC TELCMLIB UADS
		UMODLIB UMODOBJ UPDPRINT VTAMLIB ZAPPRINT`) {
		m[n] = true
	}
	return m
}()

func reservedDD(name string) bool {
	return smpDDNames[name] || strings.HasPrefix(name, "SMP") || strings.HasPrefix(name, "SYS")
}

// assignDDs names the TXLIB card of every element: the member's own name,
// which keeps the job readable against the member list, unless that is taken
// by the procedures, by one of our libraries, or by an element of the other
// type with the same name; then TX000001, TX000002, ... in order.
func assignDDs(d *Distribution) {
	used := map[string]bool{ddnameOf(d.SMP.LKLib): true}
	for _, dsn := range d.allocated() {
		used[ddnameOf(dsn)] = true
	}
	for i := range d.Libraries {
		l := &d.Libraries[i]
		for j := range l.Elements {
			e := &l.Elements[j]
			e.DD = ""
			if !reservedDD(e.Name) && !used[e.Name] {
				e.DD = e.Name
				used[e.Name] = true
			}
		}
	}
	next := 1
	for i := range d.Libraries {
		l := &d.Libraries[i]
		for j := range l.Elements {
			e := &l.Elements[j]
			if e.DD != "" {
				continue
			}
			for {
				c := fmt.Sprintf("TX%06d", next)
				next++
				if !used[c] {
					e.DD = c
					used[c] = true
					break
				}
			}
		}
	}
}

// checkElementNames: an element name is one of its type in the whole SYSMOD,
// so two libraries delivering the same type may not share a member name.
func checkElementNames(d *Distribution) error {
	owner := map[string]string{}
	for _, l := range d.delivered() {
		for _, e := range l.Elements {
			k := l.Element + " " + e.Name
			if prev, ok := owner[k]; ok {
				return errf("%s %s is a member of both %s and %s. An SMP element has one name for the whole SYSMOD -- rename one of the files.", l.Element, e.Name, prev, l.Dir)
			}
			owner[k] = l.Dir
		}
	}
	return nil
}

// txCard is one TXLIB DD card: the member of the staged library the element
// is read from. A long dataset name goes on a continuation card.
func txCard(step, dd, stage, member string) string {
	card := fmt.Sprintf("//%s%s DD  DISP=SHR,DSN=%s(%s)", step, ljust(dd, 8), stage, member)
	if len(card) <= maxCardCol {
		return card
	}
	return fmt.Sprintf("//%s%s DD  DISP=SHR,\n//             DSN=%s(%s)", step, ljust(dd, 8), stage, member)
}

// txCards are the TXLIB cards of every element, the same in the RECEIVE,
// APPLY CHECK, APPLY and ACCEPT steps: each one reads the element from the
// staged library.
func txCards(d *Distribution, step string) []string {
	var out []string
	for _, l := range d.delivered() {
		for _, e := range l.Elements {
			out = append(out, txCard(step, e.DD, l.Stage, e.Name))
		}
	}
	return out
}

func renderRecvDDs(d *Distribution) string {
	cards := txCards(d, "")
	if len(cards) == 0 {
		return ""
	}
	return "\n" + strings.Join(cards, "\n")
}

// elementStatements are the ++MAC/++SRC cards of the SYSMOD.
func elementStatements(d *Distribution) []string {
	var out []string
	for _, l := range d.delivered() {
		for _, e := range l.Elements {
			out = append(out, fmt.Sprintf("++%s(%s) DISTLIB(%s) SYSLIB(%s) TXLIB(%s) .",
				l.Element, e.Name, ddnameOf(l.DistLib), ddnameOf(l.Target), e.DD))
		}
	}
	return out
}

// wrapNames lays names out as head + "a,b,c)", continued below column 71.
func wrapNames(head string, names []string) []string {
	var lines []string
	cur := head
	for i, m := range names {
		piece := m + ","
		if i == len(names)-1 {
			piece = m + ")"
		}
		if len(cur)+len(piece) > maxCardCol-1 {
			lines = append(lines, cur)
			cur = strings.Repeat(" ", len(head)) + piece
		} else {
			cur += piece
		}
	}
	return append(lines, cur)
}

// ElementReport is what the maintainer needs before the first install: the
// names, and the SMP statements that find one already known to the target
// system. A MAC known to its CDS shows as NOT SEL in the APPLY, at return code
// 0; a SRC with the name of a load module in the CDS is worse: SMP takes it
// for that module's source, assembles it and replaces the module (or ends the
// APPLY at return code 12 when it cannot).
func ElementReport(d *Distribution) []string {
	var out []string
	for _, l := range d.delivered() {
		names := make([]string, len(l.Elements))
		for i, e := range l.Elements {
			names[i] = e.Name
		}
		out = append(out, fmt.Sprintf("%d %s elements for %s (library %s) -- before the first install, run this on the target system:",
			len(names), l.Element, l.Target, l.Dir))
		if l.Element == "SRC" {
			out = append(out, "  a name already in its CDS as a SRC shows as NOT SEL in the APPLY, at return code 0;",
				"  a name that is a load module (MOD) there makes SMP assemble the member into that module:")
			out = append(out, wrapNames("    LIST CDS MOD(", names)...)
			out[len(out)-1] += " ."
			out = append(out, wrapNames("    LIST CDS SRC(", names)...)
		} else {
			out = append(out, "  a name already in its CDS shows as NOT SEL in the APPLY, at return code 0:")
			out = append(out, wrapNames(fmt.Sprintf("    LIST CDS %s(", l.Element), names)...)
		}
		out[len(out)-1] += " ."
	}
	return out
}

// ---- checking the members -------------------------------------------------

const tabWidth = 8

// checkMember validates one member of an element library the way xmit370
// will convert it, and returns the bytes to give xmit370 and whether they
// hold characters above 0x7F (xmit370 then needs --latin1).
//
// A card image is 80 columns and SMP takes all of them, so nothing is cut,
// renumbered or stripped here; what cannot be carried is refused with the
// place: a line past column 80 once its tabs are expanded and its trailing
// blanks dropped (xmit370 does both), a control character, and a character
// CP037 has no equivalent for. A file that is valid UTF-8 is read as UTF-8
// and a character up to U+00FF is handed on as the Latin-1 byte xmit370
// maps through CP037; any other file is read as Latin-1, as xmit370 does.
func checkMember(file, text string) (string, bool, []string) {
	var problems []string
	complain := func(line, col int, format string, a ...any) {
		problems = append(problems, fmt.Sprintf("%s:%d:%d: %s", file, line, col, fmt.Sprintf(format, a...)))
	}
	asUTF8 := utf8.ValidString(text)
	var out strings.Builder
	latin1 := false
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for n, line := range lines {
		var chars []rune
		if asUTF8 {
			chars = []rune(line)
		} else {
			for i := 0; i < len(line); i++ {
				chars = append(chars, rune(line[i]))
			}
		}
		col, width := 0, 0
		var buf strings.Builder
		bad := false
		for _, c := range chars {
			switch {
			case c == '\t':
				col = (col/tabWidth + 1) * tabWidth
				buf.WriteByte('\t')
				continue
			case c < 0x20 || c == 0x7F || (c >= 0x80 && c < 0xA0):
				complain(n+1, col+1, "control character U+%04X in a text member", c)
				bad = true
			case c > 0xFF:
				complain(n+1, col+1, "'%c' (U+%04X) has no CP037 equivalent -- use plain ASCII here", c, c)
				bad = true
			case c >= 0xA0:
				latin1 = true
				buf.WriteByte(byte(c))
			default:
				buf.WriteByte(byte(c))
			}
			col++
			if c != ' ' {
				width = col
			}
		}
		if width > elemLrecl && !bad {
			complain(n+1, elemLrecl+1, "line is %d columns, exceeds the %d of a card image", width, elemLrecl)
		}
		out.WriteString(buf.String())
		out.WriteByte('\n')
	}
	return out.String(), latin1, problems
}

// capProblems keeps a long list readable.
func capProblems(problems []string, limit int) string {
	if len(problems) > limit {
		extra := len(problems) - limit
		problems = append(problems[:limit:limit], fmt.Sprintf("... and %d more", extra))
	}
	return strings.Join(problems, "\n    ")
}

func sortedElements(names []string) []Element {
	sort.Strings(names)
	out := make([]Element, len(names))
	for i, n := range names {
		out[i] = Element{Name: n}
	}
	return out
}

// commentBlock lays text out as //* comment lines below column 71.
func commentBlock(text string) string {
	var lines []string
	cur := "//*"
	for _, w := range strings.Fields(text) {
		if len(cur)+1+len(w) > maxCardCol-1 && cur != "//*" {
			lines = append(lines, cur)
			cur = "//*"
		}
		cur += " " + w
	}
	return strings.Join(append(lines, cur), "\n")
}

// elementNote is the install job's paragraph on what SMP installs and what
// stays outside it.
func elementNote(d *Distribution) string {
	var plain, smp []string
	for _, l := range d.Libraries {
		if l.delivers() {
			smp = append(smp, fmt.Sprintf("%s (%d %s)", l.Target, len(l.Elements), l.Element))
		} else {
			plain = append(plain, l.Target)
		}
	}
	text := "SMP installs the load modules and, element by element, the members of " +
		strings.Join(smp, ", ") + ". Each of those libraries is restored into a staging " +
		"library first, and every member is read from there through a TXLIB card of its own."
	if len(plain) > 0 {
		text += " " + strings.Join(plain, ", ") + " is a plain PDS restored by TSO RECEIVE " +
			"and none of SMP's business: the product owns those patterns, the site owns " +
			"the copies it makes of them."
	}
	return commentBlock(text)
}
