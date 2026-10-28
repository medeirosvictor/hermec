package ui

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func manageKey() []byte { return ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)) }
func manageReady(server, fp string) *proto.DedicatedReadiness {
	return &proto.DedicatedReadiness{ServerID: server, DestinationFingerprint: fp, ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, proto.DedicatedTransitionNonceSize)}
}

func TestDedicatedManagerFounderKeyConfirmationAndPriority(t *testing.T) {
	var m dedicatedManager
	if m.open("server", false) {
		t.Fatal("non-founder opened manager")
	}
	if !m.open("server", true) {
		t.Fatal("founder did not open manager")
	}
	m.key.setText("not a key")
	if m.parse() || m.phase != dedicatedManageError {
		t.Fatal("malformed key accepted")
	}
	pub := manageKey()
	m.key.setText(base64.RawStdEncoding.EncodeToString(pub))
	if !m.parse() || m.fingerprint != identity.Fingerprint(pub) {
		t.Fatal("key did not produce fingerprint confirmation")
	}
	got, priority, ok := m.confirm([]chain.Host{{Priority: 2}, {Priority: 9}})
	if !ok || string(got) != string(pub) || priority != 10 {
		t.Fatalf("confirmation = %v, priority %d", ok, priority)
	}
	if !m.authorized(m.gen, nil) || m.phase != dedicatedManagePending {
		t.Fatal("authorization did not enter pending")
	}
}

func TestDedicatedManagerExactReadyNonceAndRetry(t *testing.T) {
	var m dedicatedManager
	m.open("server", true)
	pub := manageKey()
	m.key.setText(base64.RawStdEncoding.EncodeToString(pub))
	m.parse()
	_, _, _ = m.confirm(nil)
	m.authorized(m.gen, nil)
	fp := m.fingerprint
	bad := manageReady("other", fp)
	if m.ready(m.gen, bad, nil) || m.phase != dedicatedManageError {
		t.Fatal("accepted wrong server readiness")
	}
	m.phase = dedicatedManagePending
	ready := manageReady("server", fp)
	if !m.ready(m.gen, ready, nil) || m.phase != dedicatedManageReady || m.nonce() == "" {
		t.Fatal("did not retain exact Ready nonce")
	}
	if !m.activation(m.gen, &proto.DedicatedResult{Status: "retry", Message: "voice is not empty"}, nil) || m.phase != dedicatedManageRetry {
		t.Fatal("voice retry not retained")
	}
	if !m.ready(m.gen, ready, nil) || m.phase != dedicatedManageReady {
		t.Fatal("retry did not recheck readiness")
	}
	m.activation(m.gen, nil, context.DeadlineExceeded)
	if m.phase != dedicatedManageRecovery || m.readiness == nil || m.nonce() == "" {
		t.Fatal("timeout discarded recovery boundary")
	}
	m.phase = dedicatedManageReady
	if !m.activation(m.gen, &proto.DedicatedResult{Status: "success", Readiness: ready}, nil) || m.phase != dedicatedManageDone {
		t.Fatal("exact successful activation was not accepted")
	}
}

func TestDedicatedManagerIgnoresStaleAndClearsOnClose(t *testing.T) {
	var m dedicatedManager
	m.open("server", true)
	old := m.gen
	m.close()
	if m.authorized(old, nil) {
		t.Fatal("accepted stale authorization")
	}
	if m.serverID != "" || m.fingerprint != "" || m.readiness != nil {
		t.Fatal("close retained dedicated state")
	}
}
