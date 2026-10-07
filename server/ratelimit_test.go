package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestBucketBurstThenDeny(t *testing.T) {
	b := newBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		if !b.allow(t0) {
			t.Fatalf("message %d denied within burst", i+1)
		}
	}
	if b.allow(t0) {
		t.Fatal("message 61 allowed past burst")
	}
}

func TestBucketRefill(t *testing.T) {
	b := newBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		b.allow(t0)
	}
	// 100ms at 30/s = 3 tokens.
	now := t0.Add(100 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if !b.allow(now) {
			t.Fatalf("refilled token %d denied", i+1)
		}
	}
	if b.allow(now) {
		t.Fatal("allowed beyond refilled tokens")
	}
}

func TestBucketRefillCapsAtBurst(t *testing.T) {
	b := newBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		b.allow(t0)
	}
	now := t0.Add(time.Hour)
	for i := 0; i < 60; i++ {
		if !b.allow(now) {
			t.Fatalf("message %d denied after long idle", i+1)
		}
	}
	if b.allow(now) {
		t.Fatal("idle time banked more than burst")
	}
}

func TestBucketBackwardsClockIgnored(t *testing.T) {
	b := newBucket(30, 60, t0)
	for i := 0; i < 60; i++ {
		b.allow(t0)
	}
	if b.allow(t0.Add(-time.Hour)) {
		t.Fatal("backwards clock granted tokens")
	}
}

func TestBucketZeroDisables(t *testing.T) {
	for _, b := range []*bucket{newBucket(0, 0, t0), newBucket(0, 60, t0), newBucket(30, 0, t0)} {
		for i := 0; i < 10000; i++ {
			if !b.allow(t0) {
				t.Fatalf("disabled bucket %+v denied", b)
			}
		}
	}
}

// fakeClock is a manually advanced clock safe for use from the server.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// startRateServer starts a server with the default limits and a frozen clock.
func startRateServer(t *testing.T, cfg Config, clk *fakeClock) string {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	s.clock = clk.Now
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return "ws://" + s.Addr()
}

func rateCfg() Config {
	cfg := DefaultConfig()
	cfg.Channels = []string{"general"}
	return cfg
}

// requireClosed reads until the server closes the connection.
func requireClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("connection not closed")
			}
			return
		}
	}
}

func TestSpammerCutAtBurst(t *testing.T) {
	clk := &fakeClock{now: t0}
	url := startRateServer(t, rateCfg(), clk)
	c, _ := authed(t, url)

	// Frozen clock: exactly the burst of 60 is tolerated. Each unjoined chat
	// is answered with a not_joined error, proving it was processed.
	for i := 0; i < 60; i++ {
		sendEnv(t, c, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "x"})
	}
	for i := 0; i < 60; i++ {
		requireErrCode(t, c, "not_joined")
	}
	// Message 61 trips the limiter.
	sendEnv(t, c, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "x"})
	requireErrCode(t, c, "rate_limited")
	requireClosed(t, c)
}

func TestSignalingMessagesAreCounted(t *testing.T) {
	clk := &fakeClock{now: t0}
	cfg := rateCfg()
	cfg.MsgRate, cfg.MsgBurst = 1, 3
	url := startRateServer(t, cfg, clk)
	c, _ := authed(t, url)
	for i := 0; i < 3; i++ {
		sendEnv(t, c, proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: "{}"})
		requireErrCode(t, c, "not_joined")
	}
	sendEnv(t, c, proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: "{}"})
	requireErrCode(t, c, "rate_limited")
	requireClosed(t, c)
}

func TestSustainedRateWithinLimitSurvives(t *testing.T) {
	clk := &fakeClock{now: t0}
	url := startRateServer(t, rateCfg(), clk)
	c, _ := authed(t, url)
	// 200 messages at 20/sec of fake time (under the 30/sec sustained rate).
	for i := 0; i < 200; i++ {
		clk.Advance(50 * time.Millisecond)
		sendEnv(t, c, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "x"})
		requireErrCode(t, c, "not_joined")
	}
}

