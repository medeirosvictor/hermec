package server

import (
	"errors"
	"net"
	"time"
	"unicode/utf8"

	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/version"
)

const (
	discoverRate  = 10
	discoverBurst = 10
	// discoverIdle is how long a source's bucket may sit unused before it
	// is eligible for pruning (it has fully refilled by then).
	discoverIdle = 10 * time.Second
	// discoverMaxSources bounds the per-source table.
	discoverMaxSources = 4096
)

// listenUDP binds the discovery socket; tests replace it to inject failures.
var listenUDP = net.ListenPacket

// ErrDiscoveryBind marks a failure to bind the UDP discovery socket.
var ErrDiscoveryBind = errors.New("udp bind failed")

type discoverSource struct {
	b    *bucket
	seen time.Time
}

// discoveryBindHost returns the UDP bind host for a TCP listener bound to ip:
// the same interface, so a loopback-only server never opens a wildcard UDP
// socket (which would trigger OS firewall prompts and announce on the LAN).
// Only a genuinely unspecified (wildcard) address yields "" (wildcard).
func discoveryBindHost(ip net.IP) string {
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

// discoverer answers discovery probes. It owns the sources table and is
// driven by a single goroutine (discoverLoop, or the shared media socket's
// reader), so it needs no locking.
type discoverer struct {
	s       *Server
	reply   []byte
	sources map[string]*discoverSource
}

// newDiscoverer builds the responder announcing the given listen port.
func (s *Server) newDiscoverer(port int) (*discoverer, error) {
	name := s.cfg.ServerName
	if name == "" {
		name = s.ln.Addr().String()
	}
	for utf8.RuneCountInString(name) > discover.MaxNameRunes {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	reply, err := discover.EncodeAnnounce(discover.Announce{
		Hermec: 1, Name: name, Port: port, Ver: version.Version,
	})
	if err != nil {
		return nil, err
	}
	return &discoverer{s: s, reply: reply, sources: make(map[string]*discoverSource)}, nil
}

// handle answers pkt if it is exactly the probe magic and reports whether it
// was one. Replies go only to the probe's source address, one per probe, rate
// limited per source IP.
func (d *discoverer) handle(pc net.PacketConn, pkt []byte, from net.Addr) bool {
	if string(pkt) != discover.Magic {
		return false
	}
	now := d.s.clock()
	key := sourceKey(from)
	src, ok := d.sources[key]
	if !ok {
		if len(d.sources) >= discoverMaxSources {
			for k, v := range d.sources {
				if now.Sub(v.seen) > discoverIdle {
					delete(d.sources, k)
				}
			}
			if len(d.sources) >= discoverMaxSources {
				return true // table full of active sources: drop
			}
		}
		src = &discoverSource{b: newBucket(discoverRate, discoverBurst, now)}
		d.sources[key] = src
	}
	src.seen = now
	if src.b.allow(now) {
		_, _ = pc.WriteTo(d.reply, from)
	}
	return true
}

// discoverLoop serves probes on a dedicated socket. Anything that is not
// exactly the probe magic (including oversized datagrams) is dropped silently.
func (s *Server) discoverLoop(pc net.PacketConn, d *discoverer) {
	buf := make([]byte, discover.MaxPacket+1)
	errRun := 0
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // closed by Shutdown
			}
			// Other errors (e.g. Windows reports an oversized datagram as
			// an error); drop that datagram and keep serving. Back off if
			// the errors persist so a broken socket cannot hot-spin.
			if errRun++; errRun >= 3 {
				time.Sleep(50 * time.Millisecond)
			}
			continue
		}
		errRun = 0
		d.handle(pc, buf[:n], from)
	}
}

func sourceKey(a net.Addr) string {
	if u, ok := a.(*net.UDPAddr); ok {
		return u.IP.String()
	}
	return a.String()
}
