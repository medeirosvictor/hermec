package state

import "testing"

func TestInputRunes(t *testing.T) {
	var b InputBuffer
	b.AppendRunes([]rune("olá ção"))
	if b.String() != "olá ção" {
		t.Fatalf("got %q", b.String())
	}
	b.Backspace()
	if b.String() != "olá çã" {
		t.Fatalf("got %q", b.String())
	}
}

func TestSubmitTrimsAndClears(t *testing.T) {
	var b InputBuffer
	b.AppendRunes([]rune("  oi  "))
	if got := b.Submit(); got != "oi" || b.String() != "" {
		t.Fatalf("got %q rest %q", got, b.String())
	}
}

func TestSubmitEmptyReturnsEmpty(t *testing.T) {
	var b InputBuffer
	b.AppendRunes([]rune("   "))
	if got := b.Submit(); got != "" {
		t.Fatalf("got %q", got)
	}
	b.Backspace() // no panic on empty
}
