package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

func mustIdent(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Fast heartbeats for every server in this package's tests, set before any
// goroutine runs (the timing is a plain variable).
func init() { server.SetHeartbeatForTest(50*time.Millisecond, 150*time.Millisecond) }

// pair is an active host A and a standby B on one chain, B linked to A.
type pair struct {
	a, b       *server.Server
	aURL, bURL string
	link       *server.Link
}

func startNamed(t *testing.T, cfg server.Config) (*server.Server, string) {
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
	return s, "ws://" + s.Addr() + "/"
}

func newPair(t *testing.T) *pair {
	t.Helper()
	ak, bk := mustIdent(t), mustIdent(t)
	cfg := server.Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	}
	g, err := chain.NewEntry(ak, nil, chain.OpGenesis, chain.GenesisBody{
		Founder: []byte(ak.PublicKey()), Salt: []byte("0123456789abcdef"),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ac, err := chain.New(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.ApplyTOML(ac, ak, cfg, []string{"channels", "roles", "default_roles"}); err != nil {
		t.Fatal(err)
	}
	for i, k := range []*identity.Identity{ak, bk} {
		head := ac.Head()
		e, err := chain.NewEntry(ak, &head, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(k.PublicKey()), Priority: i + 1}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ac.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	bc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	p := &pair{}
	acfg, bcfg := cfg, cfg
	acfg.HostKey, acfg.Chain, acfg.HostName = ak, ac, "alpha"
	bcfg.HostKey, bcfg.Chain, bcfg.HostName, bcfg.Mode = bk, bc, "beta", server.ModeStandby
	p.a, p.aURL = startNamed(t, acfg)
	p.b, p.bURL = startNamed(t, bcfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.link, err = server.DialLink(ctx, p.aURL, p.b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.link.Close)
	go p.link.Run()
	waitUntil(t, "standby role", p.link.Standby)
	p.b.SetLinkView(p.link.ActiveAlive, p.link.Standby)
	return p
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func killHost(t *testing.T, s *server.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
	time.Sleep(300 * time.Millisecond) // past the heartbeat grace
}

// waitEvent returns the first event ok accepts, or false after d.
func waitEvent(c *Client, d time.Duration, ok func(Event) bool) (Event, bool) {
	end := time.After(d)
	for {
		select {
		case ev, open := <-c.Events():
			if !open {
				return Event{}, false
			}
			if ok(ev) {
				return ev, true
			}
		case <-end:
			return Event{}, false
		}
	}
}

func TestActiveLostAfterSilence(t *testing.T) {
	p := newPair(t)
	c, err := Dial(testCtx(t), p.aURL, mustIdent(t), "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(200 * time.Millisecond) // the first hb arms the watchdog
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.a.Shutdown(ctx)
	if _, ok := waitEvent(c, 300*time.Millisecond, func(e Event) bool { return e.ActiveLost }); !ok {
		t.Fatal("no ActiveLost after the host went silent")
	}
}

// dropHB proxies url, forwarding every frame except hb: a host before v0.7.
func dropHB(t *testing.T, url string) string {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		front, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer front.Close()
		back, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			return
		}
		defer back.Close()
		go func() {
			defer back.Close()
			for {
				mt, raw, err := front.ReadMessage()
				if err != nil || back.WriteMessage(mt, raw) != nil {
					return
				}
			}
		}()
		for {
			mt, raw, err := back.ReadMessage()
			if err != nil {
				return
			}
			if env, err := proto.Decode(raw); err == nil && env.Type == proto.TypeHeartbeat {
				continue
			}
			if front.WriteMessage(mt, raw) != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
}

func TestNoActiveLostOnOldHost(t *testing.T) {
	url := dropHB(t, startServer(t, server.Config{}))
	c, err := Dial(testCtx(t), url, mustIdent(t), "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The grace is 1.5 s; wait past it.
	if _, ok := waitEvent(c, 2*time.Second, func(e Event) bool { return e.ActiveLost }); ok {
		t.Fatal("ActiveLost without any hb")
	}
}

func TestWarmTakeoverPromotes(t *testing.T) {
	p := newPair(t)
	c, err := DialWith(testCtx(t), p.bURL, mustIdent(t), "alice", "", DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.Warm() {
		t.Fatal("Warm() = false on a warm session")
	}
	killHost(t, p.a)
	if err := c.Takeover(testCtx(t)); err != nil {
		t.Fatalf("Takeover: %v", err)
	}
	if c.Warm() {
		t.Fatal("still Warm() after Takeover")
	}
	if err := c.Join(testCtx(t), "general"); err != nil {
		t.Fatalf("Join after takeover: %v", err)
	}
}

func TestTakeoverActiveAliveErr(t *testing.T) {
	p := newPair(t)
	c, err := DialWith(testCtx(t), p.bURL, mustIdent(t), "alice", "", DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = c.Takeover(testCtx(t))
	var se *ServerError
	if !errors.As(err, &se) || se.Code != proto.CodeActiveAlive {
		t.Fatalf("err = %v, want active_alive", err)
	}
	if !c.Warm() {
		t.Fatal("Warm() flipped after a refused takeover")
	}
}

func TestTakeoverRespectsContext(t *testing.T) {
	p := newPair(t)
	c, err := DialWith(testCtx(t), p.bURL, mustIdent(t), "alice", "", DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Takeover(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRollbackRefused(t *testing.T) {
	url := startServer(t, server.Config{})
	id := mustIdent(t)
	c, err := Dial(testCtx(t), url, id, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	head := c.ChainSeq()
	c.Close()
	c, err = DialWith(testCtx(t), url, id, "alice", "", DialOpts{MinChainSeq: head})
	if err != nil {
		t.Fatalf("MinChainSeq == head must pass: %v", err)
	}
	c.Close()
	_, err = DialWith(testCtx(t), url, id, "alice", "", DialOpts{MinChainSeq: head + 1})
	var se *ServerError
	if !errors.As(err, &se) || se.Code != proto.CodeBadChain || !strings.Contains(se.Message, "host served an older chain") {
		t.Fatalf("err = %v, want bad_chain older chain", err)
	}
}

func TestHostsEvent(t *testing.T) {
	url := startServer(t, server.Config{})
	c, err := Dial(testCtx(t), url, mustIdent(t), "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ev, ok := waitEvent(c, 3*time.Second, func(e Event) bool { return e.Hosts != nil }); !ok || ev.Hosts.Active.Fingerprint == "" {
		t.Fatalf("hosts event = %+v ok=%v", ev.Hosts, ok)
	}
}

func TestDialBareCloseIsServerBusy(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws, err := up.Upgrade(w, r, nil); err == nil {
			ws.UnderlyingConn().Close()
		}
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	_, err := Dial(testCtx(t), url, mustIdent(t), "alice", "")
	var se *ServerError
	if !errors.As(err, &se) || se.Code != CodeServerBusy {
		t.Fatalf("err = %v, want server_busy", err)
	}
}
