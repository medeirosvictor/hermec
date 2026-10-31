package dedicated

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server/history"
)

func mustID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// bootstrapChain is a fresh genesis plus host_add for key, the shape of a
// destination's local bootstrap.
func bootstrapChain(t *testing.T, key *identity.Identity) *chain.Chain {
	t.Helper()
	g, err := chain.NewEntry(key, nil, chain.OpGenesis, chain.GenesisBody{Name: "test", Founder: []byte(key.PublicKey()), Salt: bytes.Repeat([]byte{5}, 16)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, err := chain.New(g)
	if err != nil {
		t.Fatal(err)
	}
	addHost(t, c, key, key, 1)
	return c
}

func addHost(t *testing.T, c *chain.Chain, founder, host *identity.Identity, priority int) {
	t.Helper()
	head := c.Head()
	e, err := chain.NewEntry(founder, &head, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(host.PublicKey()), Priority: priority}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(e); err != nil {
		t.Fatal(err)
	}
}

func twoHostChain(t *testing.T) (c *chain.Chain, a, b *identity.Identity) {
	t.Helper()
	a, b = mustID(t), mustID(t)
	c = bootstrapChain(t, a)
	addHost(t, c, a, b, 2)
	return c, a, b
}

func testRecord() Record {
	return Record{Version: 1, State: Prepared, ServerID: "server", DestinationFingerprint: "destination", ChainHead: 4, ChainHeadHash: bytes.Repeat([]byte{2}, 32), HistoryLastID: "history", HistoryLastUnixMilli: 9, HistoryHash: bytes.Repeat([]byte{3}, 32), TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: RecoveryRetryPreparation}
}

func TestTransitionAtomicPrivateRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transition.json")
	want := testRecord()
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
	got, err := Load(path)
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

func TestPreparingTransitionRequiresPinnedSourceURL(t *testing.T) {
	r := testRecord()
	r.State = Preparing
	if err := r.validate(); err == nil {
		t.Fatal("accepted preparing state without source URL")
	}
	r.ActiveWSSURL = "wss://source.example/"
	if err := r.validate(); err != nil {
		t.Fatalf("valid preparing state: %v", err)
	}
}

func TestTransitionRejectsInsecureRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transition.json")
	if err := Save(path, testRecord()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission assertion")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("world-readable transition accepted")
	}
}

func TestTransitionUsesFixedRecoveryGuidance(t *testing.T) {
	r := testRecord()
	if r.Recovery.Guidance() != "Keep the current host active; retry preparation." {
		t.Fatalf("guidance = %q", r.Recovery.Guidance())
	}
	r.Recovery = RecoveryCode("password: leaked")
	if err := r.validate(); err == nil {
		t.Fatal("arbitrary recovery text accepted")
	}
}

func TestTransitionRejectsSymlinkAndOversizedRecord(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := Save(target, testRecord()); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "transition.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("symlink transition accepted")
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte{'x'}, maxTransitionBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); err == nil {
		t.Fatal("oversized transition accepted")
	}
}

func TestValidateDedicatedPreparedChecksLocalBoundary(t *testing.T) {
	c, _, destination := twoHostChain(t)
	h, err := history.Open("")
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
	r := Record{
		Version: 1, State: Prepared, ServerID: c.ID().String(),
		DestinationFingerprint: identity.Fingerprint(destination.PublicKey()), ChainHead: head.Seq, ChainHeadHash: headHash[:],
		HistoryLastID: m.ID, HistoryLastUnixMilli: m.TS.UnixMilli(), HistoryCount: count, HistoryHash: historyHash,
		TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: RecoveryRetryPreparation,
	}
	if err := ValidatePrepared(r, destination, c, h); err != nil {
		t.Fatal(err)
	}
	r.HistoryLastID = "gone"
	if err := ValidatePrepared(r, destination, c, h); err == nil {
		t.Fatal("accepted missing history boundary")
	}
	r.HistoryLastID = m.ID
	r.ChainHeadHash = append([]byte(nil), r.ChainHeadHash...)
	r.ChainHeadHash[0] ^= 1
	if err := ValidatePrepared(r, destination, c, h); err == nil {
		t.Fatal("accepted divergent chain head at same sequence")
	}
	r.ChainHeadHash = headHash[:]
	r.HistoryHash = append([]byte(nil), r.HistoryHash...)
	r.HistoryHash[0] ^= 1
	if err := ValidatePrepared(r, destination, c, h); err == nil {
		t.Fatal("accepted divergent complete history snapshot")
	}
}

func TestTerminalTransitionVerifiesAuthorizedSourceSignature(t *testing.T) {
	c, source, destination := twoHostChain(t)
	h, err := history.Open("")
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
	r := Record{Version: 1, State: Committed, ServerID: readiness.ServerID, DestinationFingerprint: readiness.DestinationFingerprint, ChainHead: readiness.ChainHead, ChainHeadHash: readiness.ChainHeadHash, HistoryCount: readiness.HistoryCount, HistoryHash: readiness.HistoryHash, TransitionNonce: readiness.TransitionNonce, Certificate: &proto.DedicatedCertificate{Readiness: readiness, SourceFingerprint: identity.Fingerprint(source.PublicKey()), Destination: dst, Signature: sig}, Recovery: RecoveryInspectCommit}
	if err := ValidateCommitted(r, destination, c, h); err != nil {
		t.Fatal(err)
	}
	r.Certificate.Signature[0] ^= 1
	if err := ValidateCommitted(r, destination, c, h); err == nil {
		t.Fatal("accepted tampered terminal certificate")
	}
}
