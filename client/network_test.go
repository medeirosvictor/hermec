package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
)

type recordingNetwork struct {
	mu        sync.Mutex
	dials     int
	pionCalls int
	dialErr   error
	pion      transport.Net
	dial      func(context.Context, string, string) (net.Conn, error)
}

func (n *recordingNetwork) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	n.mu.Lock()
	n.dials++
	n.mu.Unlock()
	if n.dial != nil {
		return n.dial(ctx, network, address)
	}
	if n.dialErr != nil {
		return nil, n.dialErr
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
func (n *recordingNetwork) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(ctx, network, address)
}
func (n *recordingNetwork) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return (&net.ListenConfig{}).ListenPacket(ctx, network, address)
}
func (n *recordingNetwork) PionNet() transport.Net {
	n.mu.Lock()
	n.pionCalls++
	n.mu.Unlock()
	return n.pion
}
func (n *recordingNetwork) dialCount() int { n.mu.Lock(); defer n.mu.Unlock(); return n.dials }
func (n *recordingNetwork) pionCount() int { n.mu.Lock(); defer n.mu.Unlock(); return n.pionCalls }

func testRecordingNetwork(t *testing.T) *recordingNetwork {
	t.Helper()
	p, err := stdnet.NewNet()
	if err != nil {
		t.Fatal(err)
	}
	return &recordingNetwork{pion: p}
}

func TestDialWithNetworkUsesSelectedProvider(t *testing.T) {
	url := startServer(t, server.Config{})
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Dial(testCtx(t), url, id, "plain", "")
	if err != nil {
		t.Fatal(err)
	}
	pin := plain.ServerID()
	plain.Close()
	n := testRecordingNetwork(t)
	c, err := DialWithNetwork(testCtx(t), url, id, "private", "", DialOpts{Pin: pin}, n)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ServerID() != pin {
		t.Fatalf("ServerID = %q, want %q", c.ServerID(), pin)
	}
	if n.dialCount() != 1 {
		t.Fatalf("provider dials = %d, want 1", n.dialCount())
	}
}

func TestDialWithNetworkIgnoresGlobalRoutes(t *testing.T) {
	for _, route := range []string{"tls", "proxy"} {
		t.Run(route, func(t *testing.T) {
			original := websocket.DefaultDialer
			copy := *original
			websocket.DefaultDialer = &copy
			t.Cleanup(func() { websocket.DefaultDialer = original })
			bypass := errors.New("global route used")
			if route == "tls" {
				copy.NetDialTLSContext = func(context.Context, string, string) (net.Conn, error) {
					return nil, bypass
				}
			} else {
				copy.Proxy = func(*http.Request) (*url.URL, error) { return nil, bypass }
			}
			n := testRecordingNetwork(t)
			unavailable := errors.New("selected network unavailable")
			n.dialErr = unavailable
			c, err := DialWithNetwork(testCtx(t), "wss://127.0.0.1:17697/", nil, "alice", "", DialOpts{}, n)
			if c != nil || !errors.Is(err, unavailable) || n.dialCount() != 1 {
				t.Fatalf("client=%v err=%v provider dials=%d; want selected network error", c, err, n.dialCount())
			}
		})
	}
}

func TestPrivateProviderFailureDoesNotFallback(t *testing.T) {
	url := startServer(t, server.Config{})
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n := testRecordingNetwork(t)
	sentinel := errors.New("private node unavailable")
	n.dialErr = sentinel
	c, err := DialWithNetwork(testCtx(t), url, id, "alice", "", DialOpts{}, n)
	if c != nil || !errors.Is(err, sentinel) {
		t.Fatalf("client = %v, err = %v, want sentinel", c, err)
	}
	if n.dialCount() != 1 {
		t.Fatalf("provider dials = %d, want 1", n.dialCount())
	}
	n.pion = nil
	c, err = DialWithNetwork(testCtx(t), url, id, "alice", "", DialOpts{}, n)
	if c != nil || err == nil || !strings.Contains(err.Error(), "PionNet") {
		t.Fatalf("invalid provider client = %v, err = %v", c, err)
	}
	if n.dialCount() != 1 {
		t.Fatal("invalid provider attempted signaling dial")
	}
}

