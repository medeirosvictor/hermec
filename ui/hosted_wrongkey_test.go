package ui

import (
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

func TestServerIDForHostedPrefersTheChain(t *testing.T) {
	g := newTestGame(t)
	g.servers = []state.ServerEntry{{URL: hostedKey, Label: "h", ServerID: "stale-pin"}}
	g.hosted = &hostedServer{id: "chain-id"}
	if got := g.serverIDFor(hostedKey); got != "chain-id" {
		t.Errorf("serverIDFor = %q, want the hosted chain's id", got)
	}
}

// Without the host key that the chain lists (an identity file lost and
// regenerated), the app must not host: it shows why on the tile instead.
func TestHostedWithWrongKeyDoesNotStart(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	g.stopHosted()

	g2 := newTestGame(t) // a fresh identity, not on the chain's host list
	withLayout(t, g2)
	t.Cleanup(g2.stopHosted)
	g2.bootHosted(path)
	h := g2.hosted
	if h == nil || h.srv != nil {
		t.Fatalf("hosted = %+v, want present but not running", h)
	}
	if !strings.Contains(h.err, "not an approved host") {
		t.Errorf("hosted.err = %q", h.err)
	}
	s := g2.sess[hostedKey]
	if s == nil || !s.failed || !strings.Contains(s.lastErr, "not an approved host") {
		t.Errorf("tile = %+v, want failed with the reason", s)
	}
}
