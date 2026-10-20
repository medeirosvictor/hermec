// Package names holds the display-name rules shared by the server and the UI.
package names

import (
	"strings"
	"unicode"
)

// Clean strips control, format (bidi overrides and the like) and line or
// paragraph separator runes from s, trims spaces and caps the result at max
// runes.
func Clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return -1
		}
		return r
	}, s)
	r := []rune(strings.TrimSpace(s))
	if len(r) > max {
		r = r[:max]
	}
	return string(r)
}
