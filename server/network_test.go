package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"

	"github.com/medeirosvictor/hermec/core/chain"
)

type serverRecordingNetwork struct {
	mu            sync.Mutex
	dials         int
	listens       int
	packetListens int
	pionCalls     int
	dialErr       error
	listenErr     error
	packetErr     error
	pion          transport.Net
	lastListener  *closeTrackingListener
	listenAddr    string
	packetAddr    string
}

func newServerRecordingNetwork(t *testing.T) *serverRecordingNetwork {
	t.Helper()
	p, err := stdnet.NewNet()
	if err != nil {
		t.Fatal(err)
	}
	return &serverRecordingNetwork{pion: p}
}

func (n *serverRecordingNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	n.mu.Lock()
	n.dials++
	err := n.dialErr
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func (n *serverRecordingNetwork) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	n.mu.Lock()
	n.listens++
	n.listenAddr = address
	err := n.listenErr
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	tracked := &closeTrackingListener{Listener: ln}
	n.mu.Lock()
	n.lastListener = tracked
	n.mu.Unlock()
	return tracked, nil
}

func (n *serverRecordingNetwork) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	n.mu.Lock()
	n.packetListens++
	n.packetAddr = address
	err := n.packetErr
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return (&net.ListenConfig{}).ListenPacket(ctx, network, address)
}

func (n *serverRecordingNetwork) PionNet() transport.Net {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pionCalls++
	return n.pion
}

func (n *serverRecordingNetwork) counts() (dials, listens, packets, pion int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dials, n.listens, n.packetListens, n.pionCalls
}

type closeTrackingListener struct {
	net.Listener
	mu     sync.Mutex
	closed bool
}

func (l *closeTrackingListener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Listener.Close()
}

func (l *closeTrackingListener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

func TestHostNetworkRoutesTCPUDPAndLinks(t *testing.T) {
	ac, ak, bk := twoHostChain(t)
	active, activeURL := startModeServer(t, Config{HostKey: ak, Chain: ac, Mode: ModeActive})
	_ = active
	bc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	n := newServerRecordingNetwork(t)
	standby, _ := startModeServer(t, Config{
		HostKey: bk, Chain: bc, Mode: ModeStandby, Network: n,
		Discoverable: false, AllowLoopbackICE: true,
	})
	if standby.pionNet != n.pion {
		t.Fatal("server did not retain the validated Pion network")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ProbeHost(ctx, activeURL, standby); err != nil {
		t.Fatalf("ProbeHost: %v", err)
	}
	dials, listens, packets, pion := n.counts()
	if dials != 1 || listens != 1 || packets != 1 || pion != 1 {
		t.Fatalf("provider operations = dial %d listen %d packet %d pion %d; want 1 each", dials, listens, packets, pion)
	}
}

func TestNetworkMediaUsesSelectedAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(addr, func(t *testing.T) {
			if strings.Contains(addr, "::1") {
				ln, err := net.Listen("tcp6", addr)
				if err != nil {
					t.Skipf("IPv6 loopback unavailable: %v", err)
				}
				ln.Close()
			}
			n := newServerRecordingNetwork(t)
			s := New(Config{Addr: addr, Network: n, Discoverable: false, AllowLoopbackICE: true})
			if err := s.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
			n.mu.Lock()
			packetAddr := n.packetAddr
			n.mu.Unlock()
			host, _, err := net.SplitHostPort(packetAddr)
			if err != nil {
				t.Fatalf("packet address %q: %v", packetAddr, err)
			}
			if strings.Contains(addr, "::1") {
				if net.ParseIP(host) == nil || !net.ParseIP(host).Equal(net.IPv6loopback) {
					t.Fatalf("media bound %q, want IPv6 loopback", packetAddr)
				}
			} else if host != "127.0.0.1" {
				t.Fatalf("media bound %q, want IPv4 loopback", packetAddr)
			}
		})
	}
}

func TestHostNetworkStartupFailureClosesOpenedListener(t *testing.T) {
	sentinel := errors.New("private UDP unavailable")
	n := newServerRecordingNetwork(t)
	n.packetErr = sentinel
	s := New(Config{Addr: "127.0.0.1:0", Network: n, Discoverable: false})
	err := s.Start()
	if !errors.Is(err, sentinel) {
		t.Fatalf("Start error = %v, want sentinel", err)
	}
	n.mu.Lock()
	ln := n.lastListener
	n.mu.Unlock()
	if ln == nil || !ln.isClosed() {
		t.Fatal("TCP listener was not closed after UDP startup failure")
	}
}

func TestHostNetworkRejectsUnsafeCombinationsBeforeBind(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*Config, *serverRecordingNetwork)
		want string
	}{
		{"nil pion", func(_ *Config, n *serverRecordingNetwork) { n.pion = nil }, "PionNet"},
		{"discovery", func(c *Config, _ *serverRecordingNetwork) { c.Discoverable = true }, "LAN discovery"},
		{"public ip", func(c *Config, _ *serverRecordingNetwork) { c.PublicIP = "203.0.113.1" }, "public_ip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := newServerRecordingNetwork(t)
			cfg := Config{Addr: "127.0.0.1:0", Network: n, Discoverable: false}
			tt.mod(&cfg, n)
			err := New(cfg).Start()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Start error = %v, want %q", err, tt.want)
			}
			_, listens, packets, _ := n.counts()
			if listens != 0 || packets != 0 {
				t.Fatalf("invalid provider opened sockets: listen=%d packet=%d", listens, packets)
			}
		})
	}
}

func TestHostNetworkDialFailureDoesNotFallback(t *testing.T) {
	ac, ak, bk := twoHostChain(t)
	_, activeURL := startModeServer(t, Config{HostKey: ak, Chain: ac, Mode: ModeActive})
	bc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	n := newServerRecordingNetwork(t)
	sentinel := errors.New("selected network unavailable")
	n.dialErr = sentinel
	s := New(Config{HostKey: bk, Chain: bc, Network: n})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = ProbeHost(ctx, activeURL, s)
	if !errors.Is(err, sentinel) {
		t.Fatalf("ProbeHost error = %v, want sentinel", err)
	}
	dials, _, _, _ := n.counts()
	if dials != 1 {
		t.Fatalf("provider dials = %d, want 1", dials)
	}
}
