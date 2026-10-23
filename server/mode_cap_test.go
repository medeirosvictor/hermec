package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func errCode(e proto.Envelope) string {
	var em proto.ErrorMsg
	if e.Type != proto.TypeError || json.Unmarshal(e.Data, &em) != nil {
		return ""
	}
	return em.Code
}

func TestWarmCapRefuses(t *testing.T) {
	cfg := modeCfg()
	cfg.MaxClients = 1
	_, url := startModeServer(t, cfg)
	if _, env := authWarm(t, url, mustID(t), ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("first warm: %q", env.Type)
	}
	if _, env := authWarm(t, url, mustID(t), ""); errCode(env) != proto.CodeServerFull {
		t.Fatalf("second warm: %q %s, want server_full", env.Type, env.Data)
	}
}

// nextOutcome reads c until its roster (promoted) or an error (refused).
func nextOutcome(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	for {
		e := readEnv(t, c)
		switch e.Type {
		case proto.TypeRoster:
			return "roster"
		case proto.TypeError:
			return errCode(e)
		}
	}
}

func TestPromotePastMaxClients(t *testing.T) {
	cfg := modeCfg()
	cfg.MaxClients = 2
	s, url := startModeServer(t, cfg)
	w1, _ := authWarm(t, url, mustID(t), "")
	w2, _ := authWarm(t, url, mustID(t), "")
	if _, env := authAs(t, url, mustID(t), ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("full auth: %q", env.Type)
	}
	s.PromoteWarm()
	got := map[string]int{}
	var promoted *websocket.Conn
	for _, w := range []*websocket.Conn{w1, w2} {
		o := nextOutcome(t, w)
		got[o]++
		if o == "roster" {
			promoted = w
		}
	}
	if got["roster"] != 1 || got[proto.CodeServerFull] != 1 {
		t.Fatalf("outcomes = %v, want one roster and one server_full", got)
	}
	// Both slots are taken now.
	if _, env := authAs(t, url, mustID(t), ""); errCode(env) != proto.CodeServerFull {
		t.Fatalf("auth with a full server: %q", env.Type)
	}
	// Closing the promoted conn frees its slot.
	promoted.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, env := authAs(t, url, mustID(t), "")
		if env.Type == proto.TypeAuthOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot not released after the promoted conn closed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWarmGetsChainFrame(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)
	w, env := authWarm(t, f.url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	x := identity.Fingerprint(mustID(t).PublicKey())
	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: x, Roles: []string{"bot"}}))
	for {
		env := readEnv(t, w)
		if env.Type == proto.TypeChain {
			return
		}
		if env.Type != proto.TypeHeartbeat {
			t.Fatalf("warm conn got %q", env.Type)
		}
	}
}

func TestBanKicksWarmConn(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)
	bad := mustID(t)
	w, env := authWarm(t, f.url, bad, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	sendAppend(t, a, f.entry(t, f.founder, chain.OpBan, chain.FPBody{Fingerprint: identity.Fingerprint(bad.PublicKey())}))
	for {
		_, raw, err := w.ReadMessage()
		if err != nil {
			t.Fatal("warm conn closed without a banned error")
		}
		env, _ := proto.Decode(raw)
		if errCode(env) == proto.CodeBanned {
			return
		}
	}
}

func TestActiveHeartbeatsAuthed(t *testing.T) {
	_, url := startModeServer(t, modeCfg())
	a, _ := authAs(t, url, mustID(t), "")
	for _, typ := range collect(a, 1200*time.Millisecond) {
		if typ == proto.TypeHeartbeat {
			return
		}
	}
	t.Fatal("authed conn got no hb")
}

func TestHostFullAuthNeedsPassword(t *testing.T) {
	host := mustID(t)
	cfg := modeCfg()
	cfg.HostKey = host
	cfg.Password = "pw"
	_, url := startModeServer(t, cfg)
	c, env := authAs(t, url, host, "")
	requireAuthFailed(t, c, env)
}
