package integration

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
)

// hostedConfig is unexported in package ui (which needs cgo), so this test
// builds the same shape by hand and round-trips it through the TOML file.
func TestHostedServerServesClients(t *testing.T) {
	idA, idB := newID(t), newID(t)
	fpA := identity.Fingerprint(idA.PublicKey())

	cfg := server.DefaultConfig()
	cfg.Addr = ":0"
	cfg.ServerName = "Hosted"
	cfg.Password = "pw"
	cfg.VoiceChannels = []string{"voice"}
	cfg.Roles.Grants = map[string][]string{fpA: {"admin"}}
	b, err := cfg.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hosted.toml")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := server.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Addr = "127.0.0.1:0"
	s := server.New(loaded)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	url := "ws://" + s.Addr() + "/"

	dial := func(id *identity.Identity, name string) *client.Client {
		c, err := client.Dial(testCtx(t), url, id, name, "pw")
		if err != nil {
			t.Fatalf("Dial %s: %v", name, err)
		}
		t.Cleanup(func() { c.Close() })
		if err := c.Join(testCtx(t), "general"); err != nil {
			t.Fatalf("Join %s: %v", name, err)
		}
		return c
	}
	a := dial(idA, "alice")
	c := dial(idB, "bob")

	waitEvent(t, a, "presence with both", func(e client.Event) bool {
		return e.Presence != nil && len(e.Presence.Members) == 2
	})
	waitEvent(t, c, "presence with both", func(e client.Event) bool {
		return e.Presence != nil && len(e.Presence.Members) == 2
	})
	if !slices.Contains(a.Roles(), "admin") {
		t.Errorf("alice roles = %v, want admin", a.Roles())
	}
	if slices.Contains(c.Roles(), "admin") {
		t.Errorf("bob roles = %v, want no admin", c.Roles())
	}
}
