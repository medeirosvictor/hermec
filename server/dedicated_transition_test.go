package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func testDedicatedTransition() DedicatedTransitionRecord {
	return DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionPrepared, ServerID: "server", DestinationFingerprint: "destination", ChainHead: 4, ChainHeadHash: bytes.Repeat([]byte{2}, 32), HistoryLastID: "history", HistoryLastUnixMilli: 9, HistoryHash: bytes.Repeat([]byte{3}, 32), TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: DedicatedRecoveryRetryPreparation}
}

func TestDedicatedTransitionAtomicPrivateRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transition.json")
	want := testDedicatedTransition()
	if err := SaveDedicatedTransition(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
	got, err := LoadDedicatedTransition(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerID != want.ServerID || got.State != want.State || !bytes.Equal(got.TransitionNonce, want.TransitionNonce) {
		t.Fatalf("loaded record = %+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "private_key", "password_key"} {
		if bytes.Contains(bytes.ToLower(data), []byte(forbidden)) {
			t.Fatalf("record leaks %q: %s", forbidden, data)
		}
	}
}

func TestDedicatedTransitionRejectsInsecureRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transition.json")
	if err := SaveDedicatedTransition(path, testDedicatedTransition()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission assertion")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDedicatedTransition(path); err == nil {
		t.Fatal("world-readable transition accepted")
	}
}

func TestDedicatedTransitionUsesFixedRecoveryGuidance(t *testing.T) {
	r := testDedicatedTransition()
	if r.Recovery.Guidance() != "Keep the current host active; retry preparation." {
		t.Fatalf("guidance = %q", r.Recovery.Guidance())
	}
	r.Recovery = DedicatedRecoveryCode("password: leaked")
	if err := r.validate(); err == nil {
		t.Fatal("arbitrary recovery text accepted")
	}
}

func TestDedicatedTransitionRejectsSymlinkAndOversizedRecord(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := SaveDedicatedTransition(target, testDedicatedTransition()); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "transition.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := LoadDedicatedTransition(link); err == nil {
		t.Fatal("symlink transition accepted")
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte{'x'}, maxDedicatedTransitionBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDedicatedTransition(big); err == nil {
		t.Fatal("oversized transition accepted")
	}
}

func TestValidateDedicatedPreparedChecksLocalBoundary(t *testing.T) {
	c, _, destination := twoHostChain(t)
	h, err := OpenHistory("")
	if err != nil {
		t.Fatal(err)
	}
	m := proto.ChatMessage{ID: "boundary", Channel: "general", TS: time.UnixMilli(9)}
	if err := h.Add(m); err != nil {
		t.Fatal(err)
	}
	head := c.Head()
	headHash := head.Hash()
	count, historyHash := proto.DedicatedHistoryCommitment([]proto.ChatMessage{m})
	r := DedicatedTransitionRecord{
		Version: 1, State: DedicatedTransitionPrepared, ServerID: c.ID().String(),
		DestinationFingerprint: identity.Fingerprint(destination.PublicKey()), ChainHead: head.Seq, ChainHeadHash: headHash[:],
		HistoryLastID: m.ID, HistoryLastUnixMilli: m.TS.UnixMilli(), HistoryCount: count, HistoryHash: historyHash,
		TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: DedicatedRecoveryRetryPreparation,
	}
	if err := ValidateDedicatedPrepared(r, destination, c, h); err != nil {
		t.Fatal(err)
	}
	r.HistoryLastID = "gone"
	if err := ValidateDedicatedPrepared(r, destination, c, h); err == nil {
		t.Fatal("accepted missing history boundary")
	}
	r.HistoryLastID = m.ID
	r.ChainHeadHash = append([]byte(nil), r.ChainHeadHash...)
	r.ChainHeadHash[0] ^= 1
	if err := ValidateDedicatedPrepared(r, destination, c, h); err == nil {
		t.Fatal("accepted divergent chain head at same sequence")
	}
	r.ChainHeadHash = headHash[:]
	r.HistoryHash = append([]byte(nil), r.HistoryHash...)
	r.HistoryHash[0] ^= 1
	if err := ValidateDedicatedPrepared(r, destination, c, h); err == nil {
		t.Fatal("accepted divergent complete history snapshot")
	}
}

func TestTerminalTransitionVerifiesAuthorizedSourceSignature(t *testing.T) {
	c, source, destination := twoHostChain(t)
	h, err := OpenHistory("")
	if err != nil {
		t.Fatal(err)
	}
	head := c.Head()
	headHash := head.Hash()
	_, historyHash := proto.DedicatedHistoryCommitment(nil)
	readiness := proto.DedicatedReadiness{ServerID: c.ID().String(), DestinationFingerprint: identity.Fingerprint(destination.PublicKey()), ChainHead: head.Seq, ChainHeadHash: headHash[:], HistoryHash: historyHash, TransitionNonce: bytes.Repeat([]byte{7}, proto.DedicatedTransitionNonceSize)}
	dst := proto.HostInfo{Fingerprint: readiness.DestinationFingerprint}
	sig, err := proto.SignDedicatedCutover(source, readiness, identity.Fingerprint(source.PublicKey()), dst)
	if err != nil {
		t.Fatal(err)
	}
	r := DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionCommitted, ServerID: readiness.ServerID, DestinationFingerprint: readiness.DestinationFingerprint, ChainHead: readiness.ChainHead, ChainHeadHash: readiness.ChainHeadHash, HistoryCount: readiness.HistoryCount, HistoryHash: readiness.HistoryHash, TransitionNonce: readiness.TransitionNonce, Certificate: &proto.DedicatedCertificate{Readiness: readiness, SourceFingerprint: identity.Fingerprint(source.PublicKey()), Destination: dst, Signature: sig}, Recovery: DedicatedRecoveryInspectCommit}
	if err := ValidateDedicatedCommitted(r, destination, c, h); err != nil {
		t.Fatal(err)
	}
	r.Certificate.Signature[0] ^= 1
	if err := ValidateDedicatedCommitted(r, destination, c, h); err == nil {
		t.Fatal("accepted tampered terminal certificate")
	}
}

func TestPreparedHostWithSourceLinkCannotPromoteThroughPublicAPI(t *testing.T) {
	fastHostTimers(t)
	c, sourceKey, destinationKey := twoHostChain(t)
	source, sourceURL := startModeServer(t, Config{
		HostKey: sourceKey, Chain: c, HostName: "source", Password: "pw",
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
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
			HostKey: destinationKey, Chain: destinationChain, HostName: "destination", Password: "pw",
			Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
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
