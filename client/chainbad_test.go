package client

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// A live chain frame that does not link to the client's copy ends the
// connection: bad_chain is the terminal event and a pending ChainAppend fails
// with ErrClosed rather than waiting for its answer.
func TestLiveBadChainFrameIsTerminal(t *testing.T) {
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHost{t: t, key: key, ch: newChain(t, key, 0)}
	f.listen(func(ws *websocket.Conn) {
		a := f.handshake(ws)
		if a == nil {
			return
		}
		raw, _ := proto.Encode(proto.TypeAuthOK, proto.AuthOK{
			Fingerprint: identity.Fingerprint(a.PubKey), ServerID: f.serverID(),
		})
		_ = ws.WriteMessage(websocket.TextMessage, raw)
		// Wait for the client's chain_append, then answer with an entry that
		// skips the client's head: it links to genesis, not to host_add.
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			env, err := proto.Decode(raw)
			if err != nil || env.Type != proto.TypeChainAppend {
				continue
			}
			genesis := f.ch.Entries()[0]
			bad, err := chain.NewEntry(key, &genesis, chain.OpConfig, chain.ConfigBody{Name: "forked"}, time.Now())
			if err != nil {
				t.Errorf("NewEntry: %v", err)
				return
			}
			eraw, _ := json.Marshal(bad)
			out, _ := proto.Encode(proto.TypeChain, proto.ChainFrame{Entries: []json.RawMessage{eraw}})
			_ = ws.WriteMessage(websocket.TextMessage, out)
			_, _, _ = ws.ReadMessage() // hold open until the client closes
			return
		}
	})

	c, err := Dial(testCtx(t), f.url, key, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	got := make(chan error, 1)
	go func() {
		got <- c.ChainAppend(testCtx(t), chain.OpGrant, chain.GrantBody{Fingerprint: "x", Roles: []string{"bot"}})
	}()
	select {
	case err := <-got:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("ChainAppend = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ChainAppend still pending after a bad live chain frame")
	}

	var last Event
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				break loop
			}
			last = ev
		case <-timeout:
			t.Fatal("events never closed")
		}
	}
	var se *ServerError
	if !errors.As(last.Err, &se) || se.Code != proto.CodeBadChain {
		t.Fatalf("last event = %+v, want a bad_chain error", last)
	}
	if h := c.Chain().Head().Seq; h != 1 {
		t.Errorf("local head = %d, want 1 (bad entry not applied)", h)
	}
}
