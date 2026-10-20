package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
)

func dialHdr(url string, h http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial(url, h)
}

func TestOriginHeaderRejected(t *testing.T) {
	url := startServer(t, chatCfg())
	c, resp, err := dialHdr(url, http.Header{"Origin": {"http://evil.example"}})
	if err == nil {
		c.Close()
		t.Fatal("upgrade with an Origin header succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %+v, want 403", resp)
	}
}

func TestNoOriginAccepted(t *testing.T) {
	url := startServer(t, chatCfg())
	c, _, err := dialHdr(url, nil)
	if err != nil {
		t.Fatalf("dial without Origin: %v", err)
	}
	c.Close()
}

func TestPreAuthCap(t *testing.T) {
	cfg := chatCfg()
	cfg.MaxPerIP = 1000 // all loopback; isolate the pre-auth cap
	url := startServer(t, cfg)
	for i := 0; i < maxPreAuth; i++ {
		c, _, err := dialHdr(url, nil)
		if err != nil {
			t.Fatalf("conn %d: %v", i+1, err)
		}
		defer c.Close()
	}
	c, _, err := dialHdr(url, nil)
	if err == nil {
		c.Close()
		t.Fatalf("conn %d (idle, pre-auth) was accepted", maxPreAuth+1)
	}
}

func TestPerIPCap(t *testing.T) {
	url := startServer(t, chatCfg()) // default per-IP cap 16
	for i := 0; i < DefaultMaxPerIP; i++ {
		c, _, err := dialHdr(url, nil)
		if err != nil {
			t.Fatalf("conn %d: %v", i+1, err)
		}
		defer c.Close()
	}
	c, _, err := dialHdr(url, nil)
	if err == nil {
		c.Close()
		t.Fatalf("conn %d from one IP was accepted", DefaultMaxPerIP+1)
	}
}

func TestPerIPSlotReleased(t *testing.T) {
	cfg := chatCfg()
	cfg.MaxPerIP = 2
	url := startServer(t, cfg)
	a, _, err := dialHdr(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := dialHdr(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	a.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _, err := dialHdr(url, nil)
		if err == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never freed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSameIPManyClientsUnderCap(t *testing.T) {
	url := startServer(t, chatCfg())
	for i := 0; i < DefaultMaxPerIP; i++ {
		c, _ := authed(t, url)
		_ = c
	}
}

func TestMaxClients(t *testing.T) {
	cfg := chatCfg()
	cfg.MaxClients = 2
	url := startServer(t, cfg)
	authed(t, url)
	authed(t, url)
	c, env := authAs(t, url, mustID(t), "")
	if env.Type != proto.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var em proto.ErrorMsg
	if err := json.Unmarshal(env.Data, &em); err != nil {
		t.Fatal(err)
	}
	if em.Code != proto.CodeServerFull {
		t.Fatalf("code = %q, want server_full", em.Code)
	}
	requireClosed(t, c)
}

func TestMaxClientsSlotFreed(t *testing.T) {
	cfg := chatCfg()
	cfg.MaxClients = 1
	url := startServer(t, cfg)
	a, _ := authed(t, url)
	a.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, env := authAs(t, url, mustID(t), "")
		if env.Type == proto.TypeAuthOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot never freed; last type %q", env.Type)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAuthBackoffSchedule(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1_000_000, 0)}
	s := New(chatCfg())
	s.clock = clk.Now
	const ip = "203.0.113.9"
	for i := 1; i <= 5; i++ {
		if d := s.noteAuthFailure(ip); d != 0 {
			t.Fatalf("failure %d delay = %v, want 0", i, d)
		}
	}
	for i, want := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60} {
		if d := s.noteAuthFailure(ip); d != want*time.Second {
			t.Fatalf("failure %d delay = %v, want %v", 6+i, d, want*time.Second)
		}
	}
	// Another address is unaffected.
	if d := s.noteAuthFailure("203.0.113.10"); d != 0 {
		t.Fatalf("other ip delay = %v", d)
	}
	// Success resets.
	s.noteAuthSuccess(ip)
	if d := s.noteAuthFailure(ip); d != 0 {
		t.Fatalf("delay after success = %v, want 0", d)
	}
	// A quiet minute (past the last delay) forgets old failures.
	for i := 0; i < 4; i++ {
		s.noteAuthFailure(ip)
	}
	if d := s.noteAuthFailure(ip); d != time.Second {
		t.Fatalf("delay = %v, want 1s", d)
	}
	clk.Advance(2*time.Minute + time.Second)
	if d := s.noteAuthFailure(ip); d != 0 {
		t.Fatalf("delay after quiet minute = %v, want 0", d)
	}
}
