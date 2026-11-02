package project

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// schemaSections maps each kind of v3Keys to the heading of the section of
// internals/mbt-3-schema.md that lists its keys.
var schemaSections = map[string]string{
	"project":      "## `[project]`",
	"toolchain":    "## `[toolchain]`",
	"build":        "## `[build]`",
	"build.host":   "### `[build.host]`",
	"lib":          "## `[lib]` and `[internal]`",
	"internal":     "## `[lib]` and `[internal]`",
	"module":       "## `[module.<NAME>]`",
	"tests":        "### `[tests]`",
	"test":         "### `[test.<NAME>]`",
	"deploy":       "## `[deploy]`",
	"distribution": "## `[distribution]`",
	"library":      "## `[distribution]`",
	"smp":          "## `[smp]`",
	"release":      "## `[release]`",
	"tso":          "## `[tso.<NAME>]`",
}

var headingRE = regexp.MustCompile(`(?m)^#{1,6} `)
var identRE = regexp.MustCompile(`[A-Za-z0-9_]+`)

// TestSchemaDocListsEveryKey fails when a key that v3Keys accepts is missing
// from the section of internals/mbt-3-schema.md that is meant to list it
// (mbt#352); the error text for an unknown key points readers to that file.
func TestSchemaDocListsEveryKey(t *testing.T) {
	b, err := os.ReadFile("../../internals/mbt-3-schema.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)

	sectionTokens := func(heading string) map[string]bool {
		start := strings.Index(doc, "\n"+heading)
		if start < 0 {
			t.Errorf("internals/mbt-3-schema.md has no section %q", heading)
			return nil
		}
		body := doc[start+len(heading)+1:]
		if loc := headingRE.FindStringIndex(body); loc != nil {
			body = body[:loc[0]]
		}
		tok := map[string]bool{}
		for _, span := range regexp.MustCompile("`[^`\n]+`").FindAllString(body, -1) {
			for _, w := range identRE.FindAllString(span, -1) {
				tok[w] = true
			}
		}
		return tok
	}

	for kind, keys := range v3Keys {
		if kind == "" {
			top := sectionTokens("## Top level")
			for _, k := range keys {
				if !top[k] && !regexp.MustCompile(`\[` + regexp.QuoteMeta(k) + `[\].]`).MatchString(doc) {
					t.Errorf("top-level table [%s] is not described in internals/mbt-3-schema.md", k)
				}
			}
			continue
		}
		heading, ok := schemaSections[kind]
		if !ok {
			t.Errorf("v3Keys has the kind %q: add its section to schemaSections", kind)
			continue
		}
		tok := sectionTokens(heading)
		for _, k := range keys {
			if tok != nil && !tok[k] {
				t.Errorf("key %q of [%s] is missing from %q in internals/mbt-3-schema.md", k, kind, heading)
			}
		}
	}
}
