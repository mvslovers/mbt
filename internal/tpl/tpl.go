// Package tpl substitutes $NAME / ${NAME} placeholders exactly as Python's
// string.Template.safe_substitute does, which mbt 2 renders its JCL with:
// "$$" is a literal '$', an unknown name is left as it is, and a '$' that
// starts no placeholder stays.
package tpl

import "regexp"

var pattern = regexp.MustCompile(`(?i)\$(?:(\$)|([_a-z][_a-z0-9]*)|\{([_a-z][_a-z0-9]*)\}|())`)

// SafeSubstitute renders text with vars.
func SafeSubstitute(text string, vars map[string]string) string {
	return pattern.ReplaceAllStringFunc(text, func(m string) string {
		sub := pattern.FindStringSubmatch(m)
		switch {
		case sub[1] != "":
			return "$"
		case sub[2] != "":
			if v, ok := vars[sub[2]]; ok {
				return v
			}
		case sub[3] != "":
			if v, ok := vars[sub[3]]; ok {
				return v
			}
		}
		return m
	})
}
