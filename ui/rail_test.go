package ui

import (
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

// A saved local entry in a non-local run must report an error without
// leaving the game stuck in the dialing state.
func TestStartDialLocalNotRunningDoesNotWedge(t *testing.T) {
	g := &game{st: state.New()}
	g.startDial(localKey, "me")
	if g.dialing {
		t.Fatal("dialing stuck true after local-not-running")
	}
	if g.st.ConnectErr == "" {
		t.Fatal("want ConnectErr")
	}
	g.addServer() // would bail if dialing were stuck
	if g.curKey != "" || g.connect.focus != 1 {
		t.Fatal("addServer did not run")
	}
}
