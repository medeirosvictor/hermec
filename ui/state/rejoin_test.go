package state

import (
	"errors"
	"strings"
	"testing"
)

func TestRejoinHappyPath(t *testing.T) {
	var r Rejoin
	r.Lost("lounge", true)
	if !r.Pending() {
		t.Fatal("not pending after a lost call")
	}
	a := r.Reconnected([]string{"general", "lounge"})
	if a != (RejoinAction{Kind: RejoinJoin, Channel: "lounge", Muted: true}) {
		t.Fatalf("action = %+v", a)
	}
	if r.Pending() || r.Reconnected([]string{"lounge"}).Kind != RejoinNone {
		t.Fatal("a rejoin must be consumed once")
	}
}

func TestRejoinChannelGone(t *testing.T) {
	var r Rejoin
	r.Lost("lounge", false)
	a := r.Reconnected([]string{"general"})
	if a.Kind != RejoinGone || a.Channel != "lounge" {
		t.Fatalf("action = %+v", a)
	}
	if r.Pending() {
		t.Fatal("still pending after the channel was gone")
	}
}

func TestRejoinNotInCallForgets(t *testing.T) {
	var r Rejoin
	r.Lost("lounge", false)
	r.Lost("", false) // dropped again outside a call
	if r.Pending() || r.Reconnected([]string{"lounge"}).Kind != RejoinNone {
		t.Fatal("a drop outside a call must not rejoin")
	}
}

func TestRejoinCancel(t *testing.T) {
	var r Rejoin
	r.Lost("lounge", true)
	r.Cancel()
	if r.Pending() || r.Reconnected([]string{"lounge"}).Kind != RejoinNone {
		t.Fatal("cancelled rejoin still fired")
	}
}

func TestRejoinNotices(t *testing.T) {
	if got := RejoinGoneNotice("lounge"); !strings.Contains(got, "lounge") {
		t.Fatal(got)
	}
	if got := RejoinFailNotice(errors.New("x")); !strings.HasPrefix(got, "voice not rejoined: ") {
		t.Fatal(got)
	}
}
