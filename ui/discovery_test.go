package ui

import (
	"context"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

// While hosting, discovery must not offer this machine's own server under its
// LAN or tailnet address: joining it would open a second session to yourself.
func TestDropSelfHosted(t *testing.T) {
	found := []state.DiscoveredIn{
		{Name: "Den", Addr: "192.168.1.5:7697", Source: "lan"},
		{Name: "Den", Addr: "100.64.0.9:7697", Source: "tailnet"},
		{Name: "Den", Addr: "127.0.0.1:7697", Source: "saved"},
		{Name: "other port", Addr: "192.168.1.5:9999", Source: "lan"},
		{Name: "friend", Addr: "192.168.1.77:7697", Source: "lan"},
	}
	self := func(context.Context) (string, string) { return "100.64.0.9", "192.168.1.5" }
	got := dropSelfHosted(context.Background(), found, self, 7697)
	if len(got) != 2 || got[0].Name != "other port" || got[1].Name != "friend" {
		t.Fatalf("kept %+v", got)
	}
	if got := dropSelfHosted(context.Background(), found, self, 0); len(got) != len(found) {
		t.Errorf("port 0 (not hosting) dropped rows: %+v", got)
	}
}

func TestSelfHostPort(t *testing.T) {
	g := newTestGame(t)
	if p := g.selfHostPort(); p != 0 {
		t.Errorf("not hosting: port = %d", p)
	}
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	if p := g.selfHostPort(); p == 0 {
		t.Errorf("hosting: port = %d", p)
	}
	g.stopHosted()
	if p := g.selfHostPort(); p != 0 {
		t.Errorf("stopped: port = %d", p)
	}
}
