package state

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func b64(pub []byte) string { return base64.RawStdEncoding.EncodeToString(pub) }
func manageKey() []byte     { return ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)) }
func manageReady(server, fp string) *proto.DedicatedReadiness {
	return &proto.DedicatedReadiness{ServerID: server, DestinationFingerprint: fp, ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, proto.DedicatedTransitionNonceSize)}
}

func readyManager(t *testing.T) *DedicatedManager {
	t.Helper()
	m := new(DedicatedManager)
	m.Open("server", true)
	pub := manageKey()
	if !m.Parse(b64(pub)) {
		t.Fatal("parse failed")
	}
	_, _, authorize, ok := m.Confirm(nil)
	if !ok || !m.Authorized(m.Gen, authorize, nil) {
		t.Fatal("authorization failed")
	}
	ready, ok := m.BeginReady()
	if !ok || !m.Ready(m.Gen, ready, manageReady("server", m.Fingerprint), nil) {
		t.Fatal("ready failed")
	}
	return m
}

func TestDedicatedManagerFounderKeyConfirmationAndPriority(t *testing.T) {
	var m DedicatedManager
	if m.Open("server", false) {
		t.Fatal("non-founder opened manager")
	}
	if !m.Open("server", true) {
		t.Fatal("founder did not open manager")
	}
	if m.Parse("not a key") || m.Phase != ManageError {
		t.Fatal("malformed key accepted")
	}
	pub := manageKey()
	if !m.Parse(b64(pub)) || m.Fingerprint != identity.Fingerprint(pub) {
		t.Fatal("key did not produce fingerprint confirmation")
	}
	got, priority, operation, ok := m.Confirm([]chain.Host{{Priority: 2}, {Priority: 9}})
	if !ok || string(got) != string(pub) || priority != 10 {
		t.Fatalf("confirmation = %v, priority %d", ok, priority)
	}
	if !m.Authorized(m.Gen, operation, nil) || m.Phase != ManagePending {
		t.Fatal("authorization did not enter pending")
	}
}

func TestDedicatedManagerMutableKeyClearsDerivedState(t *testing.T) {
	m := readyManager(t)
	m.Phase = ManageError // only editable phase after parsing
	m.KeyChanged()
	if m.Phase != ManageKey || m.pub != nil || m.Fingerprint != "" || m.Readiness != nil {
		t.Fatalf("key edit retained derived state: %+v", m)
	}
}

func TestDedicatedManagerExactReadyNonceAndRetry(t *testing.T) {
	var m DedicatedManager
	m.Open("server", true)
	pub := manageKey()
	m.Parse(b64(pub))
	_, _, authorize, _ := m.Confirm(nil)
	m.Authorized(m.Gen, authorize, nil)
	fp := m.Fingerprint
	operation, _ := m.BeginReady()
	bad := manageReady("other", fp)
	if m.Ready(m.Gen, operation, bad, nil) || m.Phase != ManageError {
		t.Fatal("accepted wrong server readiness")
	}
	m.Phase = ManagePending
	operation, _ = m.BeginReady()
	ready := manageReady("server", fp)
	if !m.Ready(m.Gen, operation, ready, nil) || m.Phase != ManageReady || m.Nonce() == "" {
		t.Fatal("did not retain exact Ready nonce")
	}
	activate, _ := m.BeginActivation()
	if !m.Activation(m.Gen, activate, &proto.DedicatedResult{Status: "retry", Message: "voice is not empty"}, nil) || m.Phase != ManageRetry {
		t.Fatal("voice retry not retained")
	}
	operation, _ = m.BeginReady()
	if !m.Ready(m.Gen, operation, ready, nil) || m.Phase != ManageReady {
		t.Fatal("retry did not recheck readiness")
	}
	activate, _ = m.BeginActivation()
	m.Activation(m.Gen, activate, nil, context.DeadlineExceeded)
	if m.Phase != ManageRecovery || m.Readiness == nil || m.Nonce() == "" {
		t.Fatal("timeout discarded recovery boundary")
	}
	m.Phase = ManageReady
	activate, _ = m.BeginActivation()
	if !m.Activation(m.Gen, activate, &proto.DedicatedResult{Status: "success", Readiness: ready}, nil) || m.Phase != ManageDone {
		t.Fatal("exact successful activation was not accepted")
	}
}

func TestDedicatedManagerSuccessRequiresCompleteReadyBoundary(t *testing.T) {
	m := readyManager(t)
	activate, _ := m.BeginActivation()
	got := *m.Readiness
	got.HistoryCount++
	if m.Activation(m.Gen, activate, &proto.DedicatedResult{Status: "success", Readiness: &got}, nil) || m.Phase != ManageRecovery {
		t.Fatal("accepted success with a changed readiness boundary")
	}
}

func TestDedicatedManagerIgnoresSupersededOperationsAndClose(t *testing.T) {
	var authorizing DedicatedManager
	authorizing.Open("server", true)
	authorizing.Parse(b64(manageKey()))
	_, _, oldAuthorize, _ := authorizing.Confirm(nil)
	authorizing.operation++ // a replacement authorization was started
	if authorizing.Authorized(authorizing.Gen, oldAuthorize, nil) {
		t.Fatal("accepted stale authorization response")
	}

	m := readyManager(t)
	oldReady := m.operation
	m.Phase = ManagePending
	newReady, _ := m.BeginReady()
	if m.Ready(m.Gen, oldReady, manageReady("server", m.Fingerprint), nil) {
		t.Fatal("accepted stale ready response")
	}
	if !m.Ready(m.Gen, newReady, manageReady("server", m.Fingerprint), nil) {
		t.Fatal("rejected newest ready response")
	}
	oldActivate, _ := m.BeginActivation()
	newActivate, _ := m.BeginActivation() // a replacement request supersedes the first
	if m.Activation(m.Gen, oldActivate, &proto.DedicatedResult{Status: "success", Readiness: m.Readiness}, nil) {
		t.Fatal("accepted stale activation response")
	}
	if !m.Activation(m.Gen, newActivate, &proto.DedicatedResult{Status: "success", Readiness: m.Readiness}, nil) {
		t.Fatal("rejected newest activation response")
	}
	old := m.Gen
	m.Close()
	if m.Authorized(old, m.operation, nil) {
		t.Fatal("accepted stale authorization after close")
	}
	if m.serverID != "" || m.Fingerprint != "" || m.Readiness != nil {
		t.Fatal("close retained dedicated state")
	}
}
