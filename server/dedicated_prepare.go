package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// Persist the source-side attestation before the ACK. A restart can then
	// acknowledge only this destination, nonce and exact boundary.
	if err := s.attestDedicatedReadiness(*want); err != nil {
		c.sendError(proto.CodeBadRequest, "cannot durably attest dedicated readiness")
		return
	}
	proof := p
	c.link.dedicatedProof = &proof
	_ = c.sendMsg(proto.TypeDedicatedAck, proto.DedicatedAck{Readiness: *want})
}

func (s *Server) readinessAttestationPath() string {
	if s.cfg.DedicatedTransitionPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.cfg.DedicatedTransitionPath), DedicatedReadinessFile)
}

// checkDedicatedReadinessAttestation prevents a reconnecting destination from
// selecting a nonce or a different boundary. Empty paths are retained for
// in-memory/unit servers only; command-launched sources always set a path.
func (s *Server) checkDedicatedReadinessAttestation(r proto.DedicatedReadiness, resumed bool) error {
	path := s.readinessAttestationPath()
	if path == "" {
		return nil
	}
	old, err := LoadDedicatedTransition(path)
	if errors.Is(err, os.ErrNotExist) {
		if resumed {
			return errors.New("dedicated preparation was not attested by this source")
		}
		return nil
	}
	if err != nil || old.State != DedicatedTransitionPrepared || !sameReadiness(proto.DedicatedReadiness{ServerID: old.ServerID, DestinationFingerprint: old.DestinationFingerprint, ChainHead: old.ChainHead, ChainHeadHash: old.ChainHeadHash, HistoryLastID: old.HistoryLastID, HistoryLastUnixMilli: old.HistoryLastUnixMilli, HistoryCount: old.HistoryCount, HistoryHash: old.HistoryHash, TransitionNonce: old.TransitionNonce}, r) {
		return errors.New("dedicated preparation differs from durable source attestation")
	}
	return nil
}

func (s *Server) attestDedicatedReadiness(r proto.DedicatedReadiness) error {
	if err := s.checkDedicatedReadinessAttestation(r, false); err != nil {
		return err
	}
	path := s.readinessAttestationPath()
	if path == "" {
		return nil
	}
	return SaveDedicatedTransition(path, DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionPrepared, ServerID: r.ServerID, DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, ChainHeadHash: append([]byte(nil), r.ChainHeadHash...), HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, HistoryCount: r.HistoryCount, HistoryHash: append([]byte(nil), r.HistoryHash...), TransitionNonce: append([]byte(nil), r.TransitionNonce...), Recovery: DedicatedRecoveryRetryPreparation})
}

func sameReadiness(a, b proto.DedicatedReadiness) bool {
	return a.ServerID == b.ServerID && a.DestinationFingerprint == b.DestinationFingerprint && a.ChainHead == b.ChainHead && string(a.ChainHeadHash) == string(b.ChainHeadHash) && a.HistoryLastID == b.HistoryLastID && a.HistoryLastUnixMilli == b.HistoryLastUnixMilli && a.HistoryCount == b.HistoryCount && string(a.HistoryHash) == string(b.HistoryHash) && string(a.TransitionNonce) == string(b.TransitionNonce)
}
