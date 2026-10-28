package embeddedprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/pion/transport/v5"
)

var errUnsupported = errors.New("embedded probe: operation unsupported by private network")

type pionNetwork struct{ node *Node }

var _ transport.Net = (*pionNetwork)(nil)

func (p *pionNetwork) ListenPacket(network, address string) (net.PacketConn, error) {
	return p.node.ListenPacket(context.Background(), network, address)
}

func (p *pionNetwork) ListenUDP(network string, laddr *net.UDPAddr) (transport.UDPConn, error) {
	if laddr == nil {
		laddr = &net.UDPAddr{}
	}
	address := laddr.String()
	if len(laddr.IP) == 0 {
		host := "0.0.0.0"
		if strings.HasSuffix(network, "6") {
			host = "::"
		}
		address = net.JoinHostPort(host, strconv.Itoa(laddr.Port))
	}
	pc, err := p.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	return &packetUDPConn{PacketConn: pc}, nil
}

func (p *pionNetwork) ListenTCP(string, *net.TCPAddr) (transport.TCPListener, error) {
	return nil, errUnsupported
}

func (p *pionNetwork) Dial(network, address string) (net.Conn, error) {
	return p.node.DialContext(context.Background(), network, address)
}

func (p *pionNetwork) DialUDP(network string, laddr, raddr *net.UDPAddr) (transport.UDPConn, error) {
	if laddr != nil && !laddr.IP.IsUnspecified() {
		ip, ok := netip.AddrFromSlice(laddr.IP)
		if !ok || !p.node.isLocal(ip.Unmap()) {
			return nil, fmt.Errorf("embedded probe: UDP local address is outside this profile")
		}
	}
	if raddr == nil {
		return nil, errors.New("embedded probe: UDP remote address is required")
	}
	c, err := p.node.DialContext(context.Background(), network, raddr.String())
	if err != nil {
		return nil, err
	}
	return &connectedUDPConn{Conn: c, remote: raddr}, nil
}

func (p *pionNetwork) DialTCP(string, *net.TCPAddr, *net.TCPAddr) (transport.TCPConn, error) {
	return nil, errUnsupported
}

func (p *pionNetwork) ResolveIPAddr(network, address string) (*net.IPAddr, error) {
	if network != "ip" && network != "ip4" && network != "ip6" {
		return nil, fmt.Errorf("embedded probe: unsupported IP network %q", network)
	}
	ip, err := netip.ParseAddr(strings.Trim(address, "[]"))
	if err != nil {
		return nil, fmt.Errorf("embedded probe: only literal profile IPs are supported: %w", err)
	}
	if err := checkFamily(network, ip); err != nil {
		return nil, err
	}
	return &net.IPAddr{IP: net.IP(ip.AsSlice())}, nil
}

func (p *pionNetwork) ResolveUDPAddr(network, address string) (*net.UDPAddr, error) {
	ip, port, err := parseLiteralAddr(network, address)
	if err != nil {
		return nil, err
	}
	return &net.UDPAddr{IP: net.IP(ip.AsSlice()), Port: port}, nil
}

func (p *pionNetwork) ResolveTCPAddr(network, address string) (*net.TCPAddr, error) {
	ip, port, err := parseLiteralAddr(network, address)
	if err != nil {
		return nil, err
	}
	return &net.TCPAddr{IP: net.IP(ip.AsSlice()), Port: port}, nil
}

func parseLiteralAddr(network, address string) (netip.Addr, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return netip.Addr{}, 0, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("embedded probe: only literal profile IPs are supported: %w", err)
	}
	if err := checkFamily(network, ip); err != nil {
		return netip.Addr{}, 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return netip.Addr{}, 0, fmt.Errorf("embedded probe: invalid port %q", portText)
	}
	return ip.Unmap(), port, nil
}

func checkFamily(network string, ip netip.Addr) error {
	if strings.HasSuffix(network, "4") && !ip.Is4() {
		return fmt.Errorf("embedded probe: %s requires IPv4", network)
	}
	if strings.HasSuffix(network, "6") && !ip.Is6() {
		return fmt.Errorf("embedded probe: %s requires IPv6", network)
	}
	return nil
}

