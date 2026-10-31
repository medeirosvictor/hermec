package dedicated

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

// AuthenticateBootstrap fetches an active host's signed chain using
// only the VPS's locally generated host key and the operator-pinned WSS URL
// and server ID. It never sends a password. The returned chain may replace
// only a fresh local bootstrap through InstallBootstrap.
func AuthenticateBootstrap(ctx context.Context, activeWSSURL, pinnedServerID string, bootstrap *chain.Chain, local *identity.Identity, chainPath string) (*chain.Chain, error) {
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
	return InstallBootstrap(bootstrap, local, res.Chain.Entries(), chainPath)
}

// InstallBootstrap replaces only the freshly generated local
// bootstrap chain with the verified active chain. It deliberately accepts no
// existing group state: retrying against a partially prepared or unrelated
// state must be an operator-visible error, not a merge.
//
// path may be empty for an in-memory destination. When present, SetPath uses
// a same-directory temp file and rename, so the old bootstrap or the complete
// authorized chain survives a crash.
func InstallBootstrap(bootstrap *chain.Chain, local *identity.Identity, entries []chain.Entry, path string) (*chain.Chain, error) {
	if bootstrap == nil || local == nil {
		return nil, errors.New("server: missing dedicated bootstrap identity")
	}
	if !isBootstrap(bootstrap, local) {
		return nil, errors.New("server: refusing to replace non-bootstrap chain")
	}
	next, err := chain.FromEntries(entries)
	if err != nil {
		return nil, fmt.Errorf("server: invalid authorized chain: %w", err)
	}
	nextState := next.State()
	if !nextState.IsHost(local.PublicKey()) {
		return nil, errors.New("server: authorized chain does not authorize this VPS key as a host")
	}
	if path != "" {
		if err := next.SetPath(path); err != nil {
			return nil, fmt.Errorf("server: install dedicated chain: %w", err)
		}
	}
	return next, nil
}

func isBootstrap(c *chain.Chain, local *identity.Identity) bool {
	es := c.Entries()
	st := c.State()
	// OpenChain's fresh state is genesis/config/host_add. Be deliberately
	// stricter than state alone: grants, bans, or any extra append mean this is
	// already a real chain and must never be overwritten by preparation.
	if len(es) > 3 || len(es) == 0 || es[0].Op != chain.OpGenesis || !st.IsHost(local.PublicKey()) || len(st.Hosts) != 1 || st.Founder != identity.Fingerprint(local.PublicKey()) {
		return false
	}
	for _, e := range es {
		if e.Op != chain.OpGenesis && e.Op != chain.OpConfig && e.Op != chain.OpHostAdd {
			return false
		}
	}
	return true
}
