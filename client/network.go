package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/netpath"
	"github.com/pion/transport/v5"
)

// DialWithNetwork authenticates through the supplied per-instance network.
// A nil network preserves the system-network behavior of DialWith.
func DialWithNetwork(ctx context.Context, url string, id *identity.Identity, name, password string, o DialOpts, network netpath.Network) (*Client, error) {
	var pionNet transport.Net
	if network != nil {
		pionNet = network.PionNet()
		if pionNet == nil {
			return nil, errors.New("client: network PionNet is nil")
		}
	}
	dialer := *websocket.DefaultDialer
	if network != nil {
		// A global TLS dial hook takes precedence over NetDialContext, and a
		// proxy changes the destination. Private sessions own their route.
		dialer.NetDial = nil
		dialer.NetDialTLSContext = nil
		dialer.Proxy = nil
		dialer.NetDialContext = network.DialContext
	}
	ws, resp, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		if errors.Is(err, websocket.ErrBadHandshake) && resp != nil {
			se := refusalError(resp.StatusCode)
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			if se != nil {
				return nil, fmt.Errorf("client: dial: %w", se)
			}
		}
		return nil, fmt.Errorf("client: dial: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = ws.Close()
		return nil, err
	}
	res, err := Handshake(ctx, ws, id, name, password, o)
	if err != nil {
		_ = ws.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = ws.Close()
		return nil, err
	}
	c := &Client{
		ws: ws, id: id, network: pionNet,
		fingerprint: res.OK.Fingerprint,
		roles:       res.OK.Roles,
		channels:    res.OK.Channels,
		serverName:  res.OK.ServerName,
		serverID:    res.OK.ServerID,
		mediaLanes:  hasCapability(res.OK.Capabilities, proto.CapabilityMediaLanesV1),
		pwKey:       res.Key,
		chain:       res.Chain,
		activations: make(map[string]chan proto.DedicatedResult),
		events:      make(chan Event, eventBuffer),
		done:        make(chan struct{}),
		loopDone:    make(chan struct{}),
		pingDone:    make(chan struct{}),
		rtt:         newRTTTracker(),
		started:     time.Now(),
	}
	c.warm.Store(o.Warm)
	ws.SetPongHandler(func(p string) error {
		c.rtt.pong(p, time.Now())
		return nil
	})
	go c.readLoop()
	go c.watchdog(time.Now)
	c.startPinger(pingInterval, pongTimeout, time.Now)
	return c, nil
}
