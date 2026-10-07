package state

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

func vstate(ch string, ms ...proto.VoiceMember) client.Event {
	return client.Event{Voice: &proto.VoiceState{Channel: ch, Members: ms}}
}

func TestSetConnectedSplitsVoiceChannels(t *testing.T) {
	s := New()
	s.SetConnected([]proto.ChannelInfo{{Name: "general", Type: "text"}, {Name: "voice", Type: "voice"}}, "me")
	if len(s.Channels) != 1 || s.Channels[0] != "general" || len(s.VoiceChannels) != 1 || s.VoiceChannels[0] != "voice" {
		t.Fatalf("text=%v voice=%v", s.Channels, s.VoiceChannels)
	}
}

func TestApplyVoiceReplacesAndRemoves(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	s.Apply(vstate("voice", proto.VoiceMember{Fingerprint: "a", Name: "ana"}, proto.VoiceMember{Fingerprint: "b", Name: "bo", Muted: true}), true)
	if len(s.Voice["voice"]) != 2 || !s.Voice["voice"][1].Muted {
		t.Fatalf("got %+v", s.Voice)
	}
	s.Apply(vstate("voice", proto.VoiceMember{Fingerprint: "b", Name: "bo"}), true)
	if len(s.Voice["voice"]) != 1 || s.Voice["voice"][0].Muted {
		t.Fatalf("not replaced: %+v", s.Voice)
	}
	s.Apply(vstate("voice"), true)
	if _, ok := s.Voice["voice"]; ok {
		t.Fatalf("empty must remove key: %+v", s.Voice)
	}
}

func TestVoiceEventsDoNotTouchInCall(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	s.SetInCall("voice")
	s.Apply(vstate("voice", proto.VoiceMember{Fingerprint: "a"}), true)
	s.Apply(vstate("voice"), true)
	if s.InCall != "voice" {
		t.Fatalf("InCall=%q", s.InCall)
	}
	s.Apply(client.Event{}, false)
	if s.InCall != "" {
		t.Fatalf("disconnect must clear InCall, got %q", s.InCall)
	}
}

func TestRowsLayout(t *testing.T) {
	s := New()
	s.SetConnected([]proto.ChannelInfo{{Name: "general", Type: "text"}, {Name: "dev", Type: "text"}, {Name: "voice", Type: "voice"}}, "me")
	s.Apply(vstate("voice", proto.VoiceMember{Fingerprint: "a", Name: "ana", Muted: true}), true)
	rows := s.Rows()
	kinds := []RowKind{RowText, RowText, RowDivider, RowVoice, RowOccupant}
	if len(rows) != len(kinds) {
		t.Fatalf("rows=%+v", rows)
	}
	for i, k := range kinds {
		if rows[i].Kind != k {
			t.Fatalf("row %d kind=%v want %v", i, rows[i].Kind, k)
		}
	}
	if rows[1].Index != 1 || rows[4].Name != "ana" || !rows[4].Muted || rows[4].Channel != "voice" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestRowsNoVoiceNoDivider(t *testing.T) {
	s := New()
	s.SetConnected(chans("general"), "me")
	if r := s.Rows(); len(r) != 1 || r[0].Kind != RowText {
		t.Fatalf("rows=%+v", r)
	}
}

func TestVoiceClickAction(t *testing.T) {
	s := New()
	if s.VoiceClickAction("v") != "join" {
		t.Fatal("want join")
	}
	s.SetInCall("v")
	if s.VoiceClickAction("v") != "leave" || s.VoiceClickAction("w") != "join" {
		t.Fatal("toggle wrong")
	}
}

func TestVoiceJoinNotice(t *testing.T) {
	old := fmt.Errorf("wrap: %w", &client.ServerError{Code: "bad_request", Message: "unknown message type voice_join"})
	if got := VoiceJoinNotice(old); got != NoVoiceSupport {
		t.Fatalf("got %q", got)
	}
	if got := VoiceJoinNotice(errors.New("boom")); !strings.Contains(got, "boom") || got == NoVoiceSupport {
		t.Fatalf("got %q", got)
	}
	if got := VoiceJoinNotice(&client.ServerError{Code: "forbidden", Message: "no"}); got == NoVoiceSupport {
		t.Fatalf("forbidden must not read as unsupported: %q", got)
	}
}

func TestFormatElapsed(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0:00", 65 * time.Second: "1:05", 3725 * time.Second: "1:02:05", -time.Second: "0:00"} {
		if got := FormatElapsed(d); got != want {
			t.Errorf("%v: %q want %q", d, got, want)
		}
	}
}
