package server

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/version"
)

func startDiscoverable(t *testing.T, mutate func(*Config), clock func() time.Time) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	if mutate != nil {
		mutate(&cfg)
	}
	s := New(cfg)
	if clock != nil {
		s.clock = clock
	}
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return s
}

func udpClient(t *testing.T, s *Server) *net.UDPConn {
	t.Helper()
	raddr, err := net.ResolveUDPAddr("udp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// readAll collects datagrams until a quiet period of wait.
func readAll(c *net.UDPConn, wait time.Duration) [][]byte {
	var out [][]byte
	buf := make([]byte, 2048)
	for {
		c.SetReadDeadline(time.Now().Add(wait))
		n, err := c.Read(buf)
		if err != nil {
			return out
		}
		out = append(out, append([]byte(nil), buf[:n]...))
	}
}

func TestDiscoveryProbeGetsOneAnnounce(t *testing.T) {
	s := startDiscoverable(t, func(c *Config) { c.ServerName = "Lab" }, nil)
	c := udpClient(t, s)
	if _, err := c.Write([]byte(discover.Magic)); err != nil {
		t.Fatal(err)
	}
	got := readAll(c, 300*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("replies = %d, want 1", len(got))
	}
	if len(got[0]) > 512 {
		t.Errorf("reply %d bytes", len(got[0]))
	}
	a, err := discover.ParseAnnounce(got[0])
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(s.Addr())
	if a.Name != "Lab" || a.Ver != version.Version || strconv.Itoa(a.Port) != port {
		t.Errorf("announce = %+v, addr %s", a, s.Addr())
	}
}

func TestDiscoveryDefaultName(t *testing.T) {
	s := startDiscoverable(t, nil, nil)
	c := udpClient(t, s)
	c.Write([]byte(discover.Magic))
	got := readAll(c, 300*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("replies = %d", len(got))
	}
	a, err := discover.ParseAnnounce(got[0])
	if err != nil || a.Name != s.Addr() {
		t.Errorf("name = %q, err %v, want %q", a.Name, err, s.Addr())
	}
}

func TestDiscoveryDropsGarbage(t *testing.T) {
	s := startDiscoverable(t, nil, nil)
	c := udpClient(t, s)
	cases := map[string][]byte{
		"empty":           {},
		"garbage":         []byte("hello"),
		"magic+suffix":    []byte(discover.Magic + "x"),
		"magic prefix":    []byte(discover.Magic[:len(discover.Magic)-1]),
		"lowercase":       []byte(strings.ToLower(discover.Magic)),
		"oversized":       []byte(strings.Repeat("A", 1400)),
		"oversized magic": []byte(discover.Magic + strings.Repeat(" ", 1400)),
	}
	for name, p := range cases {
		if _, err := c.Write(p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if got := readAll(c, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("got %d replies to garbage", len(got))
	}
	// Still alive afterwards.
	c.Write([]byte(discover.Magic))
	if got := readAll(c, 300*time.Millisecond); len(got) != 1 {
		t.Fatalf("after garbage: %d replies", len(got))
	}
}

func TestDiscoveryRepliesOnlyToSource(t *testing.T) {
	s := startDiscoverable(t, nil, nil)
	a := udpClient(t, s)
	b := udpClient(t, s)
	a.Write([]byte(discover.Magic))
	if got := readAll(b, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("bystander received %d datagrams", len(got))
	}
}

func TestDiscoveryRateLimit(t *testing.T) {
	now := time.Unix(1000, 0)
	s := startDiscoverable(t, nil, func() time.Time { return now })
	c := udpClient(t, s)
	for i := 0; i < 20; i++ {
		c.Write([]byte(discover.Magic))
	}
	if got := readAll(c, 400*time.Millisecond); len(got) != 10 {
		t.Fatalf("replies = %d, want 10 (burst)", len(got))
	}
}

func TestDiscoverableFalseNoSocket(t *testing.T) {
	s := startDiscoverable(t, func(c *Config) { c.Discoverable = false }, nil)
	if s.udp != nil {
		t.Fatal("udp socket created")
	}
	// The UDP port is free: we can bind it ourselves.
	_, port, _ := net.SplitHostPort(s.Addr())
	pc, err := net.ListenPacket("udp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("udp port in use: %v", err)
	}
	pc.Close()
}

func TestShutdownClosesDiscovery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(addr)
	pc, err := net.ListenPacket("udp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("udp still bound after Shutdown: %v", err)
	}
	pc.Close()
}

func TestLoadConfigDiscovery(t *testing.T) {
	cfg, err := LoadConfig(writeTOML(t, ""))
	if err != nil || !cfg.Discoverable || cfg.ServerName != "" {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = LoadConfig(writeTOML(t, "discoverable = false\nserver_name = \"Lab\"\n"))
	if err != nil || cfg.Discoverable || cfg.ServerName != "Lab" {
		t.Fatalf("explicit: %+v %v", cfg, err)
	}
	if _, err = LoadConfig(writeTOML(t, "server_name = \""+strings.Repeat("x", 65)+"\"\n")); err == nil {
		t.Error("65-char server_name accepted")
	}
	// MarshalTOML round-trips the opt-out.
	off := DefaultConfig()
	off.Discoverable = false
	b, err := off.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	back, err := LoadConfig(writeTOML(t, string(b)))
	if err != nil || back.Discoverable {
		t.Errorf("round trip: %+v %v", back, err)
	}
}
