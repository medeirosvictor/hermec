package state

import "testing"

func TestDefaultName(t *testing.T) {
	if got := DefaultName("k7mv-q3xp-9dfw-02hj"); got != "anon-k7mv" {
		t.Fatalf("got %q", got)
	}
	if got := DefaultName(""); got != "anon" {
		t.Fatalf("got %q", got)
	}
}
