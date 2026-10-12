package ui

import (
	"testing"
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
