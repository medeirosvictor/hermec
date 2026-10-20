package state

import "strings"

// InputBuffer is a rune-based line editor.
type InputBuffer struct {
	runes []rune
	back  int // caret distance from the end; 0 = at the end
}

// Runes returns the text (do not modify) and the caret position in runes.
func (b *InputBuffer) Runes() ([]rune, int) {
	c := len(b.runes) - b.back
	if c < 0 {
		c = 0
	}
	return b.runes, c
}

// Set replaces the text and caret (clamped).
func (b *InputBuffer) Set(text []rune, caret int) {
	b.runes = text
	caret = max(0, min(caret, len(text)))
	b.back = len(text) - caret
}

func (b *InputBuffer) AppendRunes(r []rune) { b.runes = append(b.runes, r...) }

func (b *InputBuffer) Backspace() {
	if len(b.runes) > 0 {
		b.runes = b.runes[:len(b.runes)-1]
	}
}

// Submit returns the trimmed text and clears the buffer.
func (b *InputBuffer) Submit() string {
	out := strings.TrimSpace(string(b.runes))
	b.runes, b.back = nil, 0
	return out
}

func (b *InputBuffer) String() string { return string(b.runes) }
