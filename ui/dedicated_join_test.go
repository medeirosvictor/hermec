package ui

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/ui/state"
)

func dedicatedTestDetails() state.DedicatedDetails {
	return state.DedicatedDetails{WSSURL: "wss://example.test/", ServerID: (chain.ID{1}).String()}
}

func TestDedicatedInvalidRetryInvalidatesProbe(t *testing.T) {
	g := addTestGame(t)
	g.add = addDedicated
	ctx, cancel := context.WithCancel(context.Background())
	g.dedicatedStop = cancel
	g.dedicatedGen = 4
	g.df.wss.setText("not a URL")
	if g.validateDedicatedSyntax() {
		t.Fatal("invalid draft started a probe")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("old probe was not cancelled")
	}
	if g.dedicatedGen != 5 {
		t.Fatalf("generation = %d, want 5", g.dedicatedGen)
	}
}

func TestPasteDedicatedInviteReplacesPasswordAndInvalidatesProbe(t *testing.T) {
	g := addTestGame(t)
	g.df.password.setText("old-password")
	ctx, cancel := context.WithCancel(context.Background())
	g.dedicatedStop = cancel
	g.dedicatedGen = 9
	link, err := state.FormatInvite(state.Invite{Addr: dedicatedTestDetails().WSSURL, ServerID: dedicatedTestDetails().ServerID})
	if err != nil {
		t.Fatal(err)
	}
	old := clipboardRead
	clipboardRead = func() (string, error) { return link, nil }
	t.Cleanup(func() { clipboardRead = old })
	g.pasteDedicatedInvite()
	if got := g.df.password.String(); got != "" {
		t.Fatalf("paste retained password %q", got)
	}
	if got := g.df.wss.String(); got != "wss://example.test/" {
		t.Fatalf("URL = %q", got)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("old probe was not cancelled")
	}
	if g.dedicatedGen <= 9 {
		t.Fatalf("generation = %d, want > 9", g.dedicatedGen)
	}
}

func TestPumpDedicatedReusesExistingSession(t *testing.T) {
	g := addTestGame(t)
	g.add = addDedicated
	g.dedicatedGen = 2
	g.dedicatedStop = func() {}
	g.dedicatedCh = make(chan dedicatedResult, 1)
	s := newSession("wss://example.test/", "wss://example.test/", "example.test")
	s.dialing = true
	g.sess[s.key] = s
	r := dedicatedResult{gen: 2, d: dedicatedTestDetails(), ack: make(chan bool, 1)}
	g.dedicatedCh <- r
	g.pumpDedicated()
	if g.sess[s.key] != s || !s.dialing {
		t.Fatal("dedicated validation replaced the existing session")
	}
	if g.curKey != s.key || g.df.status != "Already connected to this dedicated server." {
		t.Fatalf("did not reuse existing session: view=%q status=%q", g.curKey, g.df.status)
	}
	if accepted := <-r.ack; accepted {
		t.Fatal("probe client ownership was accepted for an existing session")
	}
}

func TestCancelDedicatedClearsPasswordAndInvalidatesProbe(t *testing.T) {
	g := addTestGame(t)
	g.df.password.setText("secret")
	buf := g.df.password.runes
	ctx, cancel := context.WithCancel(context.Background())
	g.dedicatedStop = cancel
	g.dedicatedGen = 7

	g.cancelDedicated()

	if got := g.df.password.String(); got != "" {
		t.Fatalf("cancel retained password %q", got)
	}
	for i, r := range buf {
		if r != 0 {
			t.Fatalf("password buffer byte %d was not cleared", i)
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("probe was not cancelled")
	}
	if g.dedicatedGen != 8 {
		t.Fatalf("generation = %d, want 8", g.dedicatedGen)
	}
}

func TestDedicatedCaretUsesMaskedBullets(t *testing.T) {
	f := field{}
	f.setText("päss秘密")
	f.back = 2
	got := dedicatedCaretPrefix("password:  ", &f)
	want := "password:  ••••"
	if got != want {
		t.Fatalf("caret prefix = %q, want %q", got, want)
	}
}

func TestDedicatedReconnectUsesPersistedPinChainAndProofKey(t *testing.T) {
	g := addTestGame(t)
	d := dedicatedTestDetails()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	g.servers = []state.ServerEntry{{URL: d.WSSURL, ServerID: d.ServerID, ChainSeq: 42, PasswordKey: hex.EncodeToString(key)}}
	s := newSession(d.WSSURL, d.WSSURL, "example.test")
	password, got := g.dialCreds(s)
	if password != "" || got.Pin != d.ServerID || got.MinChainSeq != 42 || string(got.PasswordKey) != string(key) {
		t.Fatalf("reconnect credentials = password %q, options %+v", password, got)
	}
}

func TestPumpDedicatedRejectsStaleResultWithoutClientOwnership(t *testing.T) {
	g := addTestGame(t)
	g.add = addDedicated
	g.dedicatedGen = 2
	g.dedicatedStop = func() {}
	g.dedicatedCh = make(chan dedicatedResult, 1)
	r := dedicatedResult{gen: 1, ack: make(chan bool, 1)}
	g.dedicatedCh <- r
	g.pumpDedicated()
	select {
	case accepted := <-r.ack:
		if accepted {
			t.Fatal("stale result was accepted")
		}
	default:
		t.Fatal("stale result was not acknowledged")
	}
}
