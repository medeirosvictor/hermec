package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func authedMedia(t *testing.T, url string) (*websocket.Conn, *identity.Identity) {
	t.Helper()
	id := mustID(t)
	c := dial(t, url)
	h := hello(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "tester",
		Sig:          id.Sign(proto.ClientAuthBytes(h.serverNonce, h.clientNonce, h.serverID[:])),
		Capabilities: []string{proto.CapabilityMediaLanesV1},
	})
	env := readEnv(t, c)
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth type = %q", env.Type)
	}
	var ok proto.AuthOK
	if err := json.Unmarshal(env.Data, &ok); err != nil {
		t.Fatal(err)
	}
	if len(ok.Capabilities) != 1 || ok.Capabilities[0] != proto.CapabilityMediaLanesV1 {
		t.Fatalf("accepted capabilities = %v", ok.Capabilities)
	}
	expectHosts(t, c)
	for env = readEnv(t, c); env.Type == proto.TypeVoiceState; env = readEnv(t, c) {
	}
	if env.Type != proto.TypeRoster {
		t.Fatalf("after auth: type = %q, want roster", env.Type)
	}
	return c, id
}

func hasMedia(m proto.VoiceMember, kind proto.MediaKind) bool {
	for _, got := range m.Media {
		if got == kind {
			return true
		}
	}
	return false
}

func mediaCount(v proto.VoiceState, kind proto.MediaKind) int {
	n := 0
	for _, m := range v.Members {
		if hasMedia(m, kind) {
			n++
		}
	}
	return n
}

func mediaOf(v proto.VoiceState, fp string, kind proto.MediaKind) bool {
	for _, m := range v.Members {
		if m.Fingerprint == fp {
			return hasMedia(m, kind)
		}
	}
	return false
}

func readMediaError(t *testing.T, c *websocket.Conn) proto.ErrorMsg {
	t.Helper()
	for {
		env := readEnvSkipRoster(t, c)
		if env.Type != proto.TypeError {
			continue
		}
		var em proto.ErrorMsg
		if err := json.Unmarshal(env.Data, &em); err != nil {
			t.Fatal(err)
		}
		return em
	}
}

func readMediaVoiceUntil(t *testing.T, c *websocket.Conn, pred func(proto.VoiceState) bool) proto.VoiceState {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		env := readEnvSkipRoster(t, c)
		if env.Type == proto.TypeHeartbeat {
			continue
		}
		if env.Type != proto.TypeVoiceState {
			t.Fatalf("type = %q, want voice_state", env.Type)
		}
		var v proto.VoiceState
		if err := json.Unmarshal(env.Data, &v); err != nil {
			t.Fatal(err)
		}
		if pred(v) {
			return v
		}
	}
}

func TestCameraLimitRejectsEleventhAndAllowsRetry(t *testing.T) {
	cfg := shareCfg()
	cfg.MaxVoice = 20
	url := startServer(t, cfg)
	observer, _ := authedMedia(t, url)
	peers := make([]*websocket.Conn, 11)
	ids := make([]*identity.Identity, 11)
	for i := range peers {
		peers[i], ids[i] = authedMedia(t, url)
		joinLounge(t, peers[i])
	}
	readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool { return len(v.Members) == 11 })

	for i := 0; i < 10; i++ {
		sendEnv(t, peers[i], proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
		want := i + 1
		readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool { return mediaCount(v, proto.MediaCamera) == want })
	}
	sendEnv(t, peers[10], proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera, Gen: 77})
	if em := readMediaError(t, peers[10]); em.Code != proto.CodeMediaFull || em.Gen != 77 {
		t.Fatalf("eleventh camera error = %+v", em)
	}

	sendEnv(t, peers[0], proto.TypeMediaStop, proto.MediaStop{Kind: proto.MediaCamera})
	readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool { return mediaCount(v, proto.MediaCamera) == 9 })
	sendEnv(t, peers[10], proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
	v := readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool { return mediaCount(v, proto.MediaCamera) == 10 })
	if mediaOf(v, fpOf(ids[0]), proto.MediaCamera) || !mediaOf(v, fpOf(ids[10]), proto.MediaCamera) {
		t.Fatalf("camera retry state = %+v", v)
	}
}

