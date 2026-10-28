package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// dedicatedManagePhase is intentionally separate from the end-user dedicated
// join form. Nothing in this state is saved: a VPS key and cutover nonce are
// useful only for this open, authenticated session.
type dedicatedManagePhase int

const (
	dedicatedManageKey dedicatedManagePhase = iota
	dedicatedManageConfirm
	dedicatedManageAuthorizing
	dedicatedManagePending
	dedicatedManageReady
	dedicatedManageRetry
	dedicatedManageRecovery
	dedicatedManageDone
	dedicatedManageError
)

type dedicatedManager struct {
	phase                          dedicatedManagePhase
	key                            field
	pub                            []byte
	fingerprint, serverID, message string
	readiness                      *proto.DedicatedReadiness
	gen, operation                 uint64
}

func maxDedicatedPriority(hosts []chain.Host) int {
	max := 0
	for _, h := range hosts {
		if h.Priority > max {
			max = h.Priority
		}
	}
	return max + 1
}

func (m *dedicatedManager) open(serverID string, founder bool) bool {
	m.close()
	if !founder {
		return false
	}
	m.serverID, m.phase = serverID, dedicatedManageKey
	m.gen++
	return true
}
func (m *dedicatedManager) close() {
	m.key = field{}
	m.pub = nil
	m.fingerprint = ""
	m.serverID = ""
	m.message = ""
	m.readiness = nil
	m.phase = dedicatedManageKey
	m.gen++
}

// keyChanged discards every value derived from the editable public key. The
// authorization UI may only display a fingerprint derived from the visible key.
func (m *dedicatedManager) keyChanged() {
	m.pub = nil
	m.fingerprint = ""
	m.readiness = nil
	m.operation++
	m.phase = dedicatedManageKey
	m.message = ""
}

func (m *dedicatedManager) parse() bool {
	pub, fp, err := client.ParseDedicatedHostPublicKey(m.key.String())
	if err != nil {
		m.pub = nil
		m.fingerprint = ""
		m.phase = dedicatedManageError
		m.message = "Enter the VPS public key printed by hermec-server."
		return false
	}
	m.pub, m.fingerprint, m.phase, m.message = pub, fp, dedicatedManageConfirm, "Confirm authorization for this fingerprint."
	return true
}
func (m *dedicatedManager) confirm(hosts []chain.Host) ([]byte, int, uint64, bool) {
	if m.phase != dedicatedManageConfirm || len(m.pub) != 32 {
		return nil, 0, 0, false
	}
	m.operation++
	m.phase, m.message = dedicatedManageAuthorizing, "Authorizing VPS host…"
	return append([]byte(nil), m.pub...), maxDedicatedPriority(hosts), m.operation, true
}
func (m *dedicatedManager) authorized(gen, operation uint64, err error) bool {
	if gen != m.gen || operation != m.operation || m.phase != dedicatedManageAuthorizing {
		return false
	}
	if err != nil {
		m.phase, m.message = dedicatedManageError, "Could not authorize the VPS host. Check this founder session and try again."
		return false
	}
	m.phase, m.message = dedicatedManagePending, "Host authorized. Waiting for the VPS to prepare…"
	return true
}
func (m *dedicatedManager) beginReady() (uint64, bool) {
	if m.phase != dedicatedManagePending && m.phase != dedicatedManageRetry && m.phase != dedicatedManageRecovery {
		return 0, false
	}
	m.operation++
	return m.operation, true
}

func (m *dedicatedManager) ready(gen, operation uint64, r *proto.DedicatedReadiness, err error) bool {
	if gen != m.gen || operation != m.operation || (m.phase != dedicatedManagePending && m.phase != dedicatedManageRetry && m.phase != dedicatedManageRecovery) {
		return false
	}
	if err != nil {
		m.phase, m.message = dedicatedManagePending, "VPS is not Ready yet. Start or retry its preparation, then check again."
		return false
	}
	if r == nil || r.Validate() != nil || r.ServerID != m.serverID || r.DestinationFingerprint != m.fingerprint {
		m.phase, m.message = dedicatedManageError, "Ready response does not match this server and VPS fingerprint."
		return false
	}
	copy := *r
	copy.TransitionNonce = append([]byte(nil), r.TransitionNonce...)
	m.readiness, m.phase, m.message = &copy, dedicatedManageReady, "Ready. Verify the exact fingerprint and transition nonce, then activate."
	return true
}
func (m *dedicatedManager) beginActivation() (uint64, bool) {
	if m.phase != dedicatedManageReady || m.readiness == nil {
		return 0, false
	}
	m.operation++
	return m.operation, true
}

func sameDedicatedReadiness(a, b *proto.DedicatedReadiness) bool {
	return a != nil && b != nil &&
		a.ServerID == b.ServerID &&
		a.DestinationFingerprint == b.DestinationFingerprint &&
		a.ChainHead == b.ChainHead &&
		bytes.Equal(a.ChainHeadHash, b.ChainHeadHash) &&
		a.HistoryLastID == b.HistoryLastID &&
		a.HistoryLastUnixMilli == b.HistoryLastUnixMilli &&
		a.HistoryCount == b.HistoryCount &&
		bytes.Equal(a.HistoryHash, b.HistoryHash) &&
		bytes.Equal(a.TransitionNonce, b.TransitionNonce)
}

func (m *dedicatedManager) activation(gen, operation uint64, r *proto.DedicatedResult, err error) bool {
	if gen != m.gen || operation != m.operation || m.phase != dedicatedManageReady || m.readiness == nil {
		return false
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			m.phase, m.message = dedicatedManageRecovery, "Activation outcome is unknown. Check Ready and retry only this exact nonce."
		} else {
			m.phase, m.message = dedicatedManageRecovery, "Activation outcome is unknown. Recheck the exact Ready boundary before retrying."
		}
		return false
	}
	if r == nil {
		m.phase, m.message = dedicatedManageRecovery, "Activation outcome is unknown. Recheck the exact Ready boundary."
		return false
	}
	switch r.Status {
	case "success":
		if !sameDedicatedReadiness(r.Readiness, m.readiness) {
			m.phase, m.message = dedicatedManageRecovery, "Activation response did not match the displayed Ready boundary. Check Ready again."
			return false
		}
		m.phase, m.message = dedicatedManageDone, "Activation requested. The source is fenced for this exact transition."
	case "pending":
		m.phase, m.message = dedicatedManagePending, "VPS is not Ready yet. Check readiness again."
	case "retry":
		m.phase, m.message = dedicatedManageRetry, "Activation must be retried after checking the exact Ready boundary."
	default:
		m.phase, m.message = dedicatedManageError, "Activation was rejected. Recheck the VPS preparation and founder session."
	}
	return true
}
func (m *dedicatedManager) nonce() string {
	if m.readiness == nil {
		return ""
	}
	return fmt.Sprintf("%x", m.readiness.TransitionNonce)
}
