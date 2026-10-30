package server

import (
	"context"
	"errors"
	"net"

	"github.com/gorilla/websocket"
)

// validateNetwork checks private-provider combinations before any listener is
// opened and retains the provider's Pion network for the server lifetime.
func (s *Server) validateNetwork() error {
	if s.cfg.Network == nil {
		s.pionNet = nil
		return nil
	}
	if s.cfg.Discoverable {
		return errors.New("server: network provider cannot use LAN discovery")
	}
	if s.cfg.PublicIP != "" {
		return errors.New("server: network provider cannot use public_ip")
	}
	pionNet := s.cfg.Network.PionNet()
	if pionNet == nil {
		return errors.New("server: network PionNet is nil")
	}
	s.pionNet = pionNet
	return nil
}

func (s *Server) listen(ctx context.Context, network, address string) (net.Listener, error) {
	if s.cfg.Network != nil {
		return s.cfg.Network.Listen(ctx, network, address)
	}
	return (&net.ListenConfig{}).Listen(ctx, network, address)
}

func (s *Server) listenPacket(ctx context.Context, network, address string, discovery bool) (net.PacketConn, error) {
	if s.cfg.Network != nil {
		return s.cfg.Network.ListenPacket(ctx, network, address)
	}
	if discovery {
		return listenUDP(network, address)
	}
	return (&net.ListenConfig{}).ListenPacket(ctx, network, address)
}

// hostDialer returns a per-call websocket dialer. Provider-backed hosts ignore
// process-global proxy and TLS dial hooks so a private link cannot escape onto
// system networking.
func (s *Server) hostDialer() websocket.Dialer {
	d := *websocket.DefaultDialer
	if s.cfg.Network != nil {
		d.NetDial = nil
		d.NetDialTLSContext = nil
		d.Proxy = nil
		d.NetDialContext = s.cfg.Network.DialContext
	}
	return d
}
