package integration

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server"
)

// TestHostedMigrationEndToEnd starts a server from an old-style config (group
// fields in the config, no chain yet), lets the founder promote a member, has
// that admin ban a third key, then restarts from the same chain file.
func TestHostedMigrationEndToEnd(t *testing.T) {
	idA, idB, idC := newID(t), newID(t), newID(t)
	fpB := identity.Fingerprint(idB.PublicKey())
	fpC := identity.Fingerprint(idC.PublicKey())
	chainPath := filepath.Join(t.TempDir(), "chain.jsonl")

	start := func() (*server.Server, string) {
		cfg := server.DefaultConfig()
		cfg.Addr = "127.0.0.1:0"
		cfg.ServerName = "Migrated"
		cfg.Password = "pw"
		c, _, err := server.OpenChain(chainPath, idA, cfg)
		if err != nil {
			t.Fatalf("OpenChain: %v", err)
		}
		cfg.HostKey, cfg.Chain = idA, c
		s := server.New(cfg)
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		return s, "ws://" + s.Addr() + "/"
	}
	stop := func(s *server.Server) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}

	s, url := start()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stop(s)
		}
	})

	a, err := client.DialPinned(testCtx(t), url, idA, "alice", "pw", "")
	if err != nil {
		t.Fatalf("Dial A: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := client.DialPinned(testCtx(t), url, idB, "bob", "pw", "")
	if err != nil {
		t.Fatalf("Dial B: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	if a.ServerID() == "" || a.ServerID() != b.ServerID() {
		t.Fatalf("server ids differ: %q vs %q", a.ServerID(), b.ServerID())
	}
	pin := a.ServerID()

	// The founder makes B an admin; B's chain copy gets the entry live.
	if err := a.ChainAppend(testCtx(t), chain.OpGrant, chain.GrantBody{Fingerprint: fpB, Roles: []string{"admin"}}); err != nil {
		t.Fatalf("A grant: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for b.Chain().Head().Seq < a.Chain().Head().Seq {
		if time.Now().After(deadline) {
			t.Fatal("grant never reached B")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := b.Chain().State(); !slices.Contains(st.Grants[fpB], "admin") {
		t.Fatalf("B's chain grants = %v, want admin", st.Grants[fpB])
	}

	// B, now an admin, bans C; C is refused with "banned".
	if err := b.ChainAppend(testCtx(t), chain.OpBan, chain.FPBody{Fingerprint: fpC}); err != nil {
		t.Fatalf("B ban: %v", err)
	}
	var se *client.ServerError
	_, err = client.DialPinned(testCtx(t), url, idC, "carol", "pw", pin)
	if !errors.As(err, &se) || se.Code != proto.CodeBanned {
		t.Fatalf("banned C dial: err = %v, want code %q", err, proto.CodeBanned)
	}

	// Restart from the same chain file: the pin still matches and the ban holds.
	a.Close()
	b.Close()
	stop(s)
	stopped = true
	s, url = start()
	t.Cleanup(func() { stop(s) })

	a2, err := client.DialPinned(testCtx(t), url, idA, "alice", "pw", pin)
	if err != nil {
		t.Fatalf("A redial with pin: %v", err)
	}
	t.Cleanup(func() { a2.Close() })
	if a2.ServerID() != pin {
		t.Errorf("server id after restart = %q, want %q", a2.ServerID(), pin)
	}
	_, err = client.DialPinned(testCtx(t), url, idC, "carol", "pw", pin)
	if !errors.As(err, &se) || se.Code != proto.CodeBanned {
		t.Fatalf("banned C after restart: err = %v, want code %q", err, proto.CodeBanned)
	}
}
