package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := ChatSend{Channel: "general", Text: "hello"}
	raw, err := Encode(TypeChatSend, in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if env.V != 1 {
		t.Errorf("V = %d, want 1", env.V)
	}
	if env.Type != TypeChatSend {
		t.Errorf("Type = %q, want %q", env.Type, TypeChatSend)
	}
	var out ChatSend
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatalf("Unmarshal data: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("payload = %+v, want %+v", out, in)
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"not json", []byte("not json")},
		{"wrong version", []byte(`{"v":2,"type":"x"}`)},
		{"empty type", []byte(`{"v":1,"type":""}`)},
		{"oversized", []byte(strings.Repeat("a", MaxMessageSize+1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.raw); err == nil {
				t.Errorf("Decode(%s) = nil error, want error", tt.name)
			}
		})
	}
}

func TestVoicePayloadsRoundTrip(t *testing.T) {
	tests := []struct {
		typ  string
		want string
		in   any
		out  any
	}{
		{TypeVoiceJoin, "voice_join", VoiceJoin{Channel: "lounge"}, new(VoiceJoin)},
		{TypeVoiceLeave, "voice_leave", VoiceLeave{}, new(VoiceLeave)},
		{TypeVoiceMute, "voice_mute", VoiceMute{Muted: true}, new(VoiceMute)},
		{TypeVoiceState, "voice_state", VoiceState{Channel: "lounge", Members: []VoiceMember{
			{Fingerprint: "k7mv-q3xp-9dfw-02hj", Name: "alice", Muted: true},
			{Fingerprint: "aaaa-bbbb-cccc-dddd", Name: "bob"},
		}}, new(VoiceState)},
		{TypeRTCOffer, "rtc_offer", RTCOffer{SDP: "v=0\r\n"}, new(RTCOffer)},
		{TypeRTCAnswer, "rtc_answer", RTCAnswer{SDP: "v=0\r\n"}, new(RTCAnswer)},
		{TypeRTCCandidate, "rtc_candidate", RTCCandidate{Candidate: `{"candidate":"x"}`}, new(RTCCandidate)},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if tt.typ != tt.want {
				t.Errorf("type constant = %q, want %q", tt.typ, tt.want)
			}
			raw, err := Encode(tt.typ, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			env, err := Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if env.Type != tt.want {
				t.Errorf("Type = %q", env.Type)
			}
			if err := json.Unmarshal(env.Data, tt.out); err != nil {
				t.Fatal(err)
			}
			if got := reflect.ValueOf(tt.out).Elem().Interface(); !reflect.DeepEqual(got, tt.in) {
				t.Errorf("got %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestAuthOKTypedChannels(t *testing.T) {
	in := AuthOK{Fingerprint: "fp", Roles: []string{"user"}, Channels: []ChannelInfo{
		{Name: "general", Type: "text"}, {Name: "lounge", Type: "voice"},
	}}
	raw, err := Encode(TypeAuthOK, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env.Data), `"channels":[{"name":"general","type":"text"}`) {
		t.Errorf("wire form = %s", env.Data)
	}
	var out AuthOK
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}
