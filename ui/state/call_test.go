package state

import "testing"

func TestCallTitle(t *testing.T) {
	for _, c := range []struct {
		ch, server string
		elsewhere  bool
		want       string
	}{
		{"voice", "primos", false, "voice"},
		{"voice", "primos", true, "voice @ primos"},
		{"voice", "", true, "voice"}, // no label known: no dangling "@"
	} {
		if got := CallTitle(c.ch, c.server, c.elsewhere); got != c.want {
			t.Errorf("CallTitle(%q, %q, %v) = %q, want %q", c.ch, c.server, c.elsewhere, got, c.want)
		}
	}
}
