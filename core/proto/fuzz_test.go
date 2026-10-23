package proto

import (
	"strings"
	"testing"
)

func FuzzDecode(f *testing.F) {
	if valid, err := Encode(TypeChatSend, ChatSend{Channel: "general", Text: "hi"}); err == nil {
		f.Add(valid)
	}
	for typ, payload := range map[string]any{
		TypeVoiceJoin:      VoiceJoin{Channel: "lounge"},
		TypeVoiceLeave:     VoiceLeave{},
		TypeVoiceState:     VoiceState{Channel: "lounge", Members: []VoiceMember{{Fingerprint: "fp", Name: "a", Muted: true}}},
		TypeVoiceMute:      VoiceMute{Muted: true},
		TypeRTCOffer:       RTCOffer{SDP: "v=0"},
		TypeRTCAnswer:      RTCAnswer{SDP: "v=0"},
		TypeRTCCandidate:   RTCCandidate{Candidate: "{}"},
		TypeAuthOK:         AuthOK{Fingerprint: "fp", Channels: []ChannelInfo{{Name: "g", Type: "text"}}},
		TypeMediaStart:     MediaStart{Kind: MediaCamera},
		TypeMediaStop:      MediaStop{Kind: MediaScreen},
		TypeMediaSubscribe: MediaSubscribe{Kind: MediaScreen, Fingerprint: "fp", Gen: 2},
		TypeMediaSource:    MediaSource{Kind: MediaCamera, Fingerprint: "fp", Slot: 1, Seq: 4, Gen: 2},
		TypeRoster:         Roster{Members: []Member{{Fingerprint: "fp", Name: "a", Roles: []string{"user"}}}, Total: 1},
	} {
		if b, err := Encode(typ, payload); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte("not json"))
	f.Add([]byte(`{"v":2,"type":"x"}`))
	f.Add([]byte(`{"v":1,"type":""}`))
	f.Add([]byte(strings.Repeat("a", MaxMessageSize+1)))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = Decode(raw) // property: never panics
	})
}
