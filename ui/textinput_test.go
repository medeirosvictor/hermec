package ui

import (
	"errors"
	"strings"
	"testing"
)

func fakeClip(s string) func() (string, error) {
	return func() (string, error) { return s, nil }
}

func TestPasteStripsNewlinesAndCaps(t *testing.T) {
	f := textField{text: []rune("ab"), caret: 1, max: 6}
	if !f.update(editKeys{paste: true}, []rune("v"), fakeClip("x\r\ny\tz\u0000\u007fQQQQ")) {
		t.Fatal("paste reported no change")
	}
	if got := string(f.text); got != "axyzQb" { // room is 4: x y z Q
		t.Fatalf("text = %q", got)
	}
	if len(f.text) != 6 || f.caret != 5 {
		t.Fatalf("len %d caret %d", len(f.text), f.caret)
	}
}

func TestPasteHugeClipboardCapped(t *testing.T) {
	f := textField{max: 128}
	f.update(editKeys{paste: true}, nil, fakeClip(strings.Repeat("é", 5_000_000)))
	if len(f.text) != 128 || f.caret != 128 {
		t.Fatalf("len %d caret %d", len(f.text), f.caret)
	}
}

func TestPasteErrorAndStrayChar(t *testing.T) {
	f := textField{text: []rune("a"), caret: 1, max: 9}
	f.update(editKeys{paste: true}, []rune("v"), func() (string, error) { return "", errors.New("no clipboard") })
	if string(f.text) != "a" {
		t.Fatalf("text = %q (stray v typed?)", string(f.text))
	}
}

func TestCaretMovesAndEdits(t *testing.T) {
	f := textField{text: []rune("abcd"), caret: 4, max: 10}
	f.update(editKeys{left: true}, nil, nil)
	f.update(editKeys{left: true}, nil, nil) // ab|cd
	f.update(editKeys{}, []rune("X"), nil)   // abX|cd
	if string(f.text) != "abXcd" || f.caret != 3 {
		t.Fatalf("%q %d", string(f.text), f.caret)
	}
	f.update(editKeys{backspace: true}, nil, nil) // ab|cd
	f.update(editKeys{del: true}, nil, nil)       // ab|d
	if string(f.text) != "abd" || f.caret != 2 {
		t.Fatalf("%q %d", string(f.text), f.caret)
	}
	f.update(editKeys{home: true}, nil, nil)
	f.update(editKeys{backspace: true}, nil, nil) // nothing before the caret
	if string(f.text) != "abd" || f.caret != 0 {
		t.Fatalf("%q %d", string(f.text), f.caret)
	}
	f.update(editKeys{end: true}, nil, nil)
	f.update(editKeys{del: true, right: true}, nil, nil)
	if f.caret != 3 || string(f.text) != "abd" {
		t.Fatalf("%q %d", string(f.text), f.caret)
	}
}

func TestJoinPasteReplacesWSPrefix(t *testing.T) {
	for _, v := range []string{"ws://100.1.2.3:7697/", "wss://h.example/"} {
		f := field{runes: []rune("ws://")}
		clipboardRead = fakeClip(v)
		f.edit(editKeys{paste: true}, nil, 128, nil, true)
		if f.String() != v {
			t.Fatalf("got %q, want %q", f.String(), v)
		}
	}
	clipboardRead = fakeClip("100.1.2.3:7697")
	f := field{runes: []rune("ws://")}
	f.edit(editKeys{paste: true}, nil, 128, nil, true)
	if f.String() != "ws://100.1.2.3:7697" {
		t.Fatalf("bare host: %q", f.String())
	}
}

func TestPasteShortcutDarwinUsesMeta(t *testing.T) {
	if !pasteShortcut("darwin", false, true) || pasteShortcut("darwin", true, false) {
		t.Error("darwin must use Cmd only")
	}
	if !pasteShortcut("windows", true, false) || pasteShortcut("windows", false, true) {
		t.Error("windows must use Ctrl only")
	}
	if !pasteShortcut("linux", true, false) {
		t.Error("linux must use Ctrl")
	}
}

func TestPortAcceptsDigitsOnlyOnPaste(t *testing.T) {
	f := textField{max: 5, accept: func(r rune) bool { return r >= '0' && r <= '9' }}
	f.update(editKeys{paste: true}, nil, fakeClip("76a97x99"))
	if string(f.text) != "76979" {
		t.Fatalf("got %q", string(f.text))
	}
}

func TestFieldCaretFollowsReplacedText(t *testing.T) {
	f := field{runes: []rune("abc")}
	f.edit(editKeys{left: true}, nil, 10, nil, false)
	f.setText("wxyz")
	f.edit(editKeys{}, []rune("!"), 10, nil, false)
	if f.String() != "wxyz!" {
		t.Fatalf("got %q", f.String())
	}
}
