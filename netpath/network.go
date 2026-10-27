// Package netpath defines the per-instance network used by private HERMEC
// sessions. A nil Network selects the process's existing system networking.
package netpath

import (
	"context"
	"net"

	"github.com/pion/transport/v5"
)

// Network supplies signaling, hosting and Pion ICE sockets for one instance.
// Private implementations must fail unavailable operations rather than using
// host sockets.
type Network interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Listen(context.Context, string, string) (net.Listener, error)
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
	PionNet() transport.Net
}
