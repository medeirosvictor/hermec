package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
