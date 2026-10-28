package ui

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

func manageKey() []byte { return ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)) }
func manageReady(server, fp string) *proto.DedicatedReadiness {
	return &proto.DedicatedReadiness{ServerID: server, DestinationFingerprint: fp, ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, proto.DedicatedTransitionNonceSize)}
}

func readyManager(t *testing.T) *dedicatedManager {
	t.Helper()
	m := new(dedicatedManager)
	m.open("server", true)
	pub := manageKey()
	m.key.setText(base64.RawStdEncoding.EncodeToString(pub))
	if !m.parse() {
		t.Fatal("parse failed")
	}
	_, _, authorize, ok := m.confirm(nil)
	if !ok || !m.authorized(m.gen, authorize, nil) {
		t.Fatal("authorization failed")
	}
	ready, ok := m.beginReady()
	if !ok || !m.ready(m.gen, ready, manageReady("server", m.fingerprint), nil) {
		t.Fatal("ready failed")
	}
	return m
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
	got, priority, operation, ok := m.confirm([]chain.Host{{Priority: 2}, {Priority: 9}})
	if !ok || string(got) != string(pub) || priority != 10 {
		t.Fatalf("confirmation = %v, priority %d", ok, priority)
	}
	if !m.authorized(m.gen, operation, nil) || m.phase != dedicatedManagePending {
		t.Fatal("authorization did not enter pending")
	}
}

func TestDedicatedManagerMutableKeyClearsDerivedState(t *testing.T) {
	m := readyManager(t)
	m.phase = dedicatedManageError // only editable phase after parsing
	m.key.setText("replacement")
	m.keyChanged()
	if m.phase != dedicatedManageKey || m.pub != nil || m.fingerprint != "" || m.readiness != nil {
		t.Fatalf("key edit retained derived state: %+v", m)
	}
}

func TestDedicatedManagerExactReadyNonceAndRetry(t *testing.T) {
	var m dedicatedManager
	m.open("server", true)
	pub := manageKey()
	m.key.setText(base64.RawStdEncoding.EncodeToString(pub))
	m.parse()
	_, _, authorize, _ := m.confirm(nil)
	m.authorized(m.gen, authorize, nil)
	fp := m.fingerprint
	operation, _ := m.beginReady()
	bad := manageReady("other", fp)
	if m.ready(m.gen, operation, bad, nil) || m.phase != dedicatedManageError {
		t.Fatal("accepted wrong server readiness")
	}
	m.phase = dedicatedManagePending
	operation, _ = m.beginReady()
	ready := manageReady("server", fp)
	if !m.ready(m.gen, operation, ready, nil) || m.phase != dedicatedManageReady || m.nonce() == "" {
		t.Fatal("did not retain exact Ready nonce")
	}
	activate, _ := m.beginActivation()
	if !m.activation(m.gen, activate, &proto.DedicatedResult{Status: "retry", Message: "voice is not empty"}, nil) || m.phase != dedicatedManageRetry {
		t.Fatal("voice retry not retained")
	}
	operation, _ = m.beginReady()
	if !m.ready(m.gen, operation, ready, nil) || m.phase != dedicatedManageReady {
		t.Fatal("retry did not recheck readiness")
	}
	activate, _ = m.beginActivation()
	m.activation(m.gen, activate, nil, context.DeadlineExceeded)
	if m.phase != dedicatedManageRecovery || m.readiness == nil || m.nonce() == "" {
		t.Fatal("timeout discarded recovery boundary")
	}
	m.phase = dedicatedManageReady
	activate, _ = m.beginActivation()
	if !m.activation(m.gen, activate, &proto.DedicatedResult{Status: "success", Readiness: ready}, nil) || m.phase != dedicatedManageDone {
		t.Fatal("exact successful activation was not accepted")
	}
}

func TestDedicatedManagerSuccessRequiresCompleteReadyBoundary(t *testing.T) {
	m := readyManager(t)
	activate, _ := m.beginActivation()
	got := *m.readiness
	got.HistoryCount++
	if m.activation(m.gen, activate, &proto.DedicatedResult{Status: "success", Readiness: &got}, nil) || m.phase != dedicatedManageRecovery {
		t.Fatal("accepted success with a changed readiness boundary")
	}
}

func TestDedicatedManagerIgnoresSupersededOperationsAndClose(t *testing.T) {
	var authorizing dedicatedManager
	authorizing.open("server", true)
	authorizing.key.setText(base64.RawStdEncoding.EncodeToString(manageKey()))
	authorizing.parse()
	_, _, oldAuthorize, _ := authorizing.confirm(nil)
	authorizing.operation++ // a replacement authorization was started
	if authorizing.authorized(authorizing.gen, oldAuthorize, nil) {
		t.Fatal("accepted stale authorization response")
	}

	m := readyManager(t)
	oldReady := m.operation
	m.phase = dedicatedManagePending
	newReady, _ := m.beginReady()
	if m.ready(m.gen, oldReady, manageReady("server", m.fingerprint), nil) {
		t.Fatal("accepted stale ready response")
	}
	if !m.ready(m.gen, newReady, manageReady("server", m.fingerprint), nil) {
		t.Fatal("rejected newest ready response")
	}
	oldActivate, _ := m.beginActivation()
	newActivate, _ := m.beginActivation() // a replacement request supersedes the first
	if m.activation(m.gen, oldActivate, &proto.DedicatedResult{Status: "success", Readiness: m.readiness}, nil) {
		t.Fatal("accepted stale activation response")
	}
	if !m.activation(m.gen, newActivate, &proto.DedicatedResult{Status: "success", Readiness: m.readiness}, nil) {
		t.Fatal("rejected newest activation response")
	}
	old := m.gen
	m.close()
	if m.authorized(old, m.operation, nil) {
		t.Fatal("accepted stale authorization after close")
	}
	if m.serverID != "" || m.fingerprint != "" || m.readiness != nil {
		t.Fatal("close retained dedicated state")
	}
}

func TestDedicatedManageCloseAndSessionLossCancelOperation(t *testing.T) {
	for _, sessionLoss := range []bool{false, true} {
		g := testGame(t)
		cancelled := false
		g.dmStop = func() { cancelled = true }
		g.dmKey = "server"
		if sessionLoss {
			// A disconnected/non-founder session is removed by background work.
			g.sess = map[string]*session{"server": {c: &client.Client{}, st: state.New()}}
			g.tickBackground(time.Now())
		} else {
			g.closeDedicatedManage()
		}
		if !cancelled || g.dmStop != nil || g.dmKey != "" {
			t.Fatalf("sessionLoss=%v: operation was not cancelled and cleared", sessionLoss)
		}
	}
}
