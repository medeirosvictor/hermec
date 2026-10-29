package server

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// takeoverPair is linkPair with fast heartbeats and B linked to A as standby,
// with B's link view and promote hook wired; promotes counts hook calls.
func takeoverPair(t *testing.T) (a, b *Server, aURL, bURL string, ak, bk *identity.Identity, ac *chain.Chain, promotes *atomic.Int32) {
	t.Helper()
	t.Cleanup(SetHeartbeatForTest(50*time.Millisecond, 150*time.Millisecond))
	a, b, aURL, bURL, ak, bk, ac = linkPair(t, "pw")
	l := dialLink(t, aURL, b)
	waitFor(t, "role", func() bool { return l.Standby() })
	b.SetLinkView(l.ActiveAlive, l.Standby)
	promotes = new(atomic.Int32)
	b.SetPromoteHook(func() { promotes.Add(1) })
	return
}

// killA shuts A down and waits out the heartbeat grace.
func killA(t *testing.T, a *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.Shutdown(ctx)
	time.Sleep(heartbeatGrace + 100*time.Millisecond)
}

func warmClient(t *testing.T, bURL string) *websocket.Conn {
	t.Helper()
	c, env := authWarm(t, bURL, mustID(t), "pw")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	return c
}

func nextErrCode(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	var e proto.ErrorMsg
	_ = json.Unmarshal(readType(t, c, proto.TypeError).Data, &e)
	return e.Code
}

func countSystem(s *Server) int {
	msgs, _ := s.hist.Page("general", "", time.Time{}, 50)
	n := 0
	for _, m := range msgs {
		if m.System {
			n++
		}
	}
	return n
}

// A host that was active all along has nothing to take over: no announcement,
// no hook.
