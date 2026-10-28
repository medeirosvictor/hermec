package server

import (
	"path/filepath"
	"testing"
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
