package ui

import (
	"runtime"
	"strings"
	"unicode"

	"github.com/atotto/clipboard"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// clipboardRead is the paste source; tests replace it.
var clipboardRead = clipboard.ReadAll

// editKeys are the editing keys that fired this tick (repeat already applied).
type editKeys struct {
	left, right, home, end, del, backspace, paste bool
}

// textField is the pure editing core shared by every text field: a rune
// buffer with a caret, a length cap, and an optional per-rune filter.
type textField struct {
	text   []rune
	caret  int
	max    int
	accept func(rune) bool // nil accepts every printable rune
	// wsPrefix marks a server-address field: pasting an address that carries its
	// own ws:// or wss:// replaces a bare scheme instead of doubling it.
	wsPrefix bool
}

// printable reports whether r may be typed or pasted.
func printable(r rune) bool { return r >= ' ' && r != 0x7f && !unicode.IsControl(r) }

func (t *textField) ok(r rune) bool {
	return printable(r) && (t.accept == nil || t.accept(r))
}

func (t *textField) clamp() {
	if t.caret < 0 {
		t.caret = 0
	}
	if t.caret > len(t.text) {
		t.caret = len(t.text)
	}
}

func (t *textField) insert(rs []rune) bool {
	t.clamp()
	room := t.max - len(t.text)
	var in []rune
	for _, r := range rs {
		if len(in) >= room {
			break
		}
		if t.ok(r) {
			in = append(in, r)
		}
	}
	if len(in) == 0 {
		return false
	}
	out := make([]rune, 0, len(t.text)+len(in))
	out = append(out, t.text[:t.caret]...)
	out = append(out, in...)
	out = append(out, t.text[t.caret:]...)
	t.text = out
	t.caret += len(in)
	return true
}

func isBareScheme(r []rune) bool {
	s := string(r)
	return s == "ws://" || s == "wss://"
}

func hasScheme(s string) bool {
	return strings.HasPrefix(s, "ws://") || strings.HasPrefix(s, "wss://")
}

// update applies one tick of input and reports whether the text changed.
// Keys run first, then typed characters; on a paste tick the characters are
// dropped (a Ctrl+V can deliver a stray "v" or control rune) and the clipboard
// text goes in instead, newlines and control characters stripped.
func (t *textField) update(k editKeys, chars []rune, paste func() (string, error)) bool {
	t.clamp()
	changed := false
	switch {
	case k.home:
		t.caret = 0
	case k.end:
		t.caret = len(t.text)
	}
	if k.left && t.caret > 0 {
		t.caret--
	}
	if k.right && t.caret < len(t.text) {
		t.caret++
	}
	if k.backspace && t.caret > 0 {
		t.text = append(t.text[:t.caret-1], t.text[t.caret:]...)
		t.caret--
		changed = true
	}
	if k.del && t.caret < len(t.text) {
		t.text = append(t.text[:t.caret], t.text[t.caret+1:]...)
		changed = true
	}
	if k.paste {
		if paste != nil {
			if s, err := paste(); err == nil && t.pasteText(s) {
				changed = true
			}
		}
		return changed
	}
	if t.insert(chars) {
		changed = true
	}
	return changed
}

// pasteNewlines maps line breaks and tabs to spaces, so pasted lines stay
// apart; "\r\n" is listed first so it becomes one space, not two.
var pasteNewlines = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

// flattenPaste turns line breaks and tabs into single spaces: a run of them
// (or of spaces already there) collapses to one.
func flattenPaste(s string) string {
	s = pasteNewlines.Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (t *textField) pasteText(s string) bool {
	s = flattenPaste(s)
	// Bound the work on a huge clipboard: nothing past the cap can land.
	if lim := 4*t.max + 64; len(s) > lim {
		s = s[:lim]
	}
	s = strings.TrimSpace(s)
	if t.wsPrefix && hasScheme(s) && isBareScheme(t.text) {
		t.text, t.caret = nil, 0
	}
	return t.insert([]rune(s))
}

// pasteShortcut reports whether the pressed modifiers mean paste on goos:
// Cmd on macOS, Ctrl elsewhere.
func pasteShortcut(goos string, ctrl, meta bool) bool {
	if goos == "darwin" {
		return meta
	}
	return ctrl
}

// readEditKeys samples the editing keys, with the usual hold-to-repeat.
func readEditKeys() editKeys {
	rep := func(k ebiten.Key) bool {
		if inpututil.IsKeyJustPressed(k) {
			return true
		}
		d := inpututil.KeyPressDuration(k)
		return d > 30 && d%3 == 0
	}
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
	return editKeys{
		left:      rep(ebiten.KeyArrowLeft),
		right:     rep(ebiten.KeyArrowRight),
		home:      inpututil.IsKeyJustPressed(ebiten.KeyHome),
		end:       inpututil.IsKeyJustPressed(ebiten.KeyEnd),
		del:       rep(ebiten.KeyDelete),
		backspace: rep(ebiten.KeyBackspace),
		paste:     inpututil.IsKeyJustPressed(ebiten.KeyV) && pasteShortcut(runtime.GOOS, ctrl, meta),
	}
}

// edit applies one tick of input to f under the given cap and filter. The
// caret is stored as a distance from the end, so code that replaces f.runes
// outright leaves the caret at the end.
func (f *field) edit(k editKeys, chars []rune, max int, accept func(rune) bool, ws bool) bool {
	t := textField{text: f.runes, caret: len(f.runes) - f.back, max: max, accept: accept, wsPrefix: ws}
	changed := t.update(k, chars, clipboardRead)
	f.runes = t.text
	f.back = len(t.text) - t.caret
	return changed
}

// setText replaces the text and puts the caret at the end.
func (f *field) setText(s string) { f.runes, f.back = []rune(s), 0 }

// caret is the caret position in runes.
func (f *field) caret() int {
	c := len(f.runes) - f.back
	if c < 0 {
		return 0
	}
	return c
}

// window picks the slice of text to show in room columns so the caret stays
// visible; it returns the start offset.
func window(n, caret, room int) int {
	if room < 1 {
		room = 1
	}
	if caret > room {
		return caret - room
	}
	return 0
}

// drawCaret draws the blinking caret at x, where prefix is the text before it.
func (g *game) drawCaret(dst *ebiten.Image, x, y float64, prefix string) {
	if (g.frame/30)%2 != 0 {
		return
	}
	cx := x + text.Advance(prefix, g.face)
	vector.FillRect(dst, float32(cx), float32(y+2), 2, float32(g.fontSize()), g.th.Bright, false)
}
