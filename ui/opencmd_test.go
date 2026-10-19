package ui

import (
	"testing"
)

func TestOpenCommandSelection(t *testing.T) {
	cases := []struct{ goos, name string }{{"darwin", "open"}, {"linux", "xdg-open"}, {"freebsd", "xdg-open"}}
	for _, c := range cases {
		n, a := openCommand(c.goos, "/x/logs")
		if n != c.name || len(a) != 1 || a[0] != "/x/logs" {
			t.Errorf("%s: got %q %v", c.goos, n, a)
		}
	}
}

func TestOpenLogFolderShowsPathAndOpens(t *testing.T) {
	g := &game{}
	g.opts.LogDir = t.TempDir()
	var got []string
	g.openFn = func(s string) { got = append(got, s) }
	g.openLogFolder()
	if len(got) != 1 || got[0] != g.opts.LogDir {
		t.Fatalf("opened %v", got)
	}
	if g.notice != g.opts.LogDir {
		t.Fatalf("notice = %q", g.notice)
	}
}
