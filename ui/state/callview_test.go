package state

import (
	"reflect"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestCurrentVoiceClickOpensCallViewWithoutLeaving(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	s.SetInCall("lobby")
	s.CloseCallView()
	s.Apply(vstate("lobby", proto.VoiceMember{Fingerprint: "me", Name: "Victor"}), true)

	if got := s.VoiceClickAction("lobby"); got != "open" {
		t.Fatalf("VoiceClickAction = %q, want open", got)
	}
	s.OpenCallView()
	if !s.CallViewOpen || s.InCall != "lobby" || len(s.Voice["lobby"]) != 1 {
		t.Fatalf("open changed call state: open=%v inCall=%q voice=%+v", s.CallViewOpen, s.InCall, s.Voice)
	}
}

func TestCallParticipantsCarryCameraScreenMuteAndStableOrder(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "self")
	s.SetInCall("lobby")
	s.Apply(vstate("lobby",
		proto.VoiceMember{Fingerprint: "z", Name: "zoe", Media: []proto.MediaKind{proto.MediaScreen}},
		proto.VoiceMember{Fingerprint: "b", Name: "al\u202eic\x00e", Muted: true, Sharing: true},
		proto.VoiceMember{Fingerprint: "self", Name: "Alice", Media: []proto.MediaKind{proto.MediaCamera}},
		proto.VoiceMember{Fingerprint: "a", Name: "Alice"},
	), true)

	got := s.CallParticipants()
	want := []CallParticipant{
		{Fingerprint: "a", Name: "Alice"},
		{Fingerprint: "self", Name: "Alice", Self: true, Camera: true},
		{Fingerprint: "b", Name: "alice", Muted: true, Sharing: true},
		{Fingerprint: "z", Name: "zoe", Sharing: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("participants =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDisconnectAndLeaveCloseCallView(t *testing.T) {
	s := New()
	s.SetConnected([]proto.ChannelInfo{{Name: "one"}, {Name: "two"}}, "me")
	s.SetInCall("lobby")
	s.NextChannel()
	if !s.CallViewOpen {
		t.Fatal("text navigation closed the call view")
	}
	s.CloseCallView()
	if s.CallViewOpen || s.InCall != "lobby" {
		t.Fatalf("back left the call: open=%v inCall=%q", s.CallViewOpen, s.InCall)
	}
	s.OpenCallView()
	s.SetInCall("")
	if s.CallViewOpen {
		t.Fatal("leaving voice kept the call view open")
	}
	s.SetInCall("lobby")
	s.Apply(client.Event{}, false)
	if s.CallViewOpen {
		t.Fatal("disconnect kept the call view open")
	}
}
