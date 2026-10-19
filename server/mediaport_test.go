package server

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

func startLoopback(t *testing.T, mod func(*Config)) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	cfg.AllowLoopbackICE = true
	if mod != nil {
		mod(&cfg)
	}
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return s
}

func tcpPort(t *testing.T, s *Server) int {
	t.Helper()
	return s.ln.Addr().(*net.TCPAddr).Port
}

func TestMediaUsesListenPort(t *testing.T) {
	for _, discoverable := range []bool{false, true} {
		t.Run(fmt.Sprintf("discoverable=%v", discoverable), func(t *testing.T) {
			s := startLoopback(t, func(c *Config) { c.Discoverable = discoverable })
			if s.mux == nil {
				t.Fatal("no media mux")
			}
			pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", tcpPort(t, s)))
			if err == nil {
				pc.Close()
				t.Fatal("UDP listen port is free; media is not bound to it")
			}
		})
	}
}

func TestMediaExplicitPort(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	s := startLoopback(t, func(c *Config) { c.MediaPort = port; c.Discoverable = false })
	if s.mux == nil {
		t.Fatal("no media mux")
	}
	if p2, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		p2.Close()
		t.Fatal("media_port is not bound")
	}
}

func TestMediaPortInUse(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	for _, discoverable := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.Addr = "127.0.0.1:0"
		cfg.AllowLoopbackICE = true
		cfg.MediaPort = port
		cfg.Discoverable = discoverable
		s := New(cfg)
		err := s.Start()
		if err == nil {
			s.Shutdown(context.Background())
			t.Fatal("Start succeeded with the media port taken")
		}
		if want := fmt.Sprintf("media port %d is in use", port); !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to contain %q", err, want)
		}
	}
}

func TestLegacyUDPRangeStillWorks(t *testing.T) {
	s := startLoopback(t, func(c *Config) {
		c.UDPPortMin, c.UDPPortMax = 50000, 50100
		c.Discoverable = false
	})
	if s.mux != nil {
		t.Fatal("mux created although udp_port_min/max is set")
	}
	pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", tcpPort(t, s)))
	if err != nil {
		t.Fatalf("listen UDP port should be free: %v", err)
	}
	pc.Close()
}

func TestMediaPortConfigRoundTrip(t *testing.T) {
	c := DefaultConfig()
	c.MediaPort = 9000
	b, err := c.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "media_port = 9000") {
		t.Fatalf("marshalled config lacks media_port:\n%s", b)
	}
}
