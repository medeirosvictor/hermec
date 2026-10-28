package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
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

func TestSourceServiceReadinessLockBlocksOfflineResetUntilShutdown(t *testing.T) {
	c, source, _ := twoHostChain(t)
	path := filepath.Join(t.TempDir(), DedicatedTransitionFile)
	s := New(Config{Addr: "127.0.0.1:0", HostKey: source, Chain: c, DedicatedTransitionPath: path})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if lock, err := AcquireDedicatedReadinessLock(path); !errors.Is(err, ErrDedicatedReadinessLocked) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("offline reset lock = %v, want running-service refusal", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireDedicatedReadinessLock(path)
	if err != nil {
		t.Fatalf("offline reset lock after shutdown: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestResetReadinessAttestationDropsPreparedLinks(t *testing.T) {
	c, source, _ := twoHostChain(t)
	path := filepath.Join(t.TempDir(), DedicatedTransitionFile)
	s := New(Config{HostKey: source, Chain: c, DedicatedTransitionPath: path})
	r := recordReadiness(testDedicatedTransition())
	if err := s.attestDedicatedReadiness(r); err != nil {
		t.Fatal(err)
	}
	peer := &conn{link: &linkState{hello: proto.LinkHello{Prepared: true}}}
	s.links[peer] = struct{}{}
	if err := s.ResetDedicatedReadinessAttestation(identity.Fingerprint(source.PublicKey())); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.links[peer]; ok || peer.link.dedicated != nil || peer.link.dedicatedProof != nil {
		t.Fatal("reset retained a live prepared link")
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
