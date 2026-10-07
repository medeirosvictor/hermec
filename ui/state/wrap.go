package state

import "strings"

// Wrap greedily wraps text to at most cols runes per line, hard-breaking
// words longer than cols. cols <= 0 returns the text unsplit.
func Wrap(text string, cols int) []string {
	if cols <= 0 {
		return []string{text}
	}
	var lines []string
	var cur []rune
	flush := func() {
		lines = append(lines, string(cur))
		cur = nil
	}
	for _, w := range strings.Fields(text) {
		word := []rune(w)
		for len(word) > cols {
			if len(cur) > 0 {
				flush()
			}
			lines = append(lines, string(word[:cols]))
			word = word[cols:]
		}
		switch {
		case len(cur) == 0:
			cur = append(cur, word...)
		case len(cur)+1+len(word) <= cols:
			cur = append(cur, ' ')
			cur = append(cur, word...)
		default:
			flush()
			cur = append(cur, word...)
		}
	}
	if len(cur) > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}
