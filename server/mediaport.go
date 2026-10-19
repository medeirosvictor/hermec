package server

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/pion/ice/v4"
)

// ErrMediaBind marks a failure to bind the UDP media port.
var ErrMediaBind = errors.New("media port bind failed")

// probeConn wraps the shared discovery+media socket. Discovery probes are
// answered inline and never reach the ICE mux; everything else (STUN, DTLS,
// RTP) is passed through. ReadFrom is only called from the mux's single
// reader goroutine, which is what makes the discoverer's lock-free state safe.
type probeConn struct {
	net.PacketConn
	d *discoverer
}

func (c *probeConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, from, err := c.PacketConn.ReadFrom(p)
		if err != nil {
			return n, from, err
		}
		if c.d.handle(c.PacketConn, p[:n], from) {
			continue
		}
		return n, from, nil
	}
}

// startUDP binds the server's UDP sockets on host (the TCP listener's
// interface) after the TCP listener is up: the discovery responder (when
// discoverable) and the single media port (unless the legacy
// UDPPortMin/UDPPortMax range is set). By default both live on the listener's
// port number, so they share one socket. Discovery goroutines are joined by
// Shutdown through s.wg; the mux is closed there too.
func (s *Server) startUDP(host string, port int) error {
	media := !(s.cfg.UDPPortMin != 0 && s.cfg.UDPPortMax != 0)
	mediaPort := s.cfg.MediaPort
	if mediaPort == 0 {
		mediaPort = port
	}
	shared := media && mediaPort == port

	var d *discoverer
	var pc net.PacketConn
	if s.cfg.Discoverable {
		var err error
		if pc, err = listenUDP("udp", net.JoinHostPort(host, strconv.Itoa(port))); err != nil {
			if shared {
				return fmt.Errorf("server: media port %d is in use: %w: %w", port, ErrDiscoveryBind, err)
			}
			return fmt.Errorf("server: discovery listen: %w: %w", ErrDiscoveryBind, err)
		}
		if d, err = s.newDiscoverer(port); err != nil {
			pc.Close()
			return fmt.Errorf("server: discovery listen: %w: %w", ErrDiscoveryBind, err)
		}
		s.udp = pc
	} else if shared {
		var err error
		if pc, err = net.ListenPacket("udp", net.JoinHostPort(host, strconv.Itoa(port))); err != nil {
			return fmt.Errorf("server: media port %d is in use: %w: %w", port, ErrMediaBind, err)
		}
	}

	mediaConn := pc
	if media && !shared {
		var err error
		mediaConn, err = net.ListenPacket("udp", net.JoinHostPort(host, strconv.Itoa(mediaPort)))
		if err != nil {
			s.closeUDP()
			return fmt.Errorf("server: media port %d is in use: %w: %w", mediaPort, ErrMediaBind, err)
		}
	}

	if d != nil && shared {
		// The mux reader drives discovery; no separate loop.
		mediaConn = &probeConn{PacketConn: pc, d: d}
	} else if d != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.discoverLoop(pc, d)
		}()
	}
	if media {
		s.mux = ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: mediaConn})
		s.logf("media: UDP port %d", mediaPort)
	}
	return nil
}

// closeUDP releases sockets bound by a failed startUDP.
func (s *Server) closeUDP() {
	if s.udp != nil {
		s.udp.Close()
		s.udp = nil
	}
}
