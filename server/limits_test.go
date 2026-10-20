package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/discover"
)

func TestAuthNameCleanedAndCapped(t *testing.T) {
	url := startServer(t, chatCfg())
	cases := []struct{ in, want string }{
		{"al‮ic\x00e ", "alice"},
		{strings.Repeat("x", 1000), strings.Repeat("x", discover.MaxNameRunes)},
		{"", "anon"},
		{" ‮\x07 ", "anon"},
	}
	for _, c := range cases {
		conn := authNamed(t, url, mustID(t), c.in)
		r := readRoster(t, conn)
		if len(r.Members) == 0 {
			t.Fatalf("empty roster for %q", c.in)
		}
		found := false
		for _, m := range r.Members {
			if m.Name == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("name %q: roster %+v lacks %q", c.in, r.Members, c.want)
		}
	}
}

func TestChatTooLongRejected(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)

	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: strings.Repeat("a", 4097)})
	requireErrCode(t, a, "bad_request")

	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: strings.Repeat("a", 4096)})
	env := readEnvSkipRoster(t, a)
	if env.Type != proto.TypeChatMessage {
		t.Fatalf("type = %q, want chat_message", env.Type)
	}
}

func TestNoFrameExceedsMax(t *testing.T) {
	url := startServer(t, voiceCfg())
	lt := strings.Repeat("<", 1000)
	var conns []*websocket.Conn
	for i := 0; i < 4; i++ {
		conns = append(conns, authNamed(t, url, mustID(t), lt))
	}
	for _, c := range conns {
		sendEnv(t, c, proto.TypeJoin, proto.Join{Channel: "general"})
		sendEnv(t, c, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	}
	sendEnv(t, conns[0], proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: strings.Repeat("<", 4096)})
	sendEnv(t, conns[0], proto.TypeVoiceMute, proto.VoiceMute{Muted: true})

	seenChat := false
	for _, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
					break
				}
				t.Fatalf("connection dropped: %v", err)
			}
			if len(raw) > proto.MaxMessageSize {
				t.Errorf("frame of %d bytes exceeds MaxMessageSize", len(raw))
			}
			if env, err := proto.Decode(raw); err == nil && env.Type == proto.TypeChatMessage {
				seenChat = true
			}
		}
	}
	if !seenChat {
		t.Error("no chat_message seen")
	}
}

func TestEnqueueDropsOversizeFrame(t *testing.T) {
	c := newConn(&Server{}, nil)
	if !c.enqueue(make([]byte, proto.MaxMessageSize+1)) {
		t.Error("oversize enqueue reported failure; the conn must not be dropped for it")
	}
	if len(c.send) != 0 {
		t.Errorf("oversize frame was queued")
	}
	if !c.enqueue(make([]byte, proto.MaxMessageSize)) || len(c.send) != 1 {
		t.Error("max-size frame should queue")
	}
}

func TestMuteNoopNoBroadcast(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, a)
	readVoiceState(t, b)
	sendEnv(t, a, proto.TypeVoiceMute, proto.VoiceMute{Muted: true})
	readVoiceState(t, b)
	// Unchanged value: must not rebroadcast. The next frame is the unmute.
	sendEnv(t, a, proto.TypeVoiceMute, proto.VoiceMute{Muted: true})
	sendEnv(t, a, proto.TypeVoiceMute, proto.VoiceMute{Muted: false})
	if v := readVoiceState(t, b); v.Members[0].Muted {
		t.Fatalf("got a muted state; the no-op mute was rebroadcast: %+v", v)
	}
}

func TestRosterTrimSinglePass(t *testing.T) {
	var members []proto.Member
	for i := 0; i < proto.MaxRosterMembers; i++ {
		members = append(members, proto.Member{
			Fingerprint: strings.Repeat("f", 64),
			Name:        strings.Repeat("é", 1000), // 2 bytes each
			Roles:       []string{"user"},
		})
	}
	raw := encodeRoster(members, 500)
	if len(raw) > proto.MaxMessageSize {
		t.Fatalf("frame %d bytes over budget", len(raw))
	}
	env, err := proto.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var r proto.Roster
	if err := json.Unmarshal(env.Data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Total != 500 {
		t.Errorf("Total = %d, want 500", r.Total)
	}
	if len(r.Members) == 0 || len(r.Members) >= len(members) {
		t.Fatalf("kept %d of %d members", len(r.Members), len(members))
	}
	// One more member must not have fit.
	more, _ := proto.Encode(proto.TypeRoster, proto.Roster{Members: members[:len(r.Members)+1], Total: 500})
	if len(more) <= proto.MaxMessageSize {
		t.Errorf("trimmed too far: %d members kept but %d fit", len(r.Members), len(r.Members)+1)
	}
}
