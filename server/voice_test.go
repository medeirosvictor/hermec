package server

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func voiceCfg() Config {
	return Config{
		Roles:         roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:      []string{"general"},
		VoiceChannels: []string{"lounge", "war-room"},
	}
}

func readVoiceState(t *testing.T, c *websocket.Conn) proto.VoiceState {
	t.Helper()
	env := readEnv(t, c)
	if env.Type != proto.TypeVoiceState {
		t.Fatalf("type = %q, want voice_state", env.Type)
	}
	var v proto.VoiceState
	if err := json.Unmarshal(env.Data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVoiceJoinBroadcastsToAllConns(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url) // never joins: still sees occupancy
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	for _, c := range []*websocket.Conn{a, b} {
		if v := readVoiceState(t, c); v.Channel != "lounge" || len(v.Members) != 1 || v.Members[0].Muted {
			t.Fatalf("state = %+v", v)
		}
	}
	sendEnv(t, b, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	for _, c := range []*websocket.Conn{a, b} {
		if v := readVoiceState(t, c); len(v.Members) != 2 {
			t.Fatalf("state = %+v, want 2 members", v)
		}
	}
}

func TestVoiceJoinUnknownOrTextBadRequest(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	for _, ch := range []string{"nope", "general"} {
		sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: ch})
		requireErrCode(t, a, "bad_request")
	}
	requireNothing(t, a)
}

func TestVoiceJoinWithoutPermForbidden(t *testing.T) {
	cfg := voiceCfg()
	cfg.Roles = roles.Config{Roles: roles.Builtin()}
	url := startServer(t, cfg)
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	requireErrCode(t, a, "forbidden")
}

func TestVoiceMoveBroadcastsBothChannels(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, a)
	readVoiceState(t, b)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "war-room"})
	got := map[string]int{}
	for i := 0; i < 2; i++ {
		v := readVoiceState(t, b)
		got[v.Channel] = len(v.Members)
	}
	if got["lounge"] != 0 || got["war-room"] != 1 || len(got) != 2 {
		t.Fatalf("states = %v", got)
	}
}

func TestVoiceRejoinSameChannelIdempotent(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, a)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	if v := readVoiceState(t, a); len(v.Members) != 1 {
		t.Fatalf("state = %+v", v)
	}
}

func TestVoiceMuteRoundTrip(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceMute, proto.VoiceMute{Muted: true})
	requireErrCode(t, a, "not_joined")
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, a)
	readVoiceState(t, b)
	sendEnv(t, a, proto.TypeVoiceMute, proto.VoiceMute{Muted: true})
	if v := readVoiceState(t, b); !v.Members[0].Muted {
		t.Fatalf("state = %+v, want muted", v)
	}
	readVoiceState(t, a)
	sendEnv(t, a, proto.TypeVoiceMute, proto.VoiceMute{Muted: false})
	if v := readVoiceState(t, b); v.Members[0].Muted {
		t.Fatalf("state = %+v, want unmuted", v)
	}
}

func TestVoiceLeaveAndNoopLeave(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceLeave, proto.VoiceLeave{})
	// A no-op leave must emit nothing: the first state seen is the join's.
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	if v := readVoiceState(t, a); len(v.Members) != 1 {
		t.Fatalf("first state = %+v, want the join", v)
	}
	readVoiceState(t, b)
	sendEnv(t, a, proto.TypeVoiceLeave, proto.VoiceLeave{})
	if v := readVoiceState(t, b); len(v.Members) != 0 {
		t.Fatalf("state = %+v", v)
	}
}

func TestVoiceDisconnectRemovesAndBroadcasts(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, b)
	a.Close()
	if v := readVoiceState(t, b); v.Channel != "lounge" || len(v.Members) != 0 {
		t.Fatalf("state = %+v", v)
	}
}

func TestRTCWithoutCallNotJoined(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeRTCOffer, proto.RTCOffer{SDP: "x"})
	requireErrCode(t, a, "not_joined")
}

func TestAuthOKCarriesVoiceChannels(t *testing.T) {
	url := startServer(t, voiceCfg())
	id := mustID(t)
	_, env := authAs(t, url, id, "")
	var ok proto.AuthOK
	if err := json.Unmarshal(env.Data, &ok); err != nil {
		t.Fatal(err)
	}
	want := []proto.ChannelInfo{{Name: "general", Type: "text"}, {Name: "lounge", Type: "voice"}, {Name: "war-room", Type: "voice"}}
	if len(ok.Channels) != 3 || ok.Channels[0] != want[0] || ok.Channels[1] != want[1] || ok.Channels[2] != want[2] {
		t.Fatalf("channels = %v", ok.Channels)
	}
}

func TestVoiceSnapshotToLateJoiner(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, a)
	// B authenticates after the call began; no membership change happens.
	b, _ := authed(t, url)
	v := readVoiceState(t, b)
	if v.Channel != "lounge" || len(v.Members) != 1 {
		t.Fatalf("snapshot = %+v", v)
	}
}

// A connection that has not authenticated yet gets no voice_state; once it
// does, auth_ok comes first and the snapshot follows.
func TestVoiceNotSentBeforeAuth(t *testing.T) {
	url := startServer(t, voiceCfg())
	a, _ := authed(t, url)
	pre := dial(t, url)
	nonce := readChallenge(t, pre)
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readVoiceState(t, a) // the broadcast has happened; pre was not a target
	id := mustID(t)
	sendEnv(t, pre, proto.TypeAuth, proto.Auth{PubKey: id.PublicKey(), Name: "late", Sig: id.Sign(nonce)})
	if env := readEnv(t, pre); env.Type != proto.TypeAuthOK {
		t.Fatalf("first message = %q, want auth_ok", env.Type)
	}
	if v := readVoiceState(t, pre); len(v.Members) != 1 {
		t.Fatalf("snapshot = %+v", v)
	}
}
