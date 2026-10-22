package client

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

func nextHistory(t *testing.T, c *Client) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatal("events closed before history")
			}
			if ev.History != nil {
				return ev
			}
		case <-timeout:
			t.Fatal("no history event")
		}
	}
}

func TestClientHistoryEvent(t *testing.T) {
	url := startServer(t, server.Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
	ida, _ := identity.Generate()
	a, err := Dial(testCtx(t), url, ida, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Join(testCtx(t), "general"); err != nil {
		t.Fatal(err)
	}
	for _, txt := range []string{"one", "two"} {
		if err := a.SendChat(testCtx(t), "general", txt); err != nil {
			t.Fatal(err)
		}
	}
	// Wait until both are echoed so they are stored before b joins.
	timeout := time.After(5 * time.Second)
	for seen := 0; seen < 2; {
		select {
		case ev, ok := <-a.Events():
			if !ok {
				t.Fatalf("events closed after %d of 2 echoes", seen)
			}
			if ev.Chat != nil {
				seen++
			}
		case <-timeout:
			t.Fatalf("saw %d of 2 echoes", seen)
		}
	}

	idb, _ := identity.Generate()
	b, err := Dial(testCtx(t), url, idb, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Join(testCtx(t), "general"); err != nil {
		t.Fatal(err)
	}
	ev := nextHistory(t, b)
	h := ev.History
	if h.Channel != "general" || h.Before != "" || len(h.Messages) != 2 {
		t.Fatalf("join history = %+v", h)
	}
	oldest := h.Messages[0].ID
	if oldest == "" {
		t.Fatal("history message has no id")
	}
	if err := b.HistoryBefore("general", oldest); err != nil {
		t.Fatal(err)
	}
	h2 := nextHistory(t, b).History
	if h2.Before != oldest || len(h2.Messages) != 0 {
		t.Fatalf("older page = %+v", h2)
	}

	_ = b.Close()
	if err := b.HistoryBefore("general", oldest); err != ErrClosed {
		t.Fatalf("HistoryBefore on closed client = %v, want ErrClosed", err)
	}
}
