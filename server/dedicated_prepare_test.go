package server

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func TestInstallDedicatedBootstrapRejectsUnrelatedOrUnauthorizedState(t *testing.T) {
	local := mustID(t)
	bootstrap, err := newChain(local, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	other := mustID(t)
	unrelated, err := newChain(other, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallDedicatedBootstrap(bootstrap, local, unrelated.Entries(), ""); err == nil {
		t.Fatal("accepted chain which does not authorize VPS key")
	}
	// A bootstrap that has acquired any non-bootstrap state is never replaced.
	head := bootstrap.Head()
	e, err := chain.NewEntry(local, &head, chain.OpGrant, chain.GrantBody{Fingerprint: identity.Fingerprint(other.PublicKey()), Roles: []string{"user"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Append(e); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallDedicatedBootstrap(bootstrap, local, bootstrap.Entries(), ""); err == nil {
		t.Fatal("replaced non-bootstrap chain")
	}
}

func TestPreparedLinkAttestsCompleteMultipageHistoryBeforePersistence(t *testing.T) {
	fastHostTimers(t)
	c, sourceKey, destinationKey := twoHostChain(t)
	source, sourceURL := startModeServer(t, Config{HostKey: sourceKey, Chain: c, HostName: "source", Password: "pw", Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}}, Channels: []string{"general"}})
	for i := 0; i < historyPage*2+7; i++ {
		m := proto.ChatMessage{ID: "prepare-" + string(rune(i+1)), Channel: "general", Text: "history", TS: time.UnixMilli(int64(i + 1))}
		if err := source.hist.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	dc, err := chain.FromEntries(c.Entries())
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := startModeServer(t, Config{HostKey: destinationKey, Chain: dc, Mode: ModePrepared, Prepared: true, HostName: "destination", Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}}, Channels: []string{"general"}})
	l := dialLink(t, sourceURL, dest)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "transition.json")
	if err := l.PersistDedicatedPreparation(ctx, path); err != nil {
		t.Fatal(err)
	}
	if got, _ := dest.hist.After(time.Time{}, "", historyPage*3); len(got) != historyPage*2+7 {
		t.Fatalf("history count = %d", len(got))
	}
	r, err := LoadDedicatedTransition(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != DedicatedTransitionPrepared || r.ChainHead != c.Head().Seq {
		t.Fatalf("transition = %+v", r)
	}
}

func TestDedicatedHistorySnapshotSurvivesTrimAndConcurrentAppend(t *testing.T) {
	h, err := OpenHistory("")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := h.Add(proto.ChatMessage{ID: fmt.Sprintf("snapshot-%d", i), Channel: "general", TS: time.Unix(int64(i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _, _ := h.Snapshot()
	done := make(chan error, 1)
	go func() { done <- h.Trim(time.Now().Add(48 * time.Hour)) }()
	if err := h.Add(proto.ChatMessage{ID: "after-snapshot", Channel: "general", TS: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 3 || snapshot[0].ID != "snapshot-0" {
		t.Fatalf("snapshot changed during trim/append: %+v", snapshot)
	}
}

func TestDedicatedProofRejectsReplayAndSourceMovement(t *testing.T) {
	// Unit-level equality includes the nonce, so a prior transition cannot be
	// acknowledged on a fresh authenticated link.
	nonce := make([]byte, proto.DedicatedTransitionNonceSize)
	a := proto.DedicatedReadiness{ServerID: "server", DestinationFingerprint: "destination", TransitionNonce: nonce}
	b := a
	b.TransitionNonce = append([]byte(nil), nonce...)
	b.TransitionNonce[0] = 1
	if sameReadiness(a, b) {
		t.Fatal("different nonce accepted as replay-safe readiness")
	}
}
