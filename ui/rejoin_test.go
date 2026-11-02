package ui

import (
	"strings"
	"testing"
)

func TestRememberCallOnlyForASettledCall(t *testing.T) {
	g := newTestGame(t)
	_, a, _ := twoSessions(t, g)
	g.rememberCall(a)
	if a.rejoin.Pending() {
		t.Fatal("remembered a call that does not exist")
	}
	callOn(g, a, "voice")
	g.vc.muted = true
	g.rememberCall(a)
	if !a.rejoin.Pending() {
		t.Fatal("did not remember the call")
	}
	g.vc.busy = true // a join or leave in flight: nothing settled to restore
	g.rememberCall(a)
	if a.rejoin.Pending() {
		t.Fatal("remembered a call mid-operation")
	}
}

// The reconnect path itself: the call comes back as an automatic join on the
// same channel (the failing devices make its outcome visible without audio).
func TestRejoinVoiceStartsAnAutomaticJoin(t *testing.T) {
	g := newTestGame(t)
	_, a, _ := twoSessions(t, g)
	g.openAudio = failAudio
	callOn(g, a, "voice")
	g.vc.muted = true
	g.rememberCall(a)
	g.dropVoice() // connLost tears the call down
	g.rejoinVoice(a)
	if g.vc.joining != "voice" || a.rejoin.Pending() {
		t.Fatalf("joining %q, pending %v", g.vc.joining, a.rejoin.Pending())
	}
	pumpVoiceUntil(t, g, func() bool { return !g.vc.busy })
	if !strings.HasPrefix(a.st.Status, "voice not rejoined: ") {
		t.Fatalf("status = %q", a.st.Status)
	}
}

func TestRejoinVoiceChannelGone(t *testing.T) {
	g := newTestGame(t)
	_, a, _ := twoSessions(t, g)
	callOn(g, a, "voice")
	g.rememberCall(a)
	g.dropVoice()
	a.st.VoiceChannels = nil
	g.rejoinVoice(a)
	if g.vc.busy || !strings.Contains(a.st.Status, "no longer exists") {
		t.Fatalf("busy %v, status %q", g.vc.busy, a.st.Status)
	}
}

func TestPendingRejoinCancelledByJoinOrDisconnect(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	g.openAudio = failAudio
	callOn(g, a, "voice")
	g.rememberCall(a)
	g.dropVoice()
	g.clickVoice("voice") // joins B's voice: the user's newest choice
	if a.rejoin.Pending() {
		t.Fatal("joining another call left A's rejoin pending")
	}
	pumpVoiceUntil(t, g, func() bool { return !g.vc.busy })

	callOn(g, b, "voice")
	g.rememberCall(b)
	g.dropVoice()
	g.disconnect(b.key)
	if b.rejoin.Pending() {
		t.Fatal("disconnecting left the rejoin pending")
	}
}

func TestRejoinSkippedWhileAnotherCallIsUp(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	callOn(g, b, "voice")
	a.rejoin.Lost("voice", false)
	g.rejoinVoice(a)
	if g.vc.busy || g.callKey != b.key {
		t.Fatalf("busy %v, callKey %q: the rejoin disturbed the live call", g.vc.busy, g.callKey)
	}
}
