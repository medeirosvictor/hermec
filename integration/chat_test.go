// Package integration exercises the real server with the real client.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

func startServer(t *testing.T) string {
	t.Helper()
	s := server.New(server.Config{
		Addr:     "127.0.0.1:0",
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return "ws://" + s.Addr() + "/"
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dialJoined(t *testing.T, url string, id *identity.Identity, name string) *client.Client {
	t.Helper()
	c, err := client.Dial(testCtx(t), url, id, name, "")
	if err != nil {
		t.Fatalf("Dial %s: %v", name, err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Join(testCtx(t), "general"); err != nil {
		t.Fatalf("Join %s: %v", name, err)
	}
	return c
}

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// waitEvent returns the first event from c for which pick returns true,
// failing the test on Err events, channel close or timeout.
func waitEvent(t *testing.T, c *client.Client, what string, pick func(client.Event) bool) client.Event {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatalf("events closed while waiting for %s", what)
			}
			if ev.Err != nil {
				t.Fatalf("error event while waiting for %s: %v", what, ev.Err)
			}
			if pick(ev) {
				return ev
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestTwoClientsChat(t *testing.T) {
	url := startServer(t)
	idA, idB := newID(t), newID(t)
	a := dialJoined(t, url, idA, "alice")
	b := dialJoined(t, url, idB, "bob")

	if err := a.SendChat(testCtx(t), "general", "hello bob"); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, b, "chat", func(e client.Event) bool { return e.Chat != nil })
	if got, want := ev.Chat.From.Fingerprint, identity.Fingerprint(idA.PublicKey()); got != want {
		t.Errorf("from = %q, want %q", got, want)
	}
	if ev.Chat.Text != "hello bob" || ev.Chat.Channel != "general" || ev.Chat.TS.IsZero() {
		t.Errorf("unexpected chat: %+v", ev.Chat)
	}
}

func TestDroppedClientLeavesPresence(t *testing.T) {
	url := startServer(t)
	a := dialJoined(t, url, newID(t), "alice")
	b := dialJoined(t, url, newID(t), "bob")

	waitEvent(t, a, "presence with both", func(e client.Event) bool {
		return e.Presence != nil && len(e.Presence.Members) == 2
	})
	// Close drops the socket without sending Leave.
	if err := b.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
	ev := waitEvent(t, a, "presence without bob", func(e client.Event) bool {
		return e.Presence != nil && len(e.Presence.Members) == 1
	})
	if ev.Presence.Members[0].Name != "alice" {
		t.Errorf("remaining member = %q, want alice", ev.Presence.Members[0].Name)
	}
}

// TestSameKeyTwoSessions documents the chosen server behavior: membership is
// per connection, so one identity on two connections ("two devices") is two
// sessions sharing a fingerprint, and presence lists that fingerprint twice
// (it is NOT deduplicated). Chat reaches both sessions.
func TestSameKeyTwoSessions(t *testing.T) {
	url := startServer(t)
	id := newID(t)
	fp := identity.Fingerprint(id.PublicKey())
	d1 := dialJoined(t, url, id, "phone")
	d2 := dialJoined(t, url, id, "laptop")

	ev := waitEvent(t, d1, "presence with both sessions", func(e client.Event) bool {
		return e.Presence != nil && len(e.Presence.Members) == 2
	})
	for _, m := range ev.Presence.Members {
		if m.Fingerprint != fp {
			t.Errorf("member fingerprint = %q, want %q", m.Fingerprint, fp)
		}
	}

	if err := d1.SendChat(testCtx(t), "general", "from phone"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*client.Client{"phone": d1, "laptop": d2} {
		got := waitEvent(t, c, name+" chat", func(e client.Event) bool { return e.Chat != nil })
		if got.Chat.Text != "from phone" || got.Chat.From.Fingerprint != fp {
			t.Errorf("%s got %+v", name, got.Chat)
		}
	}
}
