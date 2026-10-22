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
	if env.V != Version {
		t.Errorf("V = %d, want %d", env.V, Version)
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
		{"wrong version", []byte(`{"v":3,"type":"x"}`)},
		{"empty type", []byte(`{"v":2,"type":""}`)},
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

func TestRosterRoundTrip(t *testing.T) {
	in := Roster{Members: []Member{{Fingerprint: "fp", Name: "a", Roles: []string{"admin"}}}, Total: 3}
	raw, err := Encode(TypeRoster, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeRoster {
		t.Errorf("Type = %q", env.Type)
	}
	var out Roster
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

func TestDecodeRejectsV1(t *testing.T) {
	if _, err := Decode([]byte(`{"v":1,"type":"auth"}`)); err == nil {
		t.Fatal("Decode accepted v1 envelope")
	}
	if Version != 2 || LegacyVersion != 1 {
		t.Fatalf("Version=%d LegacyVersion=%d", Version, LegacyVersion)
	}
}

func TestDecodeHandshakeAcceptsV1AndV2(t *testing.T) {
	for _, v := range []string{"1", "2"} {
		if _, err := DecodeHandshake([]byte(`{"v":` + v + `,"type":"auth"}`)); err != nil {
			t.Errorf("v%s: %v", v, err)
		}
	}
	for _, raw := range []string{`{"v":3,"type":"auth"}`, `{"v":0,"type":"auth"}`, `{"v":2,"type":""}`, `nope`} {
		if _, err := DecodeHandshake([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, err := DecodeHandshake(make([]byte, MaxMessageSize+1)); err == nil {
		t.Error("accepted oversized")
	}
}

func TestEncodeVLegacy(t *testing.T) {
	raw, err := EncodeV(LegacyVersion, TypeChallenge, Challenge{Nonce: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := DecodeHandshake(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.V != 1 {
		t.Errorf("V = %d, want 1", env.V)
	}
}

func TestChainFrameRoundTrip(t *testing.T) {
	in := ChainFrame{Entries: []json.RawMessage{json.RawMessage(`{"a":"<x>"}`), json.RawMessage(`"b"`)}, More: true}
	raw, err := Encode(TypeChain, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out ChainFrame
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatal(err)
	}
	if !out.More || len(out.Entries) != 2 || string(out.Entries[0]) != `{"a":"<x>"}` || string(out.Entries[1]) != `"b"` {
		t.Errorf("round trip mismatch: %+v", out)
	}
	raw, _ = Encode(TypeChain, ChainFrame{Entries: []json.RawMessage{}})
	env, _ = Decode(raw)
	if string(env.Data) != `{"entries":[]}` {
		t.Errorf("empty frame data = %s", env.Data)
	}
	ap, _ := Encode(TypeChainAppend, ChainAppend{Entry: json.RawMessage(`{"k":1}`)})
	env, _ = Decode(ap)
	var a ChainAppend
	if err := json.Unmarshal(env.Data, &a); err != nil || string(a.Entry) != `{"k":1}` {
		t.Errorf("append: %v %s", err, a.Entry)
	}
	var ok AuthOK
	if err := json.Unmarshal([]byte(`{"server_id":"abc"}`), &ok); err != nil || ok.ServerID != "abc" {
		t.Errorf("server_id: %v %q", err, ok.ServerID)
	}
	for _, c := range []string{CodeUpdateRequired, CodeServerOutdated, CodeServerIDMismatch, CodeHostNotAuthorized, CodeBadChain, CodeChainConflict, CodeChainFull, CodeBanned} {
		if c == "" {
			t.Error("empty code")
		}
	}
	_ = HostProofReq{Nonce: []byte{1}}
	_ = HostProof{PubKey: []byte{1}, Sig: []byte{2}}
}

func TestAuthOKServerName(t *testing.T) {
	raw, err := Encode(TypeAuthOK, AuthOK{Fingerprint: "fp", ServerName: "Primos"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out AuthOK
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out.ServerName != "Primos" {
		t.Errorf("ServerName = %q", out.ServerName)
	}
	raw, err = Encode(TypeAuthOK, AuthOK{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "server_name") {
		t.Errorf("empty ServerName not omitted: %s", raw)
	}
}

func TestHistoryRoundTrip(t *testing.T) {
	in := History{Channel: "general", Before: "ab", Messages: []ChatMessage{{ID: "0f", Channel: "general", Text: "hi"}}, More: true}
	raw, err := Encode(TypeHistory, in)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != TypeHistory || TypeHistory != "history" || TypeHistoryBefore != "history_before" {
		t.Errorf("Type = %q", env.Type)
	}
	var out History
	if err := json.Unmarshal(env.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Channel != "general" || out.Before != "ab" || !out.More || len(out.Messages) != 1 ||
		out.Messages[0].ID != "0f" || out.Messages[0].Channel != "general" || out.Messages[0].Text != "hi" {
		t.Errorf("got %+v, want %+v", out, in)
	}
	hb, err := json.Marshal(HistoryBefore{Channel: "general", BeforeID: "ab"})
	if err != nil || string(hb) != `{"channel":"general","before_id":"ab"}` {
		t.Errorf("HistoryBefore = %s, %v", hb, err)
	}
}