func (p *pionNetwork) Interfaces() ([]*transport.Interface, error) {
	ifc := transport.NewInterface(net.Interface{Index: 1, Name: "hermec-private", Flags: net.FlagUp})
	for _, ip := range p.node.local {
		bits := 128
		if ip.Is4() {
			bits = 32
		}
		ifc.AddAddress(&net.IPNet{IP: net.IP(ip.AsSlice()), Mask: net.CIDRMask(bits, bits)})
	}
	return []*transport.Interface{ifc}, nil
}

func (p *pionNetwork) InterfaceByIndex(index int) (*transport.Interface, error) {
	if index != 1 {
		return nil, transport.ErrInterfaceNotFound
	}
	ifcs, _ := p.Interfaces()
	return ifcs[0], nil
}

func (p *pionNetwork) InterfaceByName(name string) (*transport.Interface, error) {
	if name != "hermec-private" {
		return nil, transport.ErrInterfaceNotFound
	}
	ifcs, _ := p.Interfaces()
	return ifcs[0], nil
}

func (p *pionNetwork) CreateDialer(*net.Dialer) transport.Dialer { return pionDialer{p} }
func (p *pionNetwork) CreateListenConfig(*net.ListenConfig) transport.ListenConfig {
	return pionListenConfig{p}
}

type pionDialer struct{ net *pionNetwork }

func (d pionDialer) Dial(network, address string) (net.Conn, error) {
	return d.net.Dial(network, address)
}

type pionListenConfig struct{ net *pionNetwork }

func (l pionListenConfig) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	return l.net.node.Listen(ctx, network, address)
}
func (l pionListenConfig) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return l.net.node.ListenPacket(ctx, network, address)
}

type packetUDPConn struct{ net.PacketConn }

var _ transport.UDPConn = (*packetUDPConn)(nil)

func (c *packetUDPConn) RemoteAddr() net.Addr       { return nil }
func (c *packetUDPConn) SetReadBuffer(int) error    { return nil }
func (c *packetUDPConn) SetWriteBuffer(int) error   { return nil }
func (c *packetUDPConn) Read(b []byte) (int, error) { n, _, err := c.ReadFrom(b); return n, err }
func (c *packetUDPConn) Write([]byte) (int, error)  { return 0, errUnsupported }
func (c *packetUDPConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	n, addr, err := c.ReadFrom(b)
	if err != nil {
		return n, nil, err
	}
	u, ok := addr.(*net.UDPAddr)
	if !ok {
		return n, nil, transport.ErrNotUDPAddress
	}
	return n, u, nil
}
func (c *packetUDPConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return c.WriteTo(b, addr)
}
func (c *packetUDPConn) ReadMsgUDP([]byte, []byte) (int, int, int, *net.UDPAddr, error) {
	return 0, 0, 0, nil, errUnsupported
}
func (c *packetUDPConn) WriteMsgUDP([]byte, []byte, *net.UDPAddr) (int, int, error) {
	return 0, 0, errUnsupported
}

type connectedUDPConn struct {
	net.Conn
	remote *net.UDPAddr
}

var _ transport.UDPConn = (*connectedUDPConn)(nil)

func (c *connectedUDPConn) SetReadBuffer(int) error  { return nil }
func (c *connectedUDPConn) SetWriteBuffer(int) error { return nil }
func (c *connectedUDPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, err := c.Read(b)
	return n, c.remote, err
}
func (c *connectedUDPConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	n, err := c.Read(b)
	return n, c.remote, err
}
func (c *connectedUDPConn) ReadMsgUDP([]byte, []byte) (int, int, int, *net.UDPAddr, error) {
	return 0, 0, 0, nil, errUnsupported
}
func (c *connectedUDPConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	if addr != nil && addr.String() != c.remote.String() {
		return 0, errors.New("embedded probe: connected UDP destination changed")
	}
	return c.Write(b)
}
func (c *connectedUDPConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return c.WriteTo(b, addr)
}
func (c *connectedUDPConn) WriteMsgUDP([]byte, []byte, *net.UDPAddr) (int, int, error) {
	return 0, 0, errUnsupported
}
