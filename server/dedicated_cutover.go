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
	switch r.State {
	case DedicatedTransitionPrepared:
		if err := ValidateDedicatedPrepared(r, s.cfg.HostKey, s.chain, s.hist); err != nil {
			return err
		}
		s.prepared.Store(true)
		s.modeState.mode.Store(int32(ModePrepared))
		return nil
	case DedicatedTransitionCommitted:
		if err := ValidateDedicatedCommitted(r, s.cfg.HostKey, s.chain, s.hist); err != nil {
			return err
		}
		if err := s.validateDedicatedCertificate(r, false); err != nil {
			return err
		}
		s.prepared.Store(false)
		s.modeState.mode.Store(int32(ModeActive))
		return nil
	case DedicatedTransitionFenced:
		if err := ValidateDedicatedFenced(r, s.cfg.HostKey, s.chain, s.hist); err != nil {
			return err
		}
		if err := s.validateDedicatedCertificate(r, true); err != nil {
			return err
		}
		s.prepared.Store(false)
		s.modeState.mode.Store(int32(ModeFenced))
		return nil
	default:
		return errors.New("server: invalid dedicated transition state")
	}
}

// validateDedicatedCertificate validates the signed decision against this
// server's current chain. fenced means this local server must be the signer;
// otherwise it must be the exact destination.
func (s *Server) validateDedicatedCertificate(r DedicatedTransitionRecord, fenced bool) error {
	if r.Certificate == nil || r.Certificate.Destination.Fingerprint != r.DestinationFingerprint {
		return errors.New("server: invalid dedicated certificate")
	}
	c := r.Certificate
	var pub []byte
	for _, h := range s.chain.State().Hosts {
		if h.Fingerprint == c.SourceFingerprint {
			pub = h.PubKey
			break
		}
	}
	if pub == nil || !proto.VerifyDedicatedCutover(pub, *c) {
		return errors.New("server: invalid dedicated certificate")
	}
	if fenced {
		if c.SourceFingerprint != s.selfInfo().Fingerprint {
			return errors.New("server: fenced transition names another source")
		}
	} else if !reflect.DeepEqual(c.Destination, s.selfInfo()) {
		return errors.New("server: committed transition names another destination")
	}
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
	// The transition boundary and the durable decision are one serialized
	// operation: no mirrored chain/history write can slip in before persistence.
	// Do not call out (including to takeover orchestration or the websocket)
	// until every one of these locks has been released. In particular, a warm
	// client's takeover holds takeover.mu before it promotes warm sessions,
	// which needs dedicatedMu.
	activated := false
	err := func() error {
		s.dedicatedMu.Lock()
		defer s.dedicatedMu.Unlock()
		s.chainMu.Lock()
		defer s.chainMu.Unlock()
		s.chMu.Lock()
		defer s.chMu.Unlock()

		if s.cfg.DedicatedTransitionPath == "" {
			return errors.New("server: link: unexpected dedicated commit")
		}
		if old, err := LoadDedicatedTransition(s.cfg.DedicatedTransitionPath); err == nil && old.State == DedicatedTransitionCommitted {
			if old.Certificate != nil && reflect.DeepEqual(*old.Certificate, c) {
				return nil
			}
			return errors.New("server: link: committed certificate differs")
		}
		if !s.Prepared() || s.Mode() != ModePrepared {
			return errors.New("server: link: unexpected dedicated commit")
		}
		self := s.selfInfo()
		if c.Readiness.DestinationFingerprint != self.Fingerprint || !reflect.DeepEqual(c.Destination, self) {
			return errors.New("server: link: unexpected dedicated commit")
		}
		state := s.chain.State()
		if !state.IsHost(s.cfg.HostKey.PublicKey()) {
			return errors.New("server: link: destination no longer authorized")
		}
		var sourcePub []byte
		for _, h := range state.Hosts {
			if h.Fingerprint == c.SourceFingerprint {
				sourcePub = h.PubKey
				break
			}
		}
		// The remote handshake identity, retained by Link, must be the signer.
		if sourcePub == nil || c.SourceFingerprint != l.peerFingerprint || !proto.VerifyDedicatedCutover(sourcePub, c) {
			return errors.New("server: link: certificate source is unauthorized or invalid")
		}
		l.prepareMu.Lock()
		prepared := l.prepared
		l.prepareMu.Unlock()
		if prepared == nil || !sameReadiness(c.Readiness, *prepared) {
			return errors.New("server: link: cutover certificate differs from preparation")
		}
		// Revalidate exact local readiness immediately before the durable commit.
		r := DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionPrepared, ServerID: c.Readiness.ServerID, DestinationFingerprint: c.Readiness.DestinationFingerprint, ChainHead: c.Readiness.ChainHead, ChainHeadHash: append([]byte(nil), c.Readiness.ChainHeadHash...), HistoryLastID: c.Readiness.HistoryLastID, HistoryLastUnixMilli: c.Readiness.HistoryLastUnixMilli, HistoryCount: c.Readiness.HistoryCount, HistoryHash: append([]byte(nil), c.Readiness.HistoryHash...), TransitionNonce: append([]byte(nil), c.Readiness.TransitionNonce...), Recovery: DedicatedRecoveryRetryPreparation}
		if err := ValidateDedicatedPrepared(r, s.cfg.HostKey, s.chain, s.hist); err != nil {
			return err
		}
		r.State, r.Certificate, r.Recovery = DedicatedTransitionCommitted, &c, DedicatedRecoveryInspectCommit
		if err := SaveDedicatedTransition(s.cfg.DedicatedTransitionPath, r); err != nil {
			return err
		}
		s.prepared.Store(false)
		s.setModeLocked(ModeActive)
		activated = true
		return nil
	}()
	if err != nil {
		return err
	}
	if activated {
		s.signalPromoteHook()
	}
	// A duplicate commit still receives its ACK, but never reactivates or
	// signals promotion a second time.
	return l.write(proto.TypeDedicatedAck, proto.DedicatedAck{Readiness: c.Readiness})
}
