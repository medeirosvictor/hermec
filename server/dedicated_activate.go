package server

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// handleDedicatedActivate is the only path from Ready to fenced. A nonce-less
// request is a founder-only readiness query; it cannot activate anything.
func (c *conn) handleDedicatedActivate(env proto.Envelope) {
	var m proto.DedicatedActivate
	if json.Unmarshal(env.Data, &m) != nil || m.Validate() != nil {
		// json.Unmarshal retains fields decoded before a validation failure. Echo
		// that correlation token so a caller never leaves its request pending.
		c.sendMsg(proto.TypeDedicatedResult, proto.DedicatedResult{Ref: m.Ref, Status: "error", Message: "malformed dedicated activation request"})
		return
	}
	result := func() proto.DedicatedResult {
		r := proto.DedicatedResult{Ref: m.Ref}
		s := c.srv
		s.dedicatedMu.Lock()
		defer s.dedicatedMu.Unlock()
		s.chainMu.Lock()
		defer s.chainMu.Unlock()
		s.chMu.Lock()
		defer s.chMu.Unlock()

		state := s.chain.State()
		if state.Founder != c.fingerprint {
			r.Status, r.Message = "error", "only the current chain founder may activate a dedicated host"
			return r
		}
		if s.Mode() == ModeFenced {
			// The journal is the authority after the source fences. It makes a
			// lost result or a source restart retryable, but only for precisely
			// the decision already made -- never a fresh activation.
			record, err := LoadDedicatedTransition(s.cfg.DedicatedTransitionPath)
			if err != nil || record.State != DedicatedTransitionFenced || record.Certificate == nil || ValidateDedicatedFenced(record, s.cfg.HostKey, s.chain, s.hist) != nil || s.validateDedicatedCertificate(record, true) != nil {
				r.Status, r.Message = "error", "fenced transition record is unavailable"
				return r
			}
			if m.DestinationFingerprint != record.DestinationFingerprint || len(m.TransitionNonce) == 0 || !reflect.DeepEqual(m.TransitionNonce, record.TransitionNonce) {
				r.Status, r.Message = "retry", "source is fenced for a different dedicated transition"
				return r
			}
			ready := record.Certificate.Readiness
			r.Status, r.Readiness, r.Message = "success", &ready, "source fenced; destination activation requested"
			return r
		}
		if s.Mode() != ModeActive {
			r.Status, r.Message = "retry", "source is no longer active"
			return r
		}
		var dst *conn
		var proof *proto.DedicatedReadinessProof
		for link := range s.links {
			if link.fingerprint == m.DestinationFingerprint && link.link != nil && link.link.dedicated != nil && link.link.dedicatedProof != nil {
				dst, proof = link, link.link.dedicatedProof
				break
			}
		}
		if dst == nil {
			r.Status, r.Message = "pending", "destination is not ready"
			return r
		}
		ready := proof.Readiness
		if !proto.VerifyDedicatedReadiness(dst.pub, ready, proof.Signature) || ready.DestinationFingerprint != m.DestinationFingerprint {
			r.Status, r.Message = "error", "destination readiness proof is invalid"
			return r
		}
		if len(m.TransitionNonce) == 0 {
			r.Status, r.Readiness = "ready", &ready
			return r
		}
		if !reflect.DeepEqual(m.TransitionNonce, ready.TransitionNonce) {
			r.Status, r.Message = "retry", "prepared destination changed; confirm its new fingerprint and nonce"
			return r
		}
		// Recheck every commitment under the cutover locks. A chain append,
		// history update, or voice join cannot slip past this gate.
		head := s.chain.Head()
		headHash := head.Hash()
		if ready.ServerID != s.chain.ID().String() || head.Seq != ready.ChainHead || !reflect.DeepEqual(headHash[:], ready.ChainHeadHash) {
			r.Status, r.Message = "retry", "source chain changed; prepare again"
			return r
		}
		var snapshot []proto.ChatMessage
		ts, id := time.Time{}, ""
		if s.hist != nil {
			snapshot, ts, id = s.hist.Snapshot()
		}
		count, hash := proto.DedicatedHistoryCommitment(snapshot)
		if id != ready.HistoryLastID || ts.UnixMilli() != ready.HistoryLastUnixMilli || count != ready.HistoryCount || !reflect.DeepEqual(hash, ready.HistoryHash) {
			r.Status, r.Message = "retry", "source history changed; prepare again"
			return r
		}
		if !s.emptyVoiceLocked() {
			r.Status, r.Message = "retry", "voice is not empty; ask everyone to leave voice, then retry activation"
			return r
		}
		if !state.IsHost(dst.pub) || identity.Fingerprint(dst.pub) != ready.DestinationFingerprint {
			r.Status, r.Message = "retry", "destination is no longer an authorized host"
			return r
		}
		cert, err := dst.fenceDedicatedLocked(*proof)
		if err != nil {
			r.Status, r.Message = "error", "could not durably fence source"
			return r
		}
		// The journal is durable before either response. A lost response is safe:
		// reconnecting destination receives this same certificate, never a new one.
		_ = dst.sendMsg(proto.TypeDedicatedCommit, proto.DedicatedCommit{Certificate: cert})
		r.Status, r.Readiness, r.Message = "success", &ready, "source fenced; destination activation requested"
		return r
	}()
	c.sendMsg(proto.TypeDedicatedResult, result)
}
