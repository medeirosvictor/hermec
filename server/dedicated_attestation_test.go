package server

import (
	"path/filepath"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

func TestSourceReadinessAttestationOnlyAllowsExactReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), DedicatedTransitionFile)
	s := &Server{cfg: Config{DedicatedTransitionPath: path}}
	r := recordReadiness(testDedicatedTransition())
	if err := s.attestDedicatedReadiness(r); err != nil {
		t.Fatal(err)
	}
	// A restarted source sees the same sidecar and accepts only this exact proof.
	if err := (&Server{cfg: Config{DedicatedTransitionPath: path}}).checkDedicatedReadinessAttestation(r, true); err != nil {
		t.Fatalf("exact replay refused: %v", err)
	}
	changedNonce := r
	changedNonce.TransitionNonce = append([]byte(nil), r.TransitionNonce...)
	changedNonce.TransitionNonce[0]++
	if err := s.checkDedicatedReadinessAttestation(changedNonce, true); err == nil {
		t.Fatal("changed nonce accepted")
	}
	changedBoundary := r
	changedBoundary.ChainHead++
	if err := s.checkDedicatedReadinessAttestation(changedBoundary, true); err == nil {
		t.Fatal("changed boundary accepted")
	}
}

func TestResetObsoleteReadinessAttestationInvalidatesOldNonce(t *testing.T) {
	c, source, _ := twoHostChain(t)
	path := filepath.Join(t.TempDir(), DedicatedTransitionFile)
	s := New(Config{HostKey: source, Chain: c, DedicatedTransitionPath: path})
	r := recordReadiness(testDedicatedTransition())
	if err := s.attestDedicatedReadiness(r); err != nil {
		t.Fatal(err)
	}
	drifted := r
	drifted.ChainHead++
	if err := s.checkDedicatedReadinessAttestation(drifted, false); err == nil {
		t.Fatal("drifted preparation was not blocked")
	}
	operator := identity.Fingerprint(source.PublicKey())
	if err := s.ResetDedicatedReadinessAttestation(operator); err != nil {
		t.Fatal(err)
	}
	if err := s.checkDedicatedReadinessAttestation(r, true); err == nil {
		t.Fatal("old nonce was accepted after reset")
	}
	if err := s.attestDedicatedReadiness(drifted); err != nil {
		t.Fatalf("reset did not permit replacement attestation: %v", err)
	}
}

func TestResetReadinessAttestationRefusesFencedSource(t *testing.T) {
	c, source, _ := twoHostChain(t)
	s := New(Config{HostKey: source, Chain: c, DedicatedTransitionPath: filepath.Join(t.TempDir(), DedicatedTransitionFile)})
	s.SetMode(ModeFenced)
	if err := s.ResetDedicatedReadinessAttestation(identity.Fingerprint(source.PublicKey())); err == nil {
		t.Fatal("fenced source reset accepted")
	}
}
