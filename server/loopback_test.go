package server

import (
	"net"
	"testing"
)

// Loopback ICE must gather on loopback only, so test runs never bind a LAN
// interface (which raises the Windows Firewall prompt).
func TestLoopbackOnlyFilter(t *testing.T) {
	for ip, want := range map[string]bool{
		"127.0.0.1":    true,
		"::1":          true,
		"192.168.2.10": false,
		"10.0.0.1":     false,
	} {
		if got := loopbackIP(net.ParseIP(ip)); got != want {
			t.Errorf("loopbackIP(%s) = %v, want %v", ip, got, want)
		}
	}
}
