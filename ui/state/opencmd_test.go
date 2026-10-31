package state

import "testing"

func TestOpenCommandSelection(t *testing.T) {
	cases := []struct{ goos, name string }{{"darwin", "open"}, {"linux", "xdg-open"}, {"freebsd", "xdg-open"}}
	for _, c := range cases {
		n, a := OpenCommand(c.goos, "/x/logs")
		if n != c.name || len(a) != 1 || a[0] != "/x/logs" {
			t.Errorf("%s: got %q %v", c.goos, n, a)
		}
	}
}
