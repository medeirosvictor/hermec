package ui

import (
	"testing"
)

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
