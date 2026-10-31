package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server/dedicated"
)

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
	if want == nil || !c.link.hello.Prepared || !dedicated.SameReadiness(*want, p.Readiness) {
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
	// Recheck authorization immediately before persistence. A host removed or
	// banned after the link was admitted must never acquire a durable Ready
	// attestation.
	state := s.chain.State()
	if !state.IsHost(c.pub) || identity.Fingerprint(c.pub) != want.DestinationFingerprint {
		c.sendError(proto.CodeForbidden, "destination is no longer an authorized host")
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
	return filepath.Join(filepath.Dir(s.cfg.DedicatedTransitionPath), dedicated.ReadinessFile)
}

// checkDedicatedReadinessAttestation prevents a reconnecting destination from
// selecting a nonce or a different boundary. Empty paths are retained for
// in-memory/unit servers only; command-launched sources always set a path.
func (s *Server) checkDedicatedReadinessAttestation(r proto.DedicatedReadiness, resumed bool) error {
	path := s.readinessAttestationPath()
	if path == "" {
		return nil
	}
	old, err := dedicated.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		if resumed {
			return errors.New("dedicated preparation was not attested by this source")
		}
		return nil
	}
	if err != nil || old.State != dedicated.Prepared || !dedicated.SameReadiness(proto.DedicatedReadiness{ServerID: old.ServerID, DestinationFingerprint: old.DestinationFingerprint, ChainHead: old.ChainHead, ChainHeadHash: old.ChainHeadHash, HistoryLastID: old.HistoryLastID, HistoryLastUnixMilli: old.HistoryLastUnixMilli, HistoryCount: old.HistoryCount, HistoryHash: old.HistoryHash, TransitionNonce: old.TransitionNonce}, r) {
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
	return dedicated.Save(path, dedicated.Record{Version: 1, State: dedicated.Prepared, ServerID: r.ServerID, DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, ChainHeadHash: append([]byte(nil), r.ChainHeadHash...), HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, HistoryCount: r.HistoryCount, HistoryHash: append([]byte(nil), r.HistoryHash...), TransitionNonce: append([]byte(nil), r.TransitionNonce...), Recovery: dedicated.RecoveryRetryPreparation})
}
