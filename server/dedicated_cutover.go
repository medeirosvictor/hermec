package server

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"

	"github.com/medeirosvictor/hermec/core/proto"
)

// emptyVoiceLocked reports the dedicated cutover gate while chMu is held.
// restoreDedicatedTransition applies only durable terminal states. A malformed
// journal fails closed rather than silently turning a fenced source active.
func (s *Server) restoreDedicatedTransition() error {
	if s.cfg.DedicatedTransitionPath == "" {
		return nil
	}
	r, err := LoadDedicatedTransition(s.cfg.DedicatedTransitionPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.State == DedicatedTransitionFenced {
		s.modeState.mode.Store(int32(ModeFenced))
		return nil
	}
	if r.State != DedicatedTransitionCommitted {
		return nil
	}
	check := r
	check.State = DedicatedTransitionPrepared
	if err := ValidateDedicatedPrepared(check, s.cfg.HostKey, s.chain, s.hist); err != nil {
		return err
	}
	var pub []byte
	for _, h := range s.chain.State().Hosts {
		if h.Fingerprint == r.Certificate.SourceFingerprint {
			pub = h.PubKey
			break
		}
	}
	if pub == nil || !proto.VerifyDedicatedCutover(pub, *r.Certificate) {
		return errors.New("server: invalid committed dedicated certificate")
	}
	s.prepared.Store(false)
	s.modeState.mode.Store(int32(ModeActive))
	return nil
}

func (s *Server) emptyVoiceLocked() bool {
	for _, members := range s.voice {
		if len(members) != 0 {
			return false
		}
	}
	return true
}

// fenceDedicatedLocked durably withdraws this source before granting the
// destination authority. The caller holds dedicatedMu, chainMu and chMu.
func (c *conn) fenceDedicatedLocked(p proto.DedicatedReadinessProof) (proto.DedicatedCertificate, error) {
	s := c.srv
	if s.cfg.DedicatedTransitionPath == "" {
		return proto.DedicatedCertificate{}, errors.New("no dedicated transition journal")
	}
	if !s.emptyVoiceLocked() {
		return proto.DedicatedCertificate{}, errors.New("voice is not empty")
	}
	state := s.chain.State()
	priority, ok := hostPriority(&state, c.pub)
	if !ok {
		return proto.DedicatedCertificate{}, errors.New("destination is no longer an authorized host")
	}
	dst := proto.HostInfo{Fingerprint: c.fingerprint, Name: c.link.hello.Name, Addrs: append([]string(nil), c.link.hello.Addrs...), Priority: priority}
	sig, err := proto.SignDedicatedCutover(s.cfg.HostKey, p.Readiness, s.selfInfo().Fingerprint, dst)
	if err != nil {
		return proto.DedicatedCertificate{}, err
	}
	cert := proto.DedicatedCertificate{Readiness: p.Readiness, SourceFingerprint: s.selfInfo().Fingerprint, Destination: dst, Signature: sig}
	r := DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionFenced, ServerID: p.Readiness.ServerID, DestinationFingerprint: p.Readiness.DestinationFingerprint, ChainHead: p.Readiness.ChainHead, ChainHeadHash: append([]byte(nil), p.Readiness.ChainHeadHash...), HistoryLastID: p.Readiness.HistoryLastID, HistoryLastUnixMilli: p.Readiness.HistoryLastUnixMilli, HistoryCount: p.Readiness.HistoryCount, HistoryHash: append([]byte(nil), p.Readiness.HistoryHash...), TransitionNonce: append([]byte(nil), p.Readiness.TransitionNonce...), Certificate: &cert, Recovery: DedicatedRecoveryInspectCommit}
	if err := SaveDedicatedTransition(s.cfg.DedicatedTransitionPath, r); err != nil {
		return proto.DedicatedCertificate{}, err
	}
	s.setModeLocked(ModeFenced)
	return cert, nil
}

// handleDedicatedCommit is the destination half of the durable two-phase
// cutover. Persistence precedes activation and the same certificate is safe to
// receive repeatedly after lost packets or crashes.
func (l *Link) handleDedicatedCommit(env proto.Envelope) error {
	var m proto.DedicatedCommit
	if json.Unmarshal(env.Data, &m) != nil {
		return errors.New("server: link: malformed dedicated commit")
	}
	c := m.Certificate
	s := l.s
	state := s.chain.State()
	self := s.selfInfo()
	if s.cfg.DedicatedTransitionPath == "" || !s.Prepared() || c.Readiness.DestinationFingerprint != self.Fingerprint || c.Destination.Fingerprint != self.Fingerprint || c.Destination.Name != self.Name || c.Destination.Priority != self.Priority || !reflect.DeepEqual(c.Destination.Addrs, self.Addrs) {
		return errors.New("server: link: unexpected dedicated commit")
	}
	if !state.IsHost(l.s.cfg.HostKey.PublicKey()) {
		return errors.New("server: link: destination no longer authorized")
	}
	// The peer was authenticated as a host, and the certificate must name it.
	// Link's handshake identity is not retained separately, so verify source
	// authorization through the signed chain fingerprint before accepting it.
	var sourcePub []byte
	for _, h := range state.Hosts {
		if h.Fingerprint == c.SourceFingerprint {
			sourcePub = h.PubKey
			break
		}
	}
	if sourcePub == nil || !proto.VerifyDedicatedCutover(sourcePub, c) {
		return errors.New("server: link: certificate source is unauthorized or invalid")
	}
	if !sameReadiness(c.Readiness, *l.prepared) {
		return errors.New("server: link: cutover certificate differs from preparation")
	}
	r := DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionCommitted, ServerID: c.Readiness.ServerID, DestinationFingerprint: c.Readiness.DestinationFingerprint, ChainHead: c.Readiness.ChainHead, ChainHeadHash: append([]byte(nil), c.Readiness.ChainHeadHash...), HistoryLastID: c.Readiness.HistoryLastID, HistoryLastUnixMilli: c.Readiness.HistoryLastUnixMilli, HistoryCount: c.Readiness.HistoryCount, HistoryHash: append([]byte(nil), c.Readiness.HistoryHash...), TransitionNonce: append([]byte(nil), c.Readiness.TransitionNonce...), Certificate: &c, Recovery: DedicatedRecoveryInspectCommit}
	if old, err := LoadDedicatedTransition(s.cfg.DedicatedTransitionPath); err == nil && old.State == DedicatedTransitionCommitted && sameReadiness(old.Certificate.Readiness, c.Readiness) {
		// durable idempotent retry
	} else if err := SaveDedicatedTransition(s.cfg.DedicatedTransitionPath, r); err != nil {
		return err
	}
	s.prepared.Store(false)
	s.SetMode(ModeActive)
	return l.write(proto.TypeDedicatedAck, proto.DedicatedAck{Readiness: c.Readiness})
}