func TestInboundFloodDoesNotCutReceiver(t *testing.T) {
	clk := &fakeClock{now: t0}
	url := startRateServer(t, rateCfg(), clk)
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, a)
	readPresence(t, b)

	// Both clients drain concurrently; B counts the chat messages it gets.
	var gotB atomic.Int32
	var closedB atomic.Bool
	drain := func(c *websocket.Conn, count *atomic.Int32, closed *atomic.Bool) {
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				if closed != nil {
					closed.Store(true)
				}
				return
			}
			if env, err := proto.Decode(raw); err == nil && env.Type == proto.TypeChatMessage && count != nil {
				count.Add(1)
			}
		}
	}
	_ = a.SetReadDeadline(time.Time{})
	_ = b.SetReadDeadline(time.Time{})
	go drain(a, nil, nil)
	go drain(b, &gotB, &closedB)

	// A sends 200 chat messages (kept under its own rate via the fake
	// clock), so B receives 200: far past B's own burst of 60. B meanwhile
	// sends an occasional message of its own.
	for i := 0; i < 200; i++ {
		clk.Advance(50 * time.Millisecond)
		sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: fmt.Sprint(i)})
		if i%40 == 0 {
			sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
		}
		time.Sleep(time.Millisecond)
	}
	deadline := time.Now().Add(5 * time.Second)
	for gotB.Load() < 200 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := gotB.Load(); n != 200 {
		t.Fatalf("B received %d/200 chat messages", n)
	}
	if closedB.Load() {
		t.Fatal("B was disconnected while receiving a flood")
	}
	sendEnv(t, b, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "still here"})
}

func TestPreAuthFloodCutByOneShotAuth(t *testing.T) {
	clk := &fakeClock{now: t0}
	url := startRateServer(t, rateCfg(), clk)
	c := dial(t, url)
	readChallenge(t, c)
	for i := 0; i < 50; i++ {
		if err := c.WriteJSON(map[string]any{"type": "chat_send", "data": map[string]any{}}); err != nil {
			break // already cut
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			return // closed
		}
	}
}

func TestPreAuthBucketLimits(t *testing.T) {
	b := newBucket(preAuthMsgRate, preAuthMsgBurst, t0)
	for i := 0; i < preAuthMsgBurst; i++ {
		if !b.allow(t0) {
			t.Fatalf("message %d denied within pre-auth burst", i+1)
		}
	}
	if b.allow(t0) {
		t.Fatal("message past pre-auth burst allowed")
	}
}

// A spammer that keeps writing well past the cut still receives the
// rate_limited error (the server drains inbound before closing).
func TestSpammerStillReceivesRateLimited(t *testing.T) {
	clk := &fakeClock{now: t0}
	url := startRateServer(t, rateCfg(), clk)
	c, _ := authed(t, url)
	for i := 0; i < 300; i++ {
		if err := c.WriteJSON(map[string]any{"type": "chat_send", "data": map[string]any{"channel": "general", "text": "x"}}); err != nil {
			break
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("closed without rate_limited: %v", err)
		}
		if env, derr := proto.Decode(raw); derr == nil && env.Type == proto.TypeError && strings.Contains(string(env.Data), "rate_limited") {
			return
		}
	}
}

func TestLoadConfigMsgRate(t *testing.T) {
	cfg, err := LoadConfig(writeTOML(t, ``))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MsgRate != DefaultMsgRate || cfg.MsgBurst != DefaultMsgBurst {
		t.Errorf("defaults = %d/%d", cfg.MsgRate, cfg.MsgBurst)
	}
	cfg, err = LoadConfig(writeTOML(t, "msg_rate = 10\nmsg_burst = 20\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MsgRate != 10 || cfg.MsgBurst != 20 {
		t.Errorf("override = %d/%d", cfg.MsgRate, cfg.MsgBurst)
	}
	cfg, err = LoadConfig(writeTOML(t, "msg_rate = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MsgRate != 0 {
		t.Errorf("msg_rate = 0 not honored: %d", cfg.MsgRate)
	}
	for _, body := range []string{"msg_rate = -1\n", "msg_burst = -5\n"} {
		if _, err := LoadConfig(writeTOML(t, body)); err == nil {
			t.Errorf("expected error for %q", body)
		}
	}
}

func TestMarshalTOMLRoundTripsMsgRate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MsgRate, cfg.MsgBurst = 7, 9
	raw, err := cfg.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.MsgRate != 7 || got.MsgBurst != 9 {
		t.Errorf("round trip = %d/%d", got.MsgRate, got.MsgBurst)
	}
}
