package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// InstallDedicatedBootstrap replaces only the freshly generated local
// bootstrap chain with the verified active chain. It deliberately accepts no
// existing group state: retrying against a partially prepared or unrelated
// state must be an operator-visible error, not a merge.
//
// path may be empty for an in-memory destination. When present, SetPath uses
// a same-directory temp file and rename, so the old bootstrap or the complete
// authorized chain survives a crash.
func InstallDedicatedBootstrap(bootstrap *chain.Chain, local *identity.Identity, entries []chain.Entry, path string) (*chain.Chain, error) {
	if bootstrap == nil || local == nil {
		return nil, errors.New("server: missing dedicated bootstrap identity")
	}
	if !isDedicatedBootstrap(bootstrap, local) {
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

func isDedicatedBootstrap(c *chain.Chain, local *identity.Identity) bool {
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

// handleDedicatedProof verifies a proof only for the prepared authenticated
// link that received the source-captured boundary. A changed source head or
// history endpoint invalidates the attempt; no state is committed here.
func (c *conn) handleDedicatedProof(env proto.Envelope) {
	var p proto.DedicatedReadinessProof
	if json.Unmarshal(env.Data, &p) != nil || p.Validate() != nil {
		c.sendError(proto.CodeBadRequest, "malformed dedicated readiness proof")
		return
	}
	s := c.srv
	s.chMu.Lock()
	want := c.link.dedicated
	s.chMu.Unlock()
	if want == nil || !c.link.hello.Prepared || !sameReadiness(*want, p.Readiness) {
		c.sendError(proto.CodeForbidden, "unexpected dedicated readiness proof")
		return
	}
	if !proto.VerifyDedicatedReadiness(c.pub, p.Readiness, p.Signature) {
		c.sendError(proto.CodeForbidden, "invalid dedicated readiness proof")
		return
	}
	// Re-read both ends after the proof arrives. This catches a source append,
	// a live chat, and a replay after a link reconnect.
	if s.chain.ID().String() != want.ServerID || s.chain.Head().Seq != want.ChainHead {
		c.sendError(proto.CodeBadChain, "source chain moved during preparation")
		return
	}
	ts, id := time.Time{}, ""
	if s.hist != nil {
		ts, id = s.hist.Last()
	}
	if id != want.HistoryLastID || ts.UnixMilli() != want.HistoryLastUnixMilli {
		c.sendError(proto.CodeBadRequest, "source history moved during preparation")
		return
	}
	if !c.sendMsg(proto.TypeDedicatedAck, proto.DedicatedAck{Readiness: *want}) {
		return
	}
	// One acknowledgement per captured nonce. A replay on the same link cannot
	// create a second transition, and reconnecting obtains a fresh nonce.
	s.chMu.Lock()
	c.link.dedicated = nil
	s.chMu.Unlock()
}

func sameReadiness(a, b proto.DedicatedReadiness) bool {
	return a.ServerID == b.ServerID && a.DestinationFingerprint == b.DestinationFingerprint && a.ChainHead == b.ChainHead && a.HistoryLastID == b.HistoryLastID && a.HistoryLastUnixMilli == b.HistoryLastUnixMilli && string(a.TransitionNonce) == string(b.TransitionNonce)
}
