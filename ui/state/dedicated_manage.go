package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// ManagePhase is the step of the founder's dedicated-host manager, which is
// intentionally separate from the end-user dedicated join form. Nothing in this
// state is saved: a VPS key and cutover nonce are useful only for this open,
// authenticated session.
type ManagePhase int

const (
	ManageKey ManagePhase = iota
	ManageConfirm
	ManageAuthorizing
	ManagePending
	ManageReady
	ManageRetry
	ManageRecovery
	ManageDone
	ManageError
)

// DedicatedManager is the founder manager state machine. The UI feeds it
// the key text and async results; stale results (older Gen or operation) are
// ignored by every transition.
type DedicatedManager struct {
	Phase                ManagePhase
	Fingerprint, Message string
	Readiness            *proto.DedicatedReadiness
	Gen                  uint64
	pub                  []byte
	serverID             string
	operation            uint64
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

func (m *DedicatedManager) Open(serverID string, founder bool) bool {
	m.Close()
	if !founder {
		return false
	}
	m.serverID, m.Phase = serverID, ManageKey
	m.Gen++
	return true
}
func (m *DedicatedManager) Close() {
	m.pub = nil
	m.Fingerprint = ""
	m.serverID = ""
	m.Message = ""
	m.Readiness = nil
	m.Phase = ManageKey
	m.Gen++
}

// keyChanged discards every value derived from the editable public key. The
// authorization UI may only display a fingerprint derived from the visible key.
func (m *DedicatedManager) KeyChanged() {
	m.pub = nil
	m.Fingerprint = ""
	m.Readiness = nil
	m.operation++
	m.Phase = ManageKey
	m.Message = ""
}

func (m *DedicatedManager) Parse(keyText string) bool {
	pub, fp, err := client.ParseDedicatedHostPublicKey(keyText)
	if err != nil {
		m.pub = nil
		m.Fingerprint = ""
		m.Phase = ManageError
		m.Message = "Enter the VPS public key printed by hermec-server."
		return false
	}
	m.pub, m.Fingerprint, m.Phase, m.Message = pub, fp, ManageConfirm, "Confirm authorization for this fingerprint."
	return true
}
func (m *DedicatedManager) Confirm(hosts []chain.Host) ([]byte, int, uint64, bool) {
	if m.Phase != ManageConfirm || len(m.pub) != 32 {
		return nil, 0, 0, false
	}
	m.operation++
	m.Phase, m.Message = ManageAuthorizing, "Authorizing VPS host…"
	return append([]byte(nil), m.pub...), maxDedicatedPriority(hosts), m.operation, true
}
func (m *DedicatedManager) Authorized(gen, operation uint64, err error) bool {
	if gen != m.Gen || operation != m.operation || m.Phase != ManageAuthorizing {
		return false
	}
	if err != nil {
		m.Phase, m.Message = ManageError, "Could not authorize the VPS host. Check this founder session and try again."
		return false
	}
	m.Phase, m.Message = ManagePending, "Host authorized. Waiting for the VPS to prepare…"
	return true
}
func (m *DedicatedManager) BeginReady() (uint64, bool) {
	if m.Phase != ManagePending && m.Phase != ManageRetry && m.Phase != ManageRecovery {
		return 0, false
	}
	m.operation++
	return m.operation, true
}

func (m *DedicatedManager) Ready(gen, operation uint64, r *proto.DedicatedReadiness, err error) bool {
	if gen != m.Gen || operation != m.operation || (m.Phase != ManagePending && m.Phase != ManageRetry && m.Phase != ManageRecovery) {
		return false
	}
	if err != nil {
		m.Phase, m.Message = ManagePending, "VPS is not Ready yet. Start or retry its preparation, then check again."
		return false
	}
	if r == nil || r.Validate() != nil || r.ServerID != m.serverID || r.DestinationFingerprint != m.Fingerprint {
		m.Phase, m.Message = ManageError, "Ready response does not match this server and VPS fingerprint."
		return false
	}
	copy := *r
	copy.TransitionNonce = append([]byte(nil), r.TransitionNonce...)
	m.Readiness, m.Phase, m.Message = &copy, ManageReady, "Ready. Verify the exact fingerprint and transition nonce, then activate."
	return true
}
func (m *DedicatedManager) BeginActivation() (uint64, bool) {
	if m.Phase != ManageReady || m.Readiness == nil {
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

func (m *DedicatedManager) Activation(gen, operation uint64, r *proto.DedicatedResult, err error) bool {
	if gen != m.Gen || operation != m.operation || m.Phase != ManageReady || m.Readiness == nil {
		return false
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			m.Phase, m.Message = ManageRecovery, "Activation outcome is unknown. Check Ready and retry only this exact nonce."
		} else {
			m.Phase, m.Message = ManageRecovery, "Activation outcome is unknown. Recheck the exact Ready boundary before retrying."
		}
		return false
	}
	if r == nil {
		m.Phase, m.Message = ManageRecovery, "Activation outcome is unknown. Recheck the exact Ready boundary."
		return false
	}
	switch r.Status {
	case "success":
		if !sameDedicatedReadiness(r.Readiness, m.Readiness) {
			m.Phase, m.Message = ManageRecovery, "Activation response did not match the displayed Ready boundary. Check Ready again."
			return false
		}
		m.Phase, m.Message = ManageDone, "Activation requested. The source is fenced for this exact transition."
	case "pending":
		m.Phase, m.Message = ManagePending, "VPS is not Ready yet. Check readiness again."
	case "retry":
		m.Phase, m.Message = ManageRetry, "Activation must be retried after checking the exact Ready boundary."
	default:
		m.Phase, m.Message = ManageError, "Activation was rejected. Recheck the VPS preparation and founder session."
	}
	return true
}
func (m *DedicatedManager) Nonce() string {
	if m.Readiness == nil {
		return ""
	}
	return fmt.Sprintf("%x", m.Readiness.TransitionNonce)
}
