package state

import "strings"

// InputBuffer is a rune-based line editor.
type InputBuffer struct {
	runes []rune
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
	b.runes = nil
	return out
}

func (b *InputBuffer) String() string { return string(b.runes) }
