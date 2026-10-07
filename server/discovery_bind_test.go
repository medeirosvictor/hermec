package server

import (
	"net"
	"testing"
)

func TestDiscoveryBindHost(t *testing.T) {
	cases := []struct {
		ip   net.IP
		want string
	}{
		{net.ParseIP("127.0.0.1"), "127.0.0.1"},
		{net.ParseIP("::1"), "::1"},
		{net.ParseIP("192.168.1.5"), "192.168.1.5"},
		{net.IPv4zero, ""},
		{net.IPv6unspecified, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := discoveryBindHost(c.ip); got != c.want {
			t.Errorf("discoveryBindHost(%v) = %q, want %q", c.ip, got, c.want)
		}
	}
}

func udpLocalIP(t *testing.T, s *Server) net.IP {
	t.Helper()
	if s.udp == nil {
		t.Fatal("no udp socket")
	}
	return s.udp.LocalAddr().(*net.UDPAddr).IP
}

func TestDiscoveryBindsLoopbackForLoopbackServer(t *testing.T) {
	s := startDiscoverable(t, nil, nil) // Addr 127.0.0.1:0
	ip := udpLocalIP(t, s)
	if !ip.Equal(net.ParseIP("127.0.0.1")) || ip.IsUnspecified() {
		t.Fatalf("udp bound to %v, want 127.0.0.1", ip)
	}
}

func TestDiscoveryBindsWildcardForWildcardServer(t *testing.T) {
	s := startDiscoverable(t, func(c *Config) { c.Addr = ":0" }, nil)
	if ip := udpLocalIP(t, s); !ip.IsUnspecified() {
		t.Fatalf("udp bound to %v, want wildcard", ip)
	}
}
