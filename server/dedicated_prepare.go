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
	// This is the commit point: no source append, chat/trim, or demotion can
	// cross validation and ACK enqueue. The destination's proof is therefore
	// for one atomic source generation.
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	s.chMu.Lock()
	defer s.chMu.Unlock()
	want := c.link.dedicated
	if want == nil || !c.link.hello.Prepared || !sameReadiness(*want, p.Readiness) {
		c.sendError(proto.CodeForbidden, "unexpected dedicated readiness proof")
		return
	}
	if !proto.VerifyDedicatedReadiness(c.pub, p.Readiness, p.Signature) {
		c.sendError(proto.CodeForbidden, "invalid dedicated readiness proof")
		return
	}
	if s.Mode() != ModeActive {
		c.sendError(proto.CodeNotActive, "source is no longer active")
		return
	}
	head := s.chain.Head()
	headHash := head.Hash()
	if s.chain.ID().String() != want.ServerID || head.Seq != want.ChainHead || string(headHash[:]) != string(want.ChainHeadHash) {
		c.sendError(proto.CodeBadChain, "source chain moved during preparation")
		return
	}
	var snapshot []proto.ChatMessage
	ts, id := time.Time{}, ""
	if s.hist != nil {
		snapshot, ts, id = s.hist.Snapshot()
	}
	historyCount, historyHash := proto.DedicatedHistoryCommitment(snapshot)
	if id != want.HistoryLastID || ts.UnixMilli() != want.HistoryLastUnixMilli || historyCount != want.HistoryCount || string(historyHash) != string(want.HistoryHash) {
		c.sendError(proto.CodeBadRequest, "source history moved during preparation")
		return
	}
	// Legacy preparation records are retained only for an in-memory/test
	// destination. A configured journal uses the durable fenced protocol.
	if s.cfg.DedicatedTransitionPath == "" {
		if !c.sendMsg(proto.TypeDedicatedAck, proto.DedicatedAck{Readiness: *want}) {
			return
		}
		c.link.dedicated, c.link.dedicatedHistory = nil, nil
		return
	}
	cert, err := c.fenceDedicatedLocked(p)
	if err != nil {
		// Crucially this is before the durable fence; source remains active.
		c.sendError(proto.CodeBadRequest, "dedicated cutover not ready")
		return
	}
	// A lost commit is retried from the same fenced journal after restart; no
	// new nonce, state boundary, or certificate is ever minted.
	_ = c.sendMsg(proto.TypeDedicatedCommit, proto.DedicatedCommit{Certificate: cert})
	c.link.dedicated, c.link.dedicatedHistory = nil, nil
}

func sameReadiness(a, b proto.DedicatedReadiness) bool {
	return a.ServerID == b.ServerID && a.DestinationFingerprint == b.DestinationFingerprint && a.ChainHead == b.ChainHead && string(a.ChainHeadHash) == string(b.ChainHeadHash) && a.HistoryLastID == b.HistoryLastID && a.HistoryLastUnixMilli == b.HistoryLastUnixMilli && a.HistoryCount == b.HistoryCount && string(a.HistoryHash) == string(b.HistoryHash) && string(a.TransitionNonce) == string(b.TransitionNonce)
}
