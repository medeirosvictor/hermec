package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/discover"
)

func muxClosed(t *testing.T, s *Server) bool {
	t.Helper()
	m, ok := s.mux.(interface{ IsClosed() bool })
	if !ok {
		t.Fatal("mux has no IsClosed")
	}
	return m.IsClosed()
}

// One oversized datagram (WSAEMSGSIZE on Windows) must not kill the mux or
// the discovery responder that shares its socket.
func TestMediaSurvivesOversizedDatagram(t *testing.T) {
	s := startLoopback(t, func(c *Config) { c.Discoverable = true })
	c := udpClient(t, s)
	if _, err := c.Write(make([]byte, 9000)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if muxClosed(t, s) {
		t.Fatal("mux closed by an oversized datagram")
	}
	if _, err := c.Write([]byte(discover.Magic)); err != nil {
		t.Fatal(err)
	}
	if got := readAll(c, 500*time.Millisecond); len(got) != 1 {
		t.Fatalf("announces after oversized datagram = %d, want 1", len(got))
	}
}

func TestMediaSurvivesOversizedDatagramNoDiscovery(t *testing.T) {
	s := startLoopback(t, func(c *Config) { c.Discoverable = false })
	c := udpClient(t, s)
	if _, err := c.Write(make([]byte, 9000)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if muxClosed(t, s) {
		t.Fatal("mux closed by an oversized datagram")
	}
}

type errConn struct {
	net.PacketConn
	errs int
}

func (e *errConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if e.errs > 0 {
		e.errs--
		return 0, nil, errors.New("wsarecvfrom: message too long")
	}
	return copy(p, "data"), &net.UDPAddr{}, nil
}

func TestMediaConnSwallowsReadErrors(t *testing.T) {
	c := &mediaConn{PacketConn: &errConn{errs: 5}}
	n, _, err := c.ReadFrom(make([]byte, 16))
	if err != nil || n != 4 {
		t.Fatalf("ReadFrom = %d, %v; want 4, nil", n, err)
	}
}

func TestDiscoverableFalseSharedPortNoAnnounce(t *testing.T) {
	s := startLoopback(t, func(c *Config) { c.Discoverable = false })
	c := udpClient(t, s)
	if _, err := c.Write([]byte(discover.Magic)); err != nil {
		t.Fatal(err)
	}
	if got := readAll(c, 400*time.Millisecond); len(got) != 0 {
		t.Fatalf("got %d replies, want none", len(got))
	}
	if muxClosed(t, s) {
		t.Fatal("mux closed")
	}
}

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
