package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server/dedicated"
	"github.com/medeirosvictor/hermec/server/history"
)

func testDedicatedTransition() dedicated.Record {
	return dedicated.Record{Version: 1, State: dedicated.Prepared, ServerID: "server", DestinationFingerprint: "destination", ChainHead: 4, ChainHeadHash: bytes.Repeat([]byte{2}, 32), HistoryLastID: "history", HistoryLastUnixMilli: 9, HistoryHash: bytes.Repeat([]byte{3}, 32), TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: dedicated.RecoveryRetryPreparation}
}

func TestStartRetryRevalidatesInvalidDedicatedJournal(t *testing.T) {
	c, source, _ := twoHostChain(t)
	path := filepath.Join(t.TempDir(), dedicated.TransitionFile)
	if err := os.WriteFile(path, []byte(`{"version":1,"state":"invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Config{
		Addr: "127.0.0.1:0", HostKey: source, Chain: c,
		HistoryPath: filepath.Join(t.TempDir(), "history.jsonl"), DedicatedTransitionPath: path,
	})
	for attempt := 1; attempt <= 2; attempt++ {
		if err := s.Start(); err == nil {
			t.Fatalf("Start attempt %d accepted invalid dedicated journal", attempt)
		}
		if s.hist != nil {
			t.Fatalf("Start attempt %d retained history after failure", attempt)
		}
		if s.Addr() != "" {
			t.Fatalf("Start attempt %d became active at %q", attempt, s.Addr())
		}
	}
}

func TestRestorePreparingTransitionRemainsPassiveAndExact(t *testing.T) {
	c, _, destination := twoHostChain(t)
	h, err := history.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	head := c.Head()
	headHash := head.Hash()
	_, historyHash := proto.DedicatedHistoryCommitment(nil)
	path := filepath.Join(t.TempDir(), dedicated.TransitionFile)
	r := dedicated.Record{Version: 1, State: dedicated.Preparing, ServerID: c.ID().String(), DestinationFingerprint: identity.Fingerprint(destination.PublicKey()), ChainHead: head.Seq, ChainHeadHash: headHash[:], HistoryHash: historyHash, TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: dedicated.RecoveryRetryPreparation, ActiveWSSURL: "wss://source.example/"}
	if err := dedicated.Save(path, r); err != nil {
		t.Fatal(err)
	}
	s := New(Config{HostKey: destination, Chain: c, DedicatedTransitionPath: path})
	s.hist = h
	if err := s.restoreDedicatedTransition(); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModePrepared || !s.Prepared() {
		t.Fatalf("preparing recovery activated destination: mode=%v prepared=%v", s.Mode(), s.Prepared())
	}
	s.SetMode(ModeActive) // the public guard must retain passive mode
	if s.Mode() != ModePrepared {
		t.Fatal("prepared destination accepted activation")
	}
}

func TestPreparedHostWithSourceLinkCannotPromoteThroughPublicAPI(t *testing.T) {
	fastHostTimers(t)
	c, sourceKey, destinationKey := twoHostChain(t)
	source, sourceURL := startModeServer(t, Config{
		HostKey: sourceKey, Chain: c, HostName: "source", Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
	if source.Mode() != ModeActive {
		t.Fatalf("source mode = %v, want active", source.Mode())
	}
	destinationChain, err := chain.FromEntries(c.Entries())
	if err != nil {
		t.Fatal(err)
	}
	destinationAddr := freeAddr(t)
	h := NewHost(HostConfig{
		Prepared: true,
		Server: Config{
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
	h.Server().SetMode(ModeActive)
	if h.Server().Mode() != ModePrepared {
		t.Fatalf("SetMode activated prepared destination: %v", h.Server().Mode())
	}
	h.Server().SetMode(ModeIdle)
	if h.Server().Promote() || h.Server().Mode() == ModeActive {
		t.Fatal("Promote activated prepared destination")
	}
	h.Server().SetMode(ModePrepared)
	h.Server().PromoteWarm()
	if h.Server().Mode() == ModeActive || h.Role() != RolePrepared || !h.Server().Prepared() {
		t.Fatalf("public API activated prepared destination: role %q mode %v prepared %v", h.Role(), h.Server().Mode(), h.Server().Prepared())
	}
}

func TestPreparedHostDoesNotSelfElectWhenSourceIsUnavailable(t *testing.T) {
	fastHostTimers(t)
	h := NewHost(HostConfig{Prepared: true, Server: Config{Addr: freeAddr(t)}})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown(context.Background())
	time.Sleep(700 * time.Millisecond) // exceeds the shortened cold-start window
	if h.Role() != RolePrepared || h.Server().Mode() != ModePrepared {
		t.Fatalf("unlinked prepared host self-elected: role %q mode %v", h.Role(), h.Server().Mode())
	}
}