func TestMediaStateIsIdempotentAndScopedToVoiceRoom(t *testing.T) {
	url := startServer(t, shareCfg())
	a, aid := authedMedia(t, url)
	b, _ := authedMedia(t, url)
	joinLounge(t, a)
	readMediaVoiceUntil(t, a, func(v proto.VoiceState) bool { return len(v.Members) == 1 })

	for range 2 {
		sendEnv(t, a, proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
	}
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	v := readMediaVoiceUntil(t, a, func(v proto.VoiceState) bool {
		return len(v.Members) == 1 && mediaOf(v, fpOf(aid), proto.MediaCamera)
	})
	if mediaCount(v, proto.MediaCamera) != 1 {
		t.Fatalf("duplicate start state = %+v", v)
	}
	for range 2 {
		sendEnv(t, a, proto.TypeMediaStop, proto.MediaStop{Kind: proto.MediaCamera})
	}
	sendEnv(t, a, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readMediaVoiceUntil(t, a, func(v proto.VoiceState) bool {
		return len(v.Members) == 1 && !mediaOf(v, fpOf(aid), proto.MediaCamera)
	})

	sendEnv(t, a, proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaKind("unknown")})
	if em := readMediaError(t, a); em.Code != proto.CodeBadRequest {
		t.Fatalf("unknown kind error = %+v", em)
	}

	sendEnv(t, a, proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaScreen})
	sendEnv(t, b, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "war-room"})
	sendEnv(t, b, proto.TypeMediaSubscribe, proto.MediaSubscribe{Kind: proto.MediaScreen, Fingerprint: fpOf(aid), Gen: 7})
	if em := readMediaError(t, b); em.Code != proto.CodeForbidden || em.Gen != 7 {
		t.Fatalf("cross-room subscribe error = %+v", em)
	}

	sendEnv(t, b, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	readMediaVoiceUntil(t, b, func(v proto.VoiceState) bool {
		return len(v.Members) == 2 && mediaOf(v, fpOf(aid), proto.MediaScreen)
	})
	sendEnv(t, a, proto.TypeMediaStop, proto.MediaStop{Kind: proto.MediaScreen})
	readMediaVoiceUntil(t, b, func(v proto.VoiceState) bool {
		return len(v.Members) == 2 && !mediaOf(v, fpOf(aid), proto.MediaScreen)
	})
	sendEnv(t, b, proto.TypeMediaSubscribe, proto.MediaSubscribe{Kind: proto.MediaScreen, Fingerprint: fpOf(aid), Gen: 8})
	if em := readMediaError(t, b); em.Code != proto.CodeNotPublishing || em.Gen != 8 {
		t.Fatalf("inactive subscribe error = %+v", em)
	}

	legacy, _ := authed(t, url)
	sendEnv(t, legacy, proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
	if em := readMediaError(t, legacy); em.Code != proto.CodeBadRequest {
		t.Fatalf("unnegotiated media error = %+v", em)
	}
}

func TestLegacyShareMapsToScreenLane(t *testing.T) {
	url := startServer(t, shareCfg())
	legacy, lid := authed(t, url)
	observer, _ := authedMedia(t, url)
	joinLounge(t, legacy)
	readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool { return len(v.Members) == 1 })
	sendEnv(t, legacy, proto.TypeShareStart, proto.ShareStart{})
	v := readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool {
		return sharingOf(v, fpOf(lid))
	})
	if !mediaOf(v, fpOf(lid), proto.MediaScreen) {
		t.Fatalf("legacy share missing screen lane: %+v", v)
	}
	sendEnv(t, legacy, proto.TypeShareStop, proto.ShareStop{})
	v = readMediaVoiceUntil(t, observer, func(v proto.VoiceState) bool { return !sharingOf(v, fpOf(lid)) })
	if mediaOf(v, fpOf(lid), proto.MediaScreen) {
		t.Fatalf("legacy stop kept screen lane: %+v", v)
	}
}
