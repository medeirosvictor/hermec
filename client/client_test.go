package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
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
	if len(c.Channels()) != 1 || c.Channels()[0] != "general" {
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
	select {
	case _, ok := <-c.Events():
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Events not closed after Close")
	}
	if err := c.Join(testCtx(t), "general"); err == nil {
		t.Fatal("Join after Close should fail")
	}
}
