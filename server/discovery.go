package server

import (
	"errors"
	"net"
	"strconv"
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

// errDiscoveryBind marks a failure to bind the UDP discovery socket.
var errDiscoveryBind = errors.New("udp bind failed")

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

// startDiscovery binds the UDP responder on host:port (the TCP listener's
// port). Called from Start after the TCP listener is up; its goroutine is
// joined by Shutdown through s.wg.
func (s *Server) startDiscovery(host string, port int) error {
	pc, err := listenUDP("udp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
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
		pc.Close()
		return err
	}
	s.udp = pc
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.discoverLoop(pc, reply)
	}()
	return nil
}

// discoverLoop answers valid probes. Anything that is not exactly the probe
// magic (including oversized datagrams) is dropped silently. Replies go only
// to the probe's source address, one per probe, rate limited per source IP.
// It owns the sources table; no locking is needed.
func (s *Server) discoverLoop(pc net.PacketConn, reply []byte) {
	sources := make(map[string]*discoverSource)
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
		if string(buf[:n]) != discover.Magic {
			continue
		}
		now := s.clock()
		key := sourceKey(from)
		src, ok := sources[key]
		if !ok {
			if len(sources) >= discoverMaxSources {
				for k, v := range sources {
					if now.Sub(v.seen) > discoverIdle {
						delete(sources, k)
					}
				}
				if len(sources) >= discoverMaxSources {
					continue // table full of active sources: drop
				}
			}
			src = &discoverSource{b: newBucket(discoverRate, discoverBurst, now)}
			sources[key] = src
		}
		src.seen = now
		if !src.b.allow(now) {
			continue
		}
		_, _ = pc.WriteTo(reply, from)
	}
}

func sourceKey(a net.Addr) string {
	if u, ok := a.(*net.UDPAddr); ok {
		return u.IP.String()
	}
	return a.String()
}
