package names

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"  bob  ", 10, "bob"},
		{"a‮b\x00c d e", 10, "abcde"},
		{strings.Repeat("x", 1000), 64, strings.Repeat("x", 64)},
		{"‮\x07", 10, ""},
	}
	for _, c := range cases {
		if got := Clean(c.in, c.max); got != c.want {
			t.Errorf("Clean(%q,%d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}
