package proto

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/medeirosvictor/hermec/core/identity"
)

// Bounds for the dedicated-hosting readiness exchange. Keeping this small
// makes the pre-activation exchange safe to retain and safe to put on the wire.
const (
	DedicatedTransitionNonceSize = 32
	MaxDedicatedServerID         = 128
	MaxDedicatedFingerprint      = 64
	MaxDedicatedHistoryID        = 128
)

const dedicatedReadinessDomain = "hermec-dedicated-readiness-v1\x00"

// DedicatedReadiness is the public, replay-resistant statement a prepared
// destination signs. ChainHead is the signed-chain sequence; HistoryLastID
// and HistoryLastUnixMilli name the last transferred history record.
type DedicatedReadiness struct {
	ServerID               string `json:"server_id"`
	DestinationFingerprint string `json:"destination_fingerprint"`
	ChainHead              uint64 `json:"chain_head"`
	ChainHeadHash          []byte `json:"chain_head_hash"`
	HistoryLastID          string `json:"history_last_id,omitempty"`
	HistoryLastUnixMilli   int64  `json:"history_last_unix_milli,omitempty"`
	HistoryCount           uint64 `json:"history_count"`
	HistoryHash            []byte `json:"history_hash"`
	TransitionNonce        []byte `json:"transition_nonce"`
}

// Validate rejects oversized or incomplete readiness statements before they
// reach signature verification or persistence.
func (r DedicatedReadiness) Validate() error {
	if r.ServerID == "" || len(r.ServerID) > MaxDedicatedServerID {
		return errors.New("proto: invalid dedicated server ID")
	}
	if r.DestinationFingerprint == "" || len(r.DestinationFingerprint) > MaxDedicatedFingerprint {
		return errors.New("proto: invalid dedicated destination fingerprint")
	}
	if len(r.HistoryLastID) > MaxDedicatedHistoryID {
		return errors.New("proto: dedicated history ID too long")
	}
	if len(r.ChainHeadHash) != sha256.Size {
		return errors.New("proto: invalid dedicated chain head hash")
	}
	if len(r.HistoryHash) != sha256.Size {
		return errors.New("proto: invalid dedicated history hash")
	}
	if len(r.TransitionNonce) != DedicatedTransitionNonceSize {
		return fmt.Errorf("proto: dedicated transition nonce must be %d bytes", DedicatedTransitionNonceSize)
	}
	return nil
}

// DedicatedReadinessBytes returns the unambiguous, domain-separated bytes
// signed by a prepared destination. It deliberately has no JSON dependency so
// different encoders cannot change what is attested.
func DedicatedReadinessBytes(r DedicatedReadiness) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	out := append([]byte(dedicatedReadinessDomain), r.TransitionNonce...)
	put := func(s string) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		out = append(out, n[:]...)
		out = append(out, s...)
	}
	put(r.ServerID)
	put(r.DestinationFingerprint)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], r.ChainHead)
	out = append(out, n[:]...)
	out = append(out, r.ChainHeadHash...)
	binary.BigEndian.PutUint64(n[:], uint64(r.HistoryLastUnixMilli))
	out = append(out, n[:]...)
	put(r.HistoryLastID)
	binary.BigEndian.PutUint64(n[:], r.HistoryCount)
	out = append(out, n[:]...)
	out = append(out, r.HistoryHash...)
	return out, nil
}

// DedicatedHistoryCommitment returns an immutable, ordered commitment to a
// complete history snapshot. Each JSON message is length-delimited so distinct
// snapshots cannot share an ambiguous byte representation.
func DedicatedHistoryCommitment(msgs []ChatMessage) (uint64, []byte) {
	h := sha256.New()
	var n [8]byte
	for _, m := range msgs {
		b, _ := json.Marshal(m)
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	return uint64(len(msgs)), h.Sum(nil)
}

// SignDedicatedReadiness signs a validated readiness statement with the
// destination host key.
func SignDedicatedReadiness(key *identity.Identity, r DedicatedReadiness) ([]byte, error) {
	if key == nil {
		return nil, errors.New("proto: nil dedicated readiness key")
	}
	b, err := DedicatedReadinessBytes(r)
	if err != nil {
		return nil, err
	}
	return key.Sign(b), nil
}

// VerifyDedicatedReadiness verifies a destination readiness signature.
func VerifyDedicatedReadiness(pub ed25519.PublicKey, r DedicatedReadiness, sig []byte) bool {
	// The key that verifies the proof must be the destination named in the
	// signed statement; otherwise a valid proof from any key could be relayed.
	if subtle.ConstantTimeCompare([]byte(identity.Fingerprint(pub)), []byte(r.DestinationFingerprint)) != 1 {
		return false
	}
	b, err := DedicatedReadinessBytes(r)
	return err == nil && identity.Verify(pub, b, sig)
}

// DedicatedReadinessProof is the bounded wire payload used during VPS
// preparation. The public key is intentionally absent: the authorized host
// key is already known from the signed chain.
type DedicatedReadinessProof struct {
	Readiness DedicatedReadiness `json:"readiness"`
	Signature []byte             `json:"signature"`
}

// DedicatedPrepare gives the passive destination the source-captured boundary
// it must attest. It is meaningful only on an authenticated host link.
type DedicatedPrepare struct {
	Readiness DedicatedReadiness `json:"readiness"`
}

// DedicatedAck is sent only after the active source verified a proof. Keeping
// the acknowledgement explicit prevents a destination crash/restart from
// mistaking a locally generated proof for a completed preparation.
type DedicatedAck struct {
	Readiness DedicatedReadiness `json:"readiness"`
}

// Validate checks all bounded proof fields.
func (p DedicatedReadinessProof) Validate() error {
	if err := p.Readiness.Validate(); err != nil {
		return err
	}
	if len(p.Signature) != ed25519.SignatureSize {
		return errors.New("proto: invalid dedicated readiness signature")
	}
	return nil
}
