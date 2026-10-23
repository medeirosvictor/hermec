package ui

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

func TestAuthFailedClearsCachedPasswordKey(t *testing.T) {
	g := newTestGame(t)
	g.servers = []state.ServerEntry{{URL: "k", PasswordKey: hex.EncodeToString(make([]byte, 32))}}
	s := newSession("k", "ws://127.0.0.1:1/", "me")
	s.dialing = true
	g.sess["k"] = s
	err := fmt.Errorf("client: auth rejected: %w", &client.ServerError{Code: "auth_failed", Message: "bad"})
	g.applyDial(dialResult{key: "k", s: s, gen: s.dialGen, err: err})
	if g.servers[0].PasswordKey != "" {
		t.Fatal("cached key kept after auth_failed")
	}
	saved, _ := state.LoadServers(g.serversPath)
	if len(saved) != 1 || saved[0].PasswordKey != "" {
		t.Fatalf("saved = %+v", saved)
	}
}
