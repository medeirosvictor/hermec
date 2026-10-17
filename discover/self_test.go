package discover

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestSelfTailscaleIP(t *testing.T) {
	oldLook, oldRun := lookPath, runStatus
	t.Cleanup(func() { lookPath, runStatus = oldLook, oldRun })

	lookPath = func(string) (string, error) { return "tailscale", nil }
	runStatus = func(context.Context, string) ([]byte, error) {
		return []byte(`{"Self":{"TailscaleIPs":["fd7a::1","100.101.102.103"]}}`), nil
	}
	if got := selfTailscaleIP(context.Background()); got != "100.101.102.103" {
		t.Errorf("got %q", got)
	}

	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if got := selfTailscaleIP(context.Background()); got != "" {
		t.Errorf("got %q with no CLI", got)
	}
}

func TestRouteLANIP(t *testing.T) {
	old := dialUDP
	t.Cleanup(func() { dialUDP = old })
	for _, tc := range []struct {
		name string
		addr net.Addr
		err  error
		want string
	}{
		{"private wins", &net.UDPAddr{IP: net.ParseIP("192.168.2.50")}, nil, "192.168.2.50"},
		{"public falls back", &net.UDPAddr{IP: net.ParseIP("8.1.2.3")}, nil, ""},
		{"cgnat falls back", &net.UDPAddr{IP: net.ParseIP("100.101.102.103")}, nil, ""},
		{"error falls back", nil, errors.New("no route"), ""},
	} {
		dialUDP = func() (net.Addr, error) { return tc.addr, tc.err }
		if got := routeLANIP(); got != tc.want {
			t.Errorf("%s: routeLANIP = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSelfLANIPPrefersRoute(t *testing.T) {
	old := dialUDP
	t.Cleanup(func() { dialUDP = old })
	dialUDP = func() (net.Addr, error) { return &net.UDPAddr{IP: net.ParseIP("10.9.8.7")}, nil }
	if got := selfLANIP(); got != "10.9.8.7" {
		t.Errorf("selfLANIP = %q, want the route address", got)
	}
}
