package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// AuthenticateDedicatedBootstrap fetches an active host's signed chain using
// only the VPS's locally generated host key and the operator-pinned WSS URL
// and server ID. It never sends a password. The returned chain may replace
// only a fresh local bootstrap through InstallDedicatedBootstrap.
func AuthenticateDedicatedBootstrap(ctx context.Context, activeWSSURL, pinnedServerID string, bootstrap *chain.Chain, local *identity.Identity, chainPath string) (*chain.Chain, error) {
	canonicalURL, err := proto.CanonicalDedicatedWSS(activeWSSURL)
	if err != nil {
		return nil, errors.New("server: dedicated active URL must be a canonical root wss URL")
	}
	if _, err := chain.ParseID(pinnedServerID); err != nil {
		return nil, fmt.Errorf("server: invalid pinned dedicated server ID: %w", err)
	}
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, canonicalURL, nil)
	if err != nil {
		return nil, fmt.Errorf("server: dial pinned dedicated host: %w", err)
	}
	defer ws.Close()
	res, err := hshake.Run(ctx, ws, local, "dedicated", "", hshake.Opts{Pin: pinnedServerID, Warm: true})
	if err != nil {
		return nil, fmt.Errorf("server: authenticate pinned dedicated host: %w", err)
	}
	if res.ServerID != pinnedServerID || res.Chain == nil {
		return nil, errors.New("server: pinned dedicated host returned a different server")
	}
	return InstallDedicatedBootstrap(bootstrap, local, res.Chain.Entries(), chainPath)
}
