package embeddedprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/medeirosvictor/hermec/netpath"
	"github.com/pion/transport/v5"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

// ProfileConfig describes one persisted embedded-network node.
type ProfileConfig struct {
	StateDir   string
	Hostname   string
	ControlURL string
	OnLoginURL func(string)
}

type profileState struct {
	Local []netip.Addr
	Peers []netip.Addr
}

type nodeBackend interface {
	Up(context.Context) (profileState, error)
	Dial(context.Context, string, string) (net.Conn, error)
	Listen(string, string) (net.Listener, error)
	ListenPacket(string, string) (net.PacketConn, error)
	Close() error
}

type tsnetBackend struct{ server *tsnet.Server }

func (b *tsnetBackend) Up(ctx context.Context) (profileState, error) {
	st, err := b.server.Up(ctx)
	if err != nil {
		return profileState{}, err
	}
	return stateFromStatus(st), nil
}
func (b *tsnetBackend) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return b.server.Dial(ctx, network, address)
}
func (b *tsnetBackend) Listen(network, address string) (net.Listener, error) {
	return b.server.Listen(network, address)
}
func (b *tsnetBackend) ListenPacket(network, address string) (net.PacketConn, error) {
	return b.server.ListenPacket(network, address)
}
func (b *tsnetBackend) Close() error { return b.server.Close() }

func stateFromStatus(st *ipnstate.Status) profileState {
	var out profileState
	if st == nil {
		return out
	}
	out.Local = append(out.Local, st.TailscaleIPs...)
	for _, p := range st.Peer {
		if p != nil {
			out.Peers = append(out.Peers, p.TailscaleIPs...)
		}
	}
	return out
}

var (
	openMu   sync.Mutex
	openDirs = map[string]struct{}{}
	authURL  = regexp.MustCompile(`https?://[^\s]+`)
)

// Node is one isolated embedded-network profile. It implements netpath.Network.
type Node struct {
	backend   nodeBackend
	stateDir  string
	local     []netip.Addr
	peers     map[netip.Addr]struct{}
	pion      *pionNetwork
	closeOnce sync.Once
	closeErr  error
}

var _ netpath.Network = (*Node)(nil)

// Open starts one tsnet node and waits until it has an assigned profile IP.
func Open(ctx context.Context, cfg ProfileConfig) (*Node, error) {
	if strings.TrimSpace(cfg.StateDir) == "" {
		return nil, errors.New("embedded probe: state directory is required")
	}
	abs, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("embedded probe: state directory: %w", err)
	}
	abs = filepath.Clean(abs)
	server := &tsnet.Server{
		Dir: abs, Hostname: cfg.Hostname, ControlURL: cfg.ControlURL,
		Logf: func(string, ...any) {},
	}
	if cfg.OnLoginURL != nil {
		server.UserLogf = func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			if raw := authURL.FindString(line); raw != "" {
				cfg.OnLoginURL(strings.TrimRight(raw, ".,);]"))
			}
		}
	} else {
		server.UserLogf = func(string, ...any) {}
	}
	return openWithBackend(ctx, abs, &tsnetBackend{server: server})
}

func openWithBackend(ctx context.Context, stateDir string, backend nodeBackend) (*Node, error) {
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	openMu.Lock()
	if _, exists := openDirs[abs]; exists {
		openMu.Unlock()
		return nil, fmt.Errorf("embedded probe: state directory already open: %s", abs)
	}
	openDirs[abs] = struct{}{}
	openMu.Unlock()
	release := func() {
		openMu.Lock()
		delete(openDirs, abs)
		openMu.Unlock()
	}

	state, err := backend.Up(ctx)
	if err != nil {
		_ = backend.Close()
		release()
		return nil, fmt.Errorf("embedded probe: start node: %w", err)
	}
	local := validUnique(state.Local)
	if len(local) == 0 {
		_ = backend.Close()
		release()
		return nil, errors.New("embedded probe: node ready without a profile address")
	}
	n := &Node{backend: backend, stateDir: abs, local: local, peers: map[netip.Addr]struct{}{}}
	for _, ip := range validUnique(state.Peers) {
		n.peers[ip] = struct{}{}
	}
	n.pion = &pionNetwork{node: n}
	return n, nil
}

func validUnique(in []netip.Addr) []netip.Addr {
	seen := map[netip.Addr]bool{}
	out := make([]netip.Addr, 0, len(in))
	for _, ip := range in {
		ip = ip.Unmap()
		if ip.IsValid() && !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out
}

func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		n.closeErr = n.backend.Close()
		openMu.Lock()
		delete(openDirs, n.stateDir)
		openMu.Unlock()
	})
	return n.closeErr
}

func (n *Node) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := n.checkDestination(network, address); err != nil {
		return nil, err
	}
	return n.backend.Dial(ctx, network, address)
}

func (n *Node) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bound, err := n.profileBind(network, address)
	if err != nil {
		return nil, err
	}
	return n.backend.Listen(network, bound)
}

func (n *Node) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bound, err := n.profileBind(network, address)
	if err != nil {
		return nil, err
	}
	pc, err := n.backend.ListenPacket(network, bound)
	if err != nil {
		return nil, err
	}
	return &restrictedPacketConn{PacketConn: pc, node: n}, nil
}

func (n *Node) PionNet() transport.Net { return n.pion }

func (n *Node) profileBind(network, address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("embedded probe: bind address: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", fmt.Errorf("embedded probe: bind requires a profile IP: %q", host)
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() {
		ip, err = n.localForNetwork(network)
		if err != nil {
			return "", err
		}
	} else if !n.isLocal(ip) {
		return "", fmt.Errorf("embedded probe: bind IP %s is outside this profile", ip)
	}
	return net.JoinHostPort(ip.String(), port), nil
}

func (n *Node) localForNetwork(network string) (netip.Addr, error) {
	want6 := strings.HasSuffix(network, "6")
	want4 := strings.HasSuffix(network, "4")
	for _, ip := range n.local {
		if (!want6 && !want4) || (want6 && ip.Is6()) || (want4 && ip.Is4()) {
			return ip, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("embedded probe: no profile address for %s", network)
}

func (n *Node) isLocal(ip netip.Addr) bool {
	for _, own := range n.local {
		if own == ip.Unmap() {
			return true
		}
	}
	return false
}

type restrictedPacketConn struct {
	net.PacketConn
	node *Node
}

func (c *restrictedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	u, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, errors.New("embedded probe: UDP destination is not an IP address")
	}
	ip, ok := netip.AddrFromSlice(u.IP)
	if !ok {
		return 0, errors.New("embedded probe: UDP destination has no IP")
	}
	ip = ip.Unmap()
	if !c.node.isLocal(ip) {
		if _, allowed := c.node.peers[ip]; !allowed {
			return 0, fmt.Errorf("embedded probe: UDP destination %s is outside this profile", ip)
		}
	}
	return c.PacketConn.WriteTo(p, addr)
}

func (n *Node) checkDestination(network, address string) error {
	if !strings.HasPrefix(network, "tcp") && !strings.HasPrefix(network, "udp") {
		return fmt.Errorf("embedded probe: unsupported network %q", network)
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("embedded probe: destination: %w", err)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("embedded probe: destination must be a peer IP during the transport proof: %q", host)
	}
	ip = ip.Unmap()
	if n.isLocal(ip) {
		return nil
	}
	if _, ok := n.peers[ip]; !ok {
		return fmt.Errorf("embedded probe: destination %s is outside this profile", ip)
	}
	return nil
}
