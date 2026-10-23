package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func modeCfg() Config {
	return Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	}
}

// startModeServer is startServer that also returns the Server.
func startModeServer(t *testing.T, cfg Config) (*Server, string) {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, "ws://" + s.Addr()
}

// authWarm is authAs with Auth.Warm set; the password is sent as given.
func authWarm(t *testing.T, url string, id *identity.Identity, password string) (*websocket.Conn, proto.Envelope) {
	t.Helper()
	c := dial(t, url)
	h := hello(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "warm", Password: password, Warm: true,
		Sig: id.Sign(proto.ClientAuthBytes(h.serverNonce, h.clientNonce, h.serverID[:])),
	})
	return c, readEnv(t, c)
}

// collect returns the types of the frames c receives within d.
func collect(c *websocket.Conn, d time.Duration) []string {
	var types []string
	end := time.Now().Add(d)
	for {
		_ = c.SetReadDeadline(end)
		_, raw, err := c.ReadMessage()
		if err != nil {
			return types
		}
		if env, err := proto.Decode(raw); err == nil {
			types = append(types, env.Type)
		}
	}
}

func TestWarmSessionGetsNoPresence(t *testing.T) {
	_, url := startModeServer(t, modeCfg())
	a, env := authAs(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("full auth: %q", env.Type)
	}
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	collect(a, 200*time.Millisecond) // roster, presence

	w, env := authWarm(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "hi"})

	hb := false
	for _, typ := range collect(w, 1200*time.Millisecond) {
		switch typ {
		case proto.TypeHeartbeat:
			hb = true
		case proto.TypeRoster, proto.TypePresence, proto.TypeChatMessage:
			t.Errorf("warm conn got %q", typ)
		}
	}
	if !hb {
		t.Error("warm conn got no hb")
	}
	for _, typ := range collect(a, 100*time.Millisecond) {
		if typ == proto.TypeRoster {
			t.Error("full client saw a roster change for a warm join")
		}
	}
}

func TestWarmNotCountedAsClient(t *testing.T) {
	cfg := modeCfg()
	cfg.MaxClients = 1
	_, url := startModeServer(t, cfg)
	if _, env := authAs(t, url, mustID(t), ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("full auth: %q", env.Type)
	}
	if _, env := authWarm(t, url, mustID(t), ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth with a full server: %q", env.Type)
	}
}

func TestStandbyRefusesFullAuth(t *testing.T) {
	cfg := modeCfg()
	cfg.Mode = ModeStandby
	_, url := startModeServer(t, cfg)
	c := dial(t, url)
	h := hello(t, c)
	id := mustID(t)
	h.sendAuth(t, c, id, "full", "")
	if env := readEnv(t, c); env.Type != proto.TypeHosts {
		t.Fatalf("first reply %q, want hosts", env.Type)
	}
	env := readEnv(t, c)
	var em proto.ErrorMsg
	if env.Type != proto.TypeError || json.Unmarshal(env.Data, &em) != nil || em.Code != proto.CodeNotActive {
		t.Fatalf("second reply %q %+v, want error not_active", env.Type, em)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection still open")
	}
}

func TestPromoteWarm(t *testing.T) {
	cfg := modeCfg()
	cfg.Mode = ModeStandby
	s, url := startModeServer(t, cfg)
	w, env := authWarm(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	// Restricted while standby.
	sendEnv(t, w, proto.TypeJoin, proto.Join{Channel: "general"})
	var em proto.ErrorMsg
	for {
		e := readEnv(t, w)
		if e.Type == proto.TypeHeartbeat {
			continue
		}
		if e.Type != proto.TypeError || json.Unmarshal(e.Data, &em) != nil || em.Code != proto.CodeNotActive {
			t.Fatalf("join on standby got %q", e.Type)
		}
		break
	}

	s.SetMode(ModeActive)
	s.chMu.Lock()
	dead := s.promoteWarmLocked()
	s.chMu.Unlock()
	dropSlow(dead)

	for e := readEnv(t, w); e.Type != proto.TypeRoster; e = readEnv(t, w) {
	}
	sendEnv(t, w, proto.TypeJoin, proto.Join{Channel: "general"})
	sendEnv(t, w, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "hello"})
	for e := readEnv(t, w); e.Type != proto.TypeChatMessage; e = readEnv(t, w) {
		if e.Type == proto.TypeError {
			t.Fatalf("promoted conn got error: %s", e.Data)
		}
	}
}

func TestRosterHasPubKey(t *testing.T) {
	_, url := startModeServer(t, modeCfg())
	id := mustID(t)
	c, env := authAs(t, url, id, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: %q", env.Type)
	}
	for {
		e := readEnv(t, c)
		if e.Type != proto.TypeRoster {
			continue
		}
		var r proto.Roster
		if err := json.Unmarshal(e.Data, &r); err != nil || len(r.Members) != 1 {
			t.Fatalf("roster %s", e.Data)
		}
		if string(r.Members[0].PubKey) != string(id.PublicKey()) {
			t.Fatal("roster member PubKey does not match the client key")
		}
		return
	}
}

func TestHostWarmAuthNoPassword(t *testing.T) {
	host := mustID(t)
	cfg := modeCfg()
	cfg.HostKey = host
	cfg.Password = "pw"
	_, url := startModeServer(t, cfg)

	if _, env := authWarm(t, url, host, ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("host warm auth without password: %q", env.Type)
	}
	c, env := authWarm(t, url, mustID(t), "")
	requireAuthFailed(t, c, env)
}
