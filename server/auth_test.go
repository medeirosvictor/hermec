package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func startServer(t *testing.T, cfg Config) string {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return "ws://" + s.Addr()
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c
}

func readEnv(t *testing.T, c *websocket.Conn) proto.Envelope {
	t.Helper()
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := proto.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

func readChallenge(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	env := readEnv(t, c)
	if env.Type != proto.TypeChallenge {
		t.Fatalf("first message type = %q, want challenge", env.Type)
	}
	var ch proto.Challenge
	if err := json.Unmarshal(env.Data, &ch); err != nil {
		t.Fatalf("challenge payload: %v", err)
	}
	if len(ch.Nonce) != 32 {
		t.Fatalf("nonce length = %d, want 32", len(ch.Nonce))
	}
	return ch.Nonce
}

func sendEnv(t *testing.T, c *websocket.Conn, typ string, payload any) {
	t.Helper()
	raw, err := proto.Encode(typ, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := c.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// authAs dials url, answers the challenge as id (signing the nonce), and
// returns the connection plus the server's verdict.
func authAs(t *testing.T, url string, id *identity.Identity, password string) (*websocket.Conn, proto.Envelope) {
	t.Helper()
	c := dial(t, url)
	nonce := readChallenge(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "tester", Sig: id.Sign(nonce), Password: password,
	})
	return c, readEnv(t, c)
}

func requireAuthFailed(t *testing.T, c *websocket.Conn, env proto.Envelope) {
	t.Helper()
	if env.Type != proto.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var em proto.ErrorMsg
	if err := json.Unmarshal(env.Data, &em); err != nil {
		t.Fatal(err)
	}
	if em.Code != "auth_failed" {
		t.Fatalf("code = %q, want auth_failed", em.Code)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection still open after auth failure")
	}
}

func mustID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAuthHappyPath(t *testing.T) {
	url := startServer(t, Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general", "dev"},
	})
	id := mustID(t)
	_, env := authAs(t, url, id, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("type = %q, want auth_ok", env.Type)
	}
	var ok proto.AuthOK
	if err := json.Unmarshal(env.Data, &ok); err != nil {
		t.Fatal(err)
	}
	if want := identity.Fingerprint(id.PublicKey()); ok.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", ok.Fingerprint, want)
	}
	if want := []string{"user"}; !reflect.DeepEqual(ok.Roles, want) {
		t.Errorf("roles = %v, want %v", ok.Roles, want)
	}
	if want := []string{"general", "dev"}; !reflect.DeepEqual(ok.Channels, want) {
		t.Errorf("channels = %v, want %v", ok.Channels, want)
	}
}

func TestAuthBadSignature(t *testing.T) {
	url := startServer(t, Config{})
	id := mustID(t)
	c := dial(t, url)
	_ = readChallenge(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "x", Sig: id.Sign([]byte("not the nonce")),
	})
	requireAuthFailed(t, c, readEnv(t, c))
}

func TestAuthWrongPassword(t *testing.T) {
	url := startServer(t, Config{Password: "s3cret"})
	c, env := authAs(t, url, mustID(t), "nope")
	requireAuthFailed(t, c, env)

	_, env = authAs(t, url, mustID(t), "s3cret")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("correct password: type = %q, want auth_ok", env.Type)
	}
}

func TestAuthAllowlist(t *testing.T) {
	a, b := mustID(t), mustID(t)
	url := startServer(t, Config{AllowedKeys: []string{identity.Fingerprint(a.PublicKey())}})

	c, env := authAs(t, url, b, "")
	requireAuthFailed(t, c, env)

	_, env = authAs(t, url, a, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("allowed key: type = %q, want auth_ok", env.Type)
	}
}

func TestOversizedFrameCloses(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	_ = readChallenge(t, c)
	big := make([]byte, 70*1024)
	_ = c.WriteMessage(websocket.TextMessage, big)
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected connection to close after oversized frame")
	}

	_, env := authAs(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("server not alive after oversized frame: type = %q", env.Type)
	}
}
