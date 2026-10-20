package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

func (s *Server) guardCounts() (pre, ips, clients int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preAuth, len(s.ipConns), s.clients
}

func waitCounts(t *testing.T, s *Server, pre, ips, clients int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, i, c := s.guardCounts()
		if p == pre && i == ips && c == clients {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("counters pre=%d ips=%d clients=%d, want %d %d %d", p, i, c, pre, ips, clients)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAuthBlockOnWire(t *testing.T) {
	clk := &fakeClock{now: time.Unix(2_000_000, 0)}
	cfg := chatCfg()
	cfg.Password = "secret"
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	s.clock = clk.Now
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	url := "ws://" + s.Addr()

	// Five failures do not block the IP; the 6th is replied to at once too,
	// then the IP is blocked for 1 s.
	for i := 0; i < 6; i++ {
		start := time.Now()
		c, env := authAs(t, url, mustID(t), "wrong")
		requireAuthFailed(t, c, env)
		if time.Since(start) > 2*time.Second {
			t.Fatalf("failure %d reply was delayed", i+1)
		}
	}
	waitCounts(t, s, 0, 0, 0) // slots are back immediately; nothing sleeps
	_, resp, err := dialHdr(url, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("dial while blocked: err=%v resp=%+v, want 503", err, resp)
	}
	waitCounts(t, s, 0, 0, 0)

	// After the window a connection is admitted again.
	clk.Advance(1100 * time.Millisecond)
	_, env := authAs(t, url, mustID(t), "secret")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("after window: type %q", env.Type)
	}
	// Success reset the run: five more failures are free again.
	for i := 0; i < 5; i++ {
		c, env := authAs(t, url, mustID(t), "wrong")
		requireAuthFailed(t, c, env)
	}
	c2, _, err := dialHdr(url, nil)
	if err != nil {
		t.Fatalf("dial after reset: %v", err)
	}
	c2.Close()
}

func TestLoadConfigMaxPerIP(t *testing.T) {
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "s.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg, err := LoadConfig(write("max_per_ip = 40\n"))
	if err != nil || cfg.MaxPerIP != 40 {
		t.Fatalf("cfg.MaxPerIP = %d, err %v, want 40", cfg.MaxPerIP, err)
	}
	out, err := cfg.MarshalTOML()
	if err != nil || !strings.Contains(string(out), "max_per_ip = 40") {
		t.Fatalf("marshal: %v\n%s", err, out)
	}
	if cfg, _ = LoadConfig(write("")); cfg.MaxPerIP != DefaultMaxPerIP {
		t.Fatalf("default = %d", cfg.MaxPerIP)
	}
	for _, body := range []string{"max_per_ip = 0\n", "max_per_ip = -1\n"} {
		if _, err := LoadConfig(write(body)); err == nil {
			t.Errorf("%q accepted", body)
		}
	}
}
