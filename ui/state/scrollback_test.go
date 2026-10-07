package state

import (
	"testing"
	"time"
)

func TestStamp(t *testing.T) {
	if got := Stamp(time.Date(2026, 1, 2, 9, 5, 59, 0, time.Local)); got != "09:05" {
		t.Fatalf("got %q", got)
	}
}

func TestClampScroll(t *testing.T) {
	cases := []struct{ total, visible, off, want int }{
		{100, 10, 5, 5},
		{100, 10, 95, 90},
		{100, 10, -3, 0},
		{5, 10, 3, 0},
		{0, 0, 7, 0},
		{10, 10, 1, 0},
	}
	for _, c := range cases {
		if got := ClampScroll(c.total, c.visible, c.off); got != c.want {
			t.Errorf("ClampScroll(%d,%d,%d)=%d want %d", c.total, c.visible, c.off, got, c.want)
		}
	}
}

func TestScrollbackWrapsWithPrefix(t *testing.T) {
	ts := time.Date(2026, 1, 2, 9, 5, 0, 0, time.Local)
	msgs := []Message{{FromName: "ana", Text: "aaa bbb ccc ddd", TS: ts}}
	// prefix "[09:05] ana: " is 13 cols; cols=21 leaves 8 for text.
	lines := Scrollback(msgs, 21)
	if len(lines) != 2 || lines[0].Stamp != "[09:05] " || lines[0].Name != "ana: " || lines[0].Text != "aaa bbb" ||
		lines[1].Stamp != "" || lines[1].Name != "" || lines[1].Text != "ccc ddd" || lines[1].Indent != 13 {
		t.Fatalf("%+v", lines)
	}
}

func TestScrollbackTinyWidthDoesNotPanic(t *testing.T) {
	msgs := []Message{{FromName: "ana", Text: "olá ção é", TS: time.Now()}}
	if len(Scrollback(msgs, 1)) == 0 {
		t.Fatal("no lines")
	}
	if len(Scrollback(nil, 40)) != 0 {
		t.Fatal("expected none")
	}
}
