package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func chatCfg() Config {
	return Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	}
}

func authed(t *testing.T, url string) (*websocket.Conn, *identity.Identity) {
	t.Helper()
	id := mustID(t)
	c, env := authAs(t, url, id, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth type = %q", env.Type)
	}
	return c, id
}

func readPresence(t *testing.T, c *websocket.Conn) proto.Presence {
	t.Helper()
	env := readEnv(t, c)
	if env.Type != proto.TypePresence {
		t.Fatalf("type = %q, want presence", env.Type)
	}
	var p proto.Presence
	if err := json.Unmarshal(env.Data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireNothing(t *testing.T, c *websocket.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, raw, err := c.ReadMessage(); err == nil {
		t.Fatalf("unexpected message: %s", raw)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
}

func requireErrCode(t *testing.T, c *websocket.Conn, code string) {
	t.Helper()
	env := readEnv(t, c)
	if env.Type != proto.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var em proto.ErrorMsg
	if err := json.Unmarshal(env.Data, &em); err != nil {
		t.Fatal(err)
	}
	if em.Code != code {
		t.Fatalf("code = %q, want %q", em.Code, code)
	}
}

func TestJoinBroadcastsPresence(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	if p := readPresence(t, a); len(p.Members) != 1 || p.Channel != "general" {
		t.Fatalf("presence after A join = %+v", p)
	}
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	if p := readPresence(t, a); len(p.Members) != 2 {
		t.Fatalf("A presence = %+v, want 2 members", p)
	}
	if p := readPresence(t, b); len(p.Members) != 2 {
		t.Fatalf("B presence = %+v, want 2 members", p)
	}
}

func TestChatRoundTrip(t *testing.T) {
	url := startServer(t, chatCfg())
	a, idA := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	readPresence(t, b)

	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "hi"})
	for _, c := range []*websocket.Conn{a, b} {
		env := readEnv(t, c)
		if env.Type != proto.TypeChatMessage {
			t.Fatalf("type = %q", env.Type)
		}
		var m proto.ChatMessage
		if err := json.Unmarshal(env.Data, &m); err != nil {
			t.Fatal(err)
		}
		if m.Text != "hi" || m.Channel != "general" ||
			m.From.Fingerprint != identity.Fingerprint(idA.PublicKey()) || m.TS.IsZero() {
			t.Fatalf("message = %+v", m)
		}
	}
}

func TestChatWithoutJoinRejected(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	sendEnv(t, b, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "x"})
	requireErrCode(t, b, "not_joined")
	requireNothing(t, a)
}

func TestJoinWithoutPermRejected(t *testing.T) {
	url := startServer(t, Config{
		Roles:    roles.Config{Roles: roles.Builtin()},
		Channels: []string{"general"},
	})
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	requireErrCode(t, a, "forbidden")
	requireNothing(t, a)
}

func TestLeaveUpdatesPresence(t *testing.T) {
	url := startServer(t, chatCfg())
	a, idA := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	readPresence(t, b)
	sendEnv(t, b, proto.TypeLeave, proto.Leave{Channel: "general"})
	p := readPresence(t, a)
	if len(p.Members) != 1 || p.Members[0].Fingerprint != identity.Fingerprint(idA.PublicKey()) {
		t.Fatalf("presence = %+v", p)
	}
}

func TestDisconnectUpdatesPresence(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	readPresence(t, b)
	b.Close()
	if p := readPresence(t, a); len(p.Members) != 1 {
		t.Fatalf("presence = %+v", p)
	}
}

func TestUnknownChannelRejected(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "nope"})
	requireErrCode(t, a, "bad_request")
}
