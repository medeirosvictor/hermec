package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

func startServer(t *testing.T, cfg server.Config) string {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := server.New(cfg)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return "ws://" + s.Addr() + "/"
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDialAuthAndJoin(t *testing.T) {
	url := startServer(t, server.Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(testCtx(t), url, id, "alice", "")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if want := identity.Fingerprint(id.PublicKey()); c.Fingerprint() != want {
		t.Errorf("Fingerprint = %q, want %q", c.Fingerprint(), want)
	}
	if len(c.Roles()) != 1 || c.Roles()[0] != "user" {
		t.Errorf("Roles = %v", c.Roles())
	}
	if len(c.Channels()) != 1 || c.Channels()[0] != (proto.ChannelInfo{Name: "general", Type: "text"}) {
		t.Errorf("Channels = %v", c.Channels())
	}
	if err := c.Join(testCtx(t), "general"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := c.Join(testCtx(t), "nope"); err == nil || !strings.Contains(err.Error(), "bad_request") {
		t.Fatalf("Join unknown channel err = %v, want bad_request", err)
	}
}

func TestDialBadPassword(t *testing.T) {
	url := startServer(t, server.Config{Password: "s3cret"})
	id, _ := identity.Generate()
	_, err := Dial(testCtx(t), url, id, "bob", "wrong")
	if err == nil || !strings.Contains(err.Error(), "auth_failed") {
		t.Fatalf("err = %v, want auth_failed", err)
	}
}

func TestCloseIdempotentAndClosesEvents(t *testing.T) {
	url := startServer(t, server.Config{})
	id, _ := identity.Generate()
	c, err := Dial(testCtx(t), url, id, "carol", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	_ = c.Close()
	// The server pushes a roster right after auth_ok, so it may already be
	// queued before Close. Tolerate server events, but a deliberate Close
	// must not surface an Err event, and the channel must end up closed.
	timeout := time.After(5 * time.Second)
drain:
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				break drain
			}
			if ev.Err != nil {
				t.Fatalf("unexpected error event after deliberate Close: %v", ev.Err)
			}
		case <-timeout:
			t.Fatal("Events not closed after Close")
		}
	}
	if err := c.Join(testCtx(t), "general"); err == nil {
		t.Fatal("Join after Close should fail")
	}
}

func waitRoster(t *testing.T, c *Client) *proto.Roster {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatal("events closed before roster")
			}
			if ev.Roster != nil {
				return ev.Roster
			}
		case <-timeout:
			t.Fatal("timed out waiting for roster event")
		}
	}
}

func TestServerName(t *testing.T) {
	for _, want := range []string{"Primos", ""} {
		url := startServer(t, server.Config{ServerName: want})
		id, _ := identity.Generate()
		c, err := Dial(testCtx(t), url, id, "alice", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := c.ServerName(); got != want {
			t.Errorf("ServerName = %q, want %q", got, want)
		}
		c.Close()
	}
}

func TestRosterEvent(t *testing.T) {
	url := startServer(t, server.Config{})
	id1, _ := identity.Generate()
	c1, err := Dial(testCtx(t), url, id1, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	r := waitRoster(t, c1)
	if r.Total != 1 || len(r.Members) != 1 || r.Members[0].Name != "alice" {
		t.Fatalf("first roster = %+v", r)
	}
	id2, _ := identity.Generate()
	c2, err := Dial(testCtx(t), url, id2, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if r := waitRoster(t, c1); r.Total != 2 {
		t.Fatalf("second roster Total = %d, want 2", r.Total)
	}
}

func TestBadRosterBecomesErrEvent(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		send := func(typ string, v any) {
			raw, _ := proto.Encode(typ, v)
			_ = ws.WriteMessage(websocket.TextMessage, raw)
		}
		send(proto.TypeChallenge, proto.Challenge{Nonce: []byte("nonce-nonce-nonce")})
		if _, _, err := ws.ReadMessage(); err != nil {
			return
		}
		send(proto.TypeAuthOK, proto.AuthOK{})
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"v":2,"type":"roster","data":"nope"}`))
		send(proto.TypeRoster, proto.Roster{Total: 7})
		_, _, _ = ws.ReadMessage() // hold open until the client closes
	}))
	defer srv.Close()
	id, _ := identity.Generate()
	c, err := Dial(testCtx(t), "ws"+strings.TrimPrefix(srv.URL, "http"), id, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case ev := <-c.Events():
		if ev.Err == nil || !strings.Contains(ev.Err.Error(), "roster") {
			t.Fatalf("event = %+v, want roster Err", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no err event")
	}
	if r := waitRoster(t, c); r.Total != 7 {
		t.Fatalf("follow-up roster = %+v", r)
	}
}
