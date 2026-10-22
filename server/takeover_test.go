package server

import (
	"context"
	"encoding/json"
	"sync"
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
	oldI, oldG := heartbeatInterval, heartbeatGrace
	heartbeatInterval, heartbeatGrace = 50*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { heartbeatInterval, heartbeatGrace = oldI, oldG })
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

func TestTakeoverActiveAlive(t *testing.T) {
	_, b, _, bURL, _, _, _, promotes := takeoverPair(t)
	c := warmClient(t, bURL)
	sendEnv(t, c, proto.TypeTakeover, struct{}{})
	if code := nextErrCode(t, c); code != proto.CodeActiveAlive {
		t.Fatalf("code = %q, want active_alive", code)
	}
	if b.Mode() != ModeStandby || promotes.Load() != 0 {
		t.Fatal("B promoted while A was alive")
	}
}

func TestTakeoverPromotes(t *testing.T) {
	a, b, _, bURL, _, _, _, promotes := takeoverPair(t)
	c := warmClient(t, bURL)
	killA(t, a)
	sendEnv(t, c, proto.TypeTakeover, struct{}{})
	readType(t, c, proto.TypeRoster)
	var h proto.Hosts
	_ = json.Unmarshal(readType(t, c, proto.TypeHosts).Data, &h)
	if h.Active.Name != "beta" {
		t.Fatalf("hosts active = %+v", h.Active)
	}
	waitFor(t, "promote hook", func() bool { return promotes.Load() == 1 })
	if b.Mode() != ModeActive {
		t.Fatalf("mode %v", b.Mode())
	}
	waitFor(t, "system line", func() bool {
		msgs, _ := b.hist.Page("general", "", time.Time{}, 10)
		last := len(msgs) - 1
		return last >= 0 && msgs[last].System && msgs[last].Text == "host changed to beta"
	})
}

func TestTakeoverConcurrent(t *testing.T) {
	a, _, _, bURL, _, _, _, promotes := takeoverPair(t)
	var cs []*websocket.Conn
	for i := 0; i < 5; i++ {
		cs = append(cs, warmClient(t, bURL))
	}
	killA(t, a)
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Add(1)
		go func(c *websocket.Conn) {
			defer wg.Done()
			sendEnv(t, c, proto.TypeTakeover, struct{}{})
		}(c)
	}
	wg.Wait()
	for _, c := range cs {
		for {
			var r proto.Roster
			_ = json.Unmarshal(readType(t, c, proto.TypeRoster).Data, &r)
			if len(r.Members) == 5 {
				break
			}
		}
		readType(t, c, proto.TypeHosts)
	}
	if n := promotes.Load(); n != 1 {
		t.Fatalf("promote hook ran %d times", n)
	}
}

func TestRemovedHostCannotPromote(t *testing.T) {
	_, b, aURL, bURL, ak, bk, ac, _ := takeoverPair(t)
	c := warmClient(t, bURL)
	appendAs(t, aURL, ak, ac, chain.OpHostRemove, chain.FPBody{Fingerprint: identity.Fingerprint(bk.PublicKey())})
	waitFor(t, "removal on B", func() bool { st := b.chain.State(); return !st.IsHost(bk.PublicKey()) })
	sendEnv(t, c, proto.TypeTakeover, struct{}{})
	if code := nextErrCode(t, c); code != proto.CodeHostNotAuthorized {
		t.Fatalf("code = %q", code)
	}
	if b.Mode() == ModeActive {
		t.Fatal("removed host promoted")
	}
}