func TestNetworkActiveAndWarmPeersUseProvider(t *testing.T) {
	p := newPair(t)
	n := testRecordingNetwork(t)
	udp := &observedPionNet{Net: n.pion}
	n.pion = udp
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	active, err := DialWithNetwork(testCtx(t), p.aURL, id, "active", "", DialOpts{}, n)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	active.SetLoopbackICE(true)
	go drain(active)
	warm, err := DialWithNetwork(testCtx(t), p.bURL, id, "warm", "", DialOpts{Warm: true}, n)
	if err != nil {
		t.Fatal(err)
	}
	defer warm.Close()
	warm.SetLoopbackICE(true)
	go drain(warm)
	// The provider may stop returning a network after shutdown. Both clients
	// must retain the validated instance rather than silently select OS sockets.
	n.pion = nil
	if err := active.JoinVoice(testCtx(t), warmVoiceCh, &markerSource{marker: 1}, newRecSink()); err != nil {
		t.Fatal(err)
	}
	beforeWarm, _ := udp.counts()
	if beforeWarm == 0 {
		t.Fatal("active peer bypassed provider UDP sockets")
	}
	if err := warm.OpenWarmPeer(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if afterWarm, _ := udp.counts(); afterWarm <= beforeWarm {
		t.Fatal("warm peer bypassed provider UDP sockets")
	}
}

func TestDialWithNetworkCancellationClosesLateSocket(t *testing.T) {
	url := startServer(t, server.Config{})
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n := testRecordingNetwork(t)
	started := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{})
	n.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		close(started)
		<-release
		conn, err := (&net.Dialer{}).DialContext(context.Background(), network, address)
		if err != nil {
			return nil, err
		}
		return &observedConn{Conn: conn, closed: closed}, nil
	}
	result := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c, err := DialWithNetwork(ctx, url, id, "late", "", DialOpts{}, n)
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	<-started
	// A dial completing after cancellation must close its late socket.
	cancel()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dial stalled")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("late socket not closed")
	}
}

type observedConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *observedConn) Close() error { c.once.Do(func() { close(c.closed) }); return c.Conn.Close() }

// This fixture wraps real loopback UDP sockets and can delay their creation.
type observedPionNet struct {
	transport.Net
	mu             sync.Mutex
	opened, closed int
	started        chan struct{}
	release        chan struct{}
	gate           sync.Once
}

func (n *observedPionNet) ListenUDP(network string, address *net.UDPAddr) (transport.UDPConn, error) {
	n.gate.Do(func() {
		if n.started != nil {
			close(n.started)
			<-n.release
		}
	})
	conn, err := n.Net.ListenUDP(network, address)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.opened++
	n.mu.Unlock()
	return &observedUDPConn{UDPConn: conn, owner: n}, nil
}

func (n *observedPionNet) counts() (int, int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.opened, n.closed
}

type observedUDPConn struct {
	transport.UDPConn
	owner *observedPionNet
	once  sync.Once
}

func (c *observedUDPConn) Close() error {
	err := c.UDPConn.Close()
	c.once.Do(func() {
		c.owner.mu.Lock()
		c.owner.closed++
		c.owner.mu.Unlock()
	})
	return err
}

func TestNetworkPeerCreationAfterCloseReleasesResources(t *testing.T) {
	for _, warm := range []bool{false, true} {
		name := "active"
		if warm {
			name = "warm"
		}
		t.Run(name, func(t *testing.T) {
			p := newPair(t)
			n := testRecordingNetwork(t)
			udp := &observedPionNet{Net: n.pion, started: make(chan struct{}), release: make(chan struct{})}
			n.pion = udp
			var release sync.Once
			unblock := func() { release.Do(func() { close(udp.release) }) }
			defer unblock()
			id, err := identity.Generate()
			if err != nil {
				t.Fatal(err)
			}
			address := p.aURL
			if warm {
				address = p.bURL
			}
			c, err := DialWithNetwork(testCtx(t), address, id, name, "", DialOpts{Warm: warm}, n)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetLoopbackICE(true)
			go drain(c)
			result := make(chan error, 1)
			go func() {
				if warm {
					result <- c.OpenWarmPeer(testCtx(t))
					return
				}
				result <- c.JoinVoice(testCtx(t), warmVoiceCh, &markerSource{marker: 1}, newRecSink())
			}()
			select {
			case <-udp.started:
			case <-time.After(5 * time.Second):
				t.Fatal("peer never requested provider socket")
			}
			closed := make(chan struct{})
			go func() { c.Close(); close(closed) }()
			<-c.done
			unblock()
			select {
			case err := <-result:
				if !errors.Is(err, ErrClosed) {
					t.Fatalf("late peer err=%v; want ErrClosed", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("late peer did not exit")
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not exit")
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				opened, closed := udp.counts()
				if opened > 0 && closed == opened {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("provider sockets: opened=%d closed=%d", opened, closed)
				}
				time.Sleep(10 * time.Millisecond)
			}
			c.wpMu.Lock()
			retained := c.wp != nil
			c.wpMu.Unlock()
			if retained {
				t.Fatal("closed client retained a warm peer")
			}
		})
	}
}
