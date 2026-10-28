package embeddedprobe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeBackend struct {
	state       profileState
	upErr       error
	packets     *memoryPacketNetwork
	mu          sync.Mutex
	closed      bool
	listenCalls int
	packetCalls int
	dialCalls   int
}

func (b *fakeBackend) Up(context.Context) (profileState, error) { return b.state, b.upErr }
func (b *fakeBackend) Dial(context.Context, string, string) (net.Conn, error) {
	b.mu.Lock()
	b.dialCalls++
	b.mu.Unlock()
	return nil, errUnsupported
}
func (b *fakeBackend) Listen(string, string) (net.Listener, error) {
	b.mu.Lock()
	b.listenCalls++
	b.mu.Unlock()
	return nil, errUnsupported
}
func (b *fakeBackend) ListenPacket(_, address string) (net.PacketConn, error) {
	b.mu.Lock()
	b.packetCalls++
	b.mu.Unlock()
	if b.packets == nil {
		return nil, errUnsupported
	}
	return b.packets.listen(address)
}
func (b *fakeBackend) Close() error   { b.mu.Lock(); b.closed = true; b.mu.Unlock(); return nil }
func (b *fakeBackend) isClosed() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.closed }
func (b *fakeBackend) calls() (int, int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dialCalls, b.listenCalls, b.packetCalls
}

func openFake(t *testing.T, dir string, b *fakeBackend) *Node {
	t.Helper()
	n, err := openWithBackend(context.Background(), dir, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n
}

func TestProfilesDoNotShareStateByControlURL(t *testing.T) {
	base := t.TempDir()
	aBackend := &fakeBackend{state: profileState{Local: []netip.Addr{netip.MustParseAddr("100.64.0.1")}}}
	bBackend := &fakeBackend{state: profileState{Local: []netip.Addr{netip.MustParseAddr("100.64.0.2")}}}
	a := openFake(t, filepath.Join(base, "group-a"), aBackend)
	b := openFake(t, filepath.Join(base, "group-b"), bBackend)
	if a.stateDir == b.stateDir || a.local[0] == b.local[0] {
		t.Fatalf("profiles shared state: A=%q/%v B=%q/%v", a.stateDir, a.local, b.stateDir, b.local)
	}
	dup := &fakeBackend{state: profileState{Local: []netip.Addr{netip.MustParseAddr("100.64.0.3")}}}
	if n, err := openWithBackend(context.Background(), a.stateDir, dup); err == nil || n != nil {
		t.Fatal("duplicate state directory opened")
	}
	if dup.isClosed() {
		t.Fatal("backend that was never opened was closed")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if bBackend.isClosed() {
		t.Fatal("closing one profile closed another")
	}
}

func TestNotReadyProviderFailsClosed(t *testing.T) {
	b := &fakeBackend{}
	n, err := openWithBackend(context.Background(), t.TempDir(), b)
	if err == nil || n != nil {
		t.Fatalf("node=%v err=%v; want no-address failure", n, err)
	}
	if !b.isClosed() {
		t.Fatal("failed backend was not closed")
	}
}

func TestPionNetRejectsNonProfileBind(t *testing.T) {
	b := &fakeBackend{packets: newMemoryPacketNetwork(), state: profileState{
		Local: []netip.Addr{netip.MustParseAddr("100.64.0.10")},
		Peers: []netip.Addr{netip.MustParseAddr("100.64.0.11")},
	}}
	n := openFake(t, t.TempDir(), b)
	if pc, err := n.ListenPacket(context.Background(), "udp4", "127.0.0.1:9000"); err == nil || pc != nil {
		t.Fatal("host-network bind escaped the profile")
	}
	if c, err := n.DialContext(context.Background(), "udp4", "192.0.2.1:9000"); err == nil || c != nil {
		t.Fatal("off-profile destination was accepted")
	}
	if c, err := n.DialContext(context.Background(), "tcp", "public.example:9000"); err == nil || c != nil {
		t.Fatal("unverified DNS destination was accepted")
	}
	dials, listens, packets := b.calls()
	if dials != 0 || listens != 0 || packets != 0 {
		t.Fatalf("rejected operations reached backend: %d %d %d", dials, listens, packets)
	}
	pc, err := n.ListenPacket(context.Background(), "udp4", "100.64.0.10:9000")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.WriteTo([]byte("escape"), &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 9000}); err == nil {
		t.Fatal("off-profile UDP destination was accepted")
	}
}

func TestProfileStateDirectoriesAreResolved(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("test-state", t.Name())
	abs, _ := filepath.Abs(rel)
	b := &fakeBackend{state: profileState{Local: []netip.Addr{netip.MustParseAddr("100.64.1.1")}}}
	n := openFake(t, rel, b)
	if n.stateDir != filepath.Clean(abs) {
		t.Fatalf("state dir = %q, want under %q", n.stateDir, cwd)
	}
}

type memoryPacketNetwork struct {
	mu       sync.Mutex
	conns    map[string]*memoryPacketConn
	nextPort int
}

type memoryPacket struct {
	data []byte
	from *net.UDPAddr
}

type memoryPacketConn struct {
	net           *memoryPacketNetwork
	addr          *net.UDPAddr
	recv          chan memoryPacket
	closed        chan struct{}
	once          sync.Once
	mu            sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
}

func newMemoryPacketNetwork() *memoryPacketNetwork {
	return &memoryPacketNetwork{conns: map[string]*memoryPacketConn{}, nextPort: 20000}
}

func (m *memoryPacketNetwork) listen(address string) (net.PacketConn, error) {
	u, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.Port == 0 {
		u.Port = m.nextPort
		m.nextPort++
	}
	key := u.String()
	if _, ok := m.conns[key]; ok {
		return nil, errors.New("address in use")
	}
	c := &memoryPacketConn{net: m, addr: u, recv: make(chan memoryPacket, 8), closed: make(chan struct{})}
	m.conns[key] = c
	return c, nil
}

func (c *memoryPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	deadline := c.readDeadline
	c.mu.Unlock()
	var timer <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, nil, os.ErrDeadlineExceeded
		}
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case pkt := <-c.recv:
		n := copy(p, pkt.data)
		return n, pkt.from, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	case <-timer:
		return 0, nil, os.ErrDeadlineExceeded
	}
}
func (c *memoryPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	deadline := c.writeDeadline
	c.mu.Unlock()
	if !deadline.IsZero() && time.Now().After(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	c.net.mu.Lock()
	dst := c.net.conns[addr.String()]
	c.net.mu.Unlock()
	if dst == nil {
		return 0, errors.New("destination unavailable")
	}
	pkt := memoryPacket{data: append([]byte(nil), p...), from: c.addr}
	select {
	case dst.recv <- pkt:
		return len(p), nil
	case <-dst.closed:
		return 0, net.ErrClosed
	case <-c.closed:
		return 0, net.ErrClosed
	}
}
func (c *memoryPacketConn) Close() error {
	c.once.Do(func() {
		c.net.mu.Lock()
		delete(c.net.conns, c.addr.String())
		c.net.mu.Unlock()
		close(c.closed)
	})
	return nil
}
func (c *memoryPacketConn) LocalAddr() net.Addr { return c.addr }
func (c *memoryPacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline, c.writeDeadline = t, t
	c.mu.Unlock()
	return nil
}
func (c *memoryPacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *memoryPacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}
