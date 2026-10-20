package ui

import (
	"fmt"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

func failedDial(t *testing.T, err error) (*game, *session) {
	t.Helper()
	g := newTestGame(t)
	s := newSession("k", "ws://127.0.0.1:1/", "me")
	s.everUp = true
	s.dialing = true
	s.st.Phase = state.PhaseDisconnected
	g.sess["k"] = s
	g.applyDial(dialResult{key: "k", s: s, gen: s.dialGen, err: err})
	return g, s
}

func TestAuthFailedStopsAutoRetry(t *testing.T) {
	err := fmt.Errorf("client: auth rejected: %w", &client.ServerError{Code: "auth_failed", Message: "bad password"})
	g, s := failedDial(t, err)
	if !s.retryAt.IsZero() {
		t.Fatalf("auth_failed scheduled an automatic retry at %v", s.retryAt)
	}
	if !s.failed || s.lastErr == "" {
		t.Fatalf("failed=%v lastErr=%q, want a manual-retry failure with the error", s.failed, s.lastErr)
	}
	if got := lostText(s, s.retryAt); got == "" || got == "reconnecting..." {
		t.Fatalf("lostText = %q", got)
	}
	// Manual retry still works.
	g.retryNow("k")
	if !s.dialing {
		t.Fatal("manual retry did not dial")
	}
}

func TestServerFullKeepsAutoRetry(t *testing.T) {
	err := fmt.Errorf("client: auth rejected: %w", &client.ServerError{Code: proto.CodeServerFull, Message: "full"})
	_, s := failedDial(t, err)
	if s.retryAt.IsZero() {
		t.Fatal("server_full must keep retrying with backoff")
	}
}
