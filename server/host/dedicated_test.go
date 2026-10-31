package host

import (
	"context"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

func TestPreparedHostWithSourceLinkCannotPromoteThroughPublicAPI(t *testing.T) {
	fastHostTimers(t)
	c, sourceKey, destinationKey := twoHostChain(t)
	source, sourceURL := startServer(t, server.Config{
		HostKey: sourceKey, Chain: c, HostName: "source", Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
	if source.Mode() != server.ModeActive {
		t.Fatalf("source mode = %v, want active", source.Mode())
	}
	destinationChain, err := chain.FromEntries(c.Entries())
	if err != nil {
		t.Fatal(err)
	}
	destinationAddr := freeAddr(t)
	h := New(Config{
		Prepared: true,
		Server: server.Config{
			Addr: destinationAddr, Advertise: []string{"ws://" + destinationAddr},
			HostKey: destinationKey, Chain: destinationChain, HostName: "destination", Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
			Channels: []string{"general"},
		},
		Known: func() []string { return []string{sourceURL} },
	})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown(context.Background())

	waitFor(t, "prepared destination source link", func() bool {
		return len(source.Links()) == 1 && h.Role() == RolePrepared
	})

	// These are all public activation-capable Server operations. A prepared
	// destination has no public commit seam: source presence alone cannot
	// substitute for verified durable transition evidence and a cutover.
	h.Server().SetMode(server.ModeActive)
	if h.Server().Mode() != server.ModePrepared {
		t.Fatalf("SetMode activated prepared destination: %v", h.Server().Mode())
	}
	h.Server().SetMode(server.ModeIdle)
	if h.Server().Promote() || h.Server().Mode() == server.ModeActive {
		t.Fatal("Promote activated prepared destination")
	}
	h.Server().SetMode(server.ModePrepared)
	h.Server().PromoteWarm()
	if h.Server().Mode() == server.ModeActive || h.Role() != RolePrepared || !h.Server().Prepared() {
		t.Fatalf("public API activated prepared destination: role %q mode %v prepared %v", h.Role(), h.Server().Mode(), h.Server().Prepared())
	}
}

func TestPreparedHostDoesNotSelfElectWhenSourceIsUnavailable(t *testing.T) {
	fastHostTimers(t)
	h := New(Config{Prepared: true, Server: server.Config{Addr: freeAddr(t)}})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown(context.Background())
	time.Sleep(700 * time.Millisecond) // exceeds the shortened cold-start window
	if h.Role() != RolePrepared || h.Server().Mode() != server.ModePrepared {
		t.Fatalf("unlinked prepared host self-elected: role %q mode %v", h.Role(), h.Server().Mode())
	}
}
