package proto

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

func TestDedicatedReadinessIsDomainSeparatedAndNonceBound(t *testing.T) {
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, DedicatedTransitionNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	r := DedicatedReadiness{ServerID: "server", DestinationFingerprint: "destination", ChainHead: 7, HistoryLastID: "line", HistoryLastUnixMilli: 12, TransitionNonce: nonce}
	sig, err := SignDedicatedReadiness(key, r)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyDedicatedReadiness(key.PublicKey(), r, sig) {
		t.Fatal("valid readiness proof rejected")
	}
	other := r
	other.TransitionNonce = append([]byte(nil), nonce...)
	other.TransitionNonce[0] ^= 1
	if VerifyDedicatedReadiness(key.PublicKey(), other, sig) {
		t.Fatal("proof replayed for another nonce")
	}
	b, err := DedicatedReadinessBytes(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("hermec-dedicated-readiness-v1\x00")) {
		t.Fatalf("missing readiness domain: %q", b)
	}
}

func TestDedicatedReadinessBounds(t *testing.T) {
	r := DedicatedReadiness{ServerID: "s", DestinationFingerprint: "d", TransitionNonce: make([]byte, DedicatedTransitionNonceSize)}
	p := DedicatedReadinessProof{Readiness: r, Signature: make([]byte, 64)}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	r.TransitionNonce = nil
	if err := r.Validate(); err == nil {
		t.Fatal("short nonce accepted")
	}
}
