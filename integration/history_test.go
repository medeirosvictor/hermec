package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// TestHistoryReconnectNoDuplicates has a client read history on join, drop,
// miss two messages and reconnect; its state must hold each message once, in order.
func TestHistoryReconnectNoDuplicates(t *testing.T) {
	s := server.New(server.Config{
		Addr:        "127.0.0.1:0",
		Roles:       roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:    []string{"general"},
		HistoryPath: filepath.Join(t.TempDir(), "history.jsonl"),
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
	url := "ws://" + s.Addr() + "/"

	idA, idB := newID(t), newID(t)
	a := dialJoined(t, url, idA, "alice")
	send := func(from, to int) {
		for i := from; i <= to; i++ {
			if err := a.SendChat(testCtx(t), "general", fmt.Sprintf("msg %d", i)); err != nil {
				t.Fatal(err)
			}
		}
		got := 0
		for got < to-from+1 {
			waitEvent(t, a, "own chat", func(e client.Event) bool { return e.Chat != nil })
			got++
		}
	}
	send(1, 5)

	st := state.New()
	connect := func() *client.Client {
		b := dialJoined(t, url, idB, "bob")
		st.SetConnected([]proto.ChannelInfo{{Name: "general", Type: "text"}}, "")
		waitEvent(t, b, "history", func(e client.Event) bool {
			st.Apply(e, true)
			return e.History != nil
		})
		return b
	}

	b := connect()
	if got := len(st.Messages["general"]); got != 5 {
		t.Fatalf("after first join: %d messages, want 5", got)
	}
	b.Close()

	send(6, 7)
	b2 := connect()
	defer b2.Close()

	msgs := st.Messages["general"]
	if len(msgs) != 7 {
		t.Fatalf("after reconnect: %d messages, want 7: %+v", len(msgs), msgs)
	}
	seen := map[string]bool{}
	for i, m := range msgs {
		if m.ID == "" || seen[m.ID] {
			t.Errorf("message %d: id %q empty or duplicate", i, m.ID)
		}
		seen[m.ID] = true
		if want := fmt.Sprintf("msg %d", i+1); m.Text != want {
			t.Errorf("message %d = %q, want %q", i, m.Text, want)
		}
		if i > 0 && !msgs[i-1].TS.Before(m.TS) {
			t.Errorf("message %d: ts not after previous", i)
		}
	}
}
