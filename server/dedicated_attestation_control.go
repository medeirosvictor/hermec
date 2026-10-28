package server

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// InspectDedicatedReadinessAttestation returns the source's durable Ready
// boundary. It is intentionally available only to the current founder on an
// active, unfenced source, so it cannot be used to reinterpret a cutover.
// operator is the authenticated local source-host operator.
func (s *Server) InspectDedicatedReadinessAttestation(operator string) (*proto.DedicatedReadiness, error) {
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	s.chMu.Lock()
	defer s.chMu.Unlock()
	if err := s.canManageDedicatedAttestationLocked(operator); err != nil {
		return nil, err
	}
	r, err := s.loadDedicatedReadinessAttestationLocked()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ready := recordReadiness(r)
	return &ready, nil
}

// ResetDedicatedReadinessAttestation safely aborts an obsolete Ready attempt.
// It is not a cutover undo: it refuses after a certificate or fence exists.
// Clearing the durable record and all in-memory prepared links means an old
// nonce cannot become valid again after chain/history/authorization drift.
func (s *Server) ResetDedicatedReadinessAttestation(operator string) error {
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	s.chMu.Lock()
	defer s.chMu.Unlock()
	if err := s.canManageDedicatedAttestationLocked(operator); err != nil {
		return err
	}
	path := s.readinessAttestationPath()
	if path == "" {
		return errors.New("server: no dedicated readiness attestation journal")
	}
	if _, err := s.loadDedicatedReadinessAttestationLocked(); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err != nil {
		return err
	} else {
		err = d.Sync()
		_ = d.Close()
		if err != nil && runtime.GOOS != "windows" {
			return err
		}
	}
	for c := range s.links {
		if c.link != nil && c.link.hello.Prepared {
			c.link.dedicated = nil
			c.link.dedicatedProof = nil
			c.link.dedicatedHistory = nil
		}
	}
	return nil
}

func (s *Server) canManageDedicatedAttestationLocked(operator string) error {
	if s.Mode() != ModeActive || s.Prepared() {
		return errors.New("server: readiness attestation can be managed only on an active source")
	}
	if s.cfg.HostKey == nil || operator == "" || operator != identity.Fingerprint(s.cfg.HostKey.PublicKey()) {
		return errors.New("server: local source operator authentication required for readiness attestation")
	}
	state := s.chain.State()
	if !state.IsHost(s.cfg.HostKey.PublicKey()) {
		return errors.New("server: local readiness-attestation operator is no longer an authorized host")
	}
	if s.cfg.DedicatedTransitionPath == "" {
		return errors.New("server: no dedicated transition journal")
	}
	if r, err := LoadDedicatedTransition(s.cfg.DedicatedTransitionPath); err == nil {
		if r.State == DedicatedTransitionFenced || r.Certificate != nil {
			return errors.New("server: cutover certificate or fence exists")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Server) loadDedicatedReadinessAttestationLocked() (DedicatedTransitionRecord, error) {
	path := s.readinessAttestationPath()
	if path == "" {
		return DedicatedTransitionRecord{}, os.ErrNotExist
	}
	r, err := LoadDedicatedTransition(path)
	if err != nil {
		return DedicatedTransitionRecord{}, err
	}
	if r.State != DedicatedTransitionPrepared {
		return DedicatedTransitionRecord{}, errors.New("server: invalid dedicated readiness attestation")
	}
	return r, nil
}
