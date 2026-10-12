package ui

import (
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

// A saved local entry in a non-local run must report an error without
// leaving a session behind or the game stuck in a dialing state.
func TestStartDialLocalNotRunningDoesNotWedge(t *testing.T) {
	g := newTestGame(t)
	if s := g.openSession(localKey, "me"); s != nil {
		t.Fatalf("openSession = %+v, want nil", s)
	}
	if len(g.sess) != 0 {
		t.Fatalf("sessions = %d, want 0", len(g.sess))
	}
	if g.none.st.ConnectErr == "" {
		t.Fatal("want ConnectErr")
	}
	g.addServer()
	if g.curKey != "" || g.connect.focus != 1 {
		t.Fatal("addServer did not run")
	}
}

func TestTileFacts(t *testing.T) {
	g := newTestGame(t)
	mk := func(key string) *session {
		s := newSession(key, key, "me")
		g.sess[key] = s
		return s
	}
	// The zero clients are not closable; drop them before newTestGame's cleanup.
	t.Cleanup(func() {
		for _, s := range g.sess {
			s.c = nil
		}
	})
	up := func(s *session) {
		s.c = &client.Client{}
		s.everUp = true
	}
	view := mk("view")
	up(view)
	bg := mk("bg")
	bg.everUp = true // c == nil: reconnecting
	call := mk("call")
	up(call)
	unread := mk("unread")
	up(unread)
	unread.st.Unread = map[string]bool{"general": true}
	first := mk("first") // first dial, never connected
	first.dialing = true
	g.curKey, g.callKey = "view", "call"

	cases := map[string]state.TileFacts{
		"view":   {Viewed: true, HasSession: true},
		"bg":     {HasSession: true, Reconnecting: true},
		"call":   {HasSession: true, InCall: true},
		"unread": {HasSession: true, Unread: true},
		"first":  {HasSession: true},
		"nosess": {},
	}
	for k, want := range cases {
		if got := g.tileFacts(state.ServerEntry{URL: k}); got != want {
			t.Errorf("%s: got %+v want %+v", k, got, want)
		}
	}
}
