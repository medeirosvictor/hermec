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
	MaxDedicatedHostName         = 128
	MaxDedicatedHostAddrs        = 16
	MaxDedicatedHostAddr         = 512
)

const dedicatedReadinessDomain = "hermec-dedicated-readiness-v1\x00"
const dedicatedCutoverDomain = "hermec-dedicated-cutover-v1\x00"

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

// ValidateDedicatedHostInfo rejects unbounded HostInfo fields before they are
// signed or persisted in a cutover certificate.
func ValidateDedicatedHostInfo(h HostInfo) error {
	if h.Fingerprint == "" || len(h.Fingerprint) > MaxDedicatedFingerprint {
		return errors.New("proto: invalid dedicated host fingerprint")
	}
	if len(h.Name) > MaxDedicatedHostName || len(h.Addrs) > MaxDedicatedHostAddrs {
		return errors.New("proto: invalid dedicated host info")
	}
	for _, a := range h.Addrs {
		if a == "" || len(a) > MaxDedicatedHostAddr {
			return errors.New("proto: invalid dedicated host address")
		}
	}
	return nil
}

// DedicatedCertificate is the source's one-use, signed authority to activate
// a prepared destination. It binds both host identities, the exact mirrored
// state, nonce, and the address clients must use. It is deliberately not a
// generic host promotion token.
type DedicatedCertificate struct {
	Readiness         DedicatedReadiness `json:"readiness"`
	SourceFingerprint string             `json:"source_fingerprint"`
	Destination       HostInfo           `json:"destination"`
	Signature         []byte             `json:"signature"`
}

// DedicatedCutover signs an unambiguous certificate body using the active
// source's host key.
func SignDedicatedCutover(key *identity.Identity, r DedicatedReadiness, source string, destination HostInfo) ([]byte, error) {
	if key == nil || source == "" || len(source) > MaxDedicatedFingerprint || destination.Fingerprint != r.DestinationFingerprint {
		return nil, errors.New("proto: invalid dedicated cutover identities")
	}
	if err := ValidateDedicatedHostInfo(destination); err != nil {
		return nil, err
	}
	b, err := DedicatedReadinessBytes(r)
	if err != nil {
		return nil, err
	}
	b = append([]byte(dedicatedCutoverDomain), b...)
	for _, s := range []string{source, destination.Fingerprint, destination.Name} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		b = append(b, n[:]...)
		b = append(b, s...)
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(destination.Addrs)))
	b = append(b, n[:]...)
	for _, a := range destination.Addrs {
		binary.BigEndian.PutUint32(n[:], uint32(len(a)))
		b = append(b, n[:]...)
		b = append(b, a...)
	}
	var p [8]byte
	binary.BigEndian.PutUint64(p[:], uint64(destination.Priority))
	b = append(b, p[:]...)
	return key.Sign(b), nil
}

// VerifyDedicatedCutover verifies a certificate against the already authorized
// source host key; the caller must additionally ensure that key is authorized.
func VerifyDedicatedCutover(pub ed25519.PublicKey, c DedicatedCertificate) bool {
	if ValidateDedicatedHostInfo(c.Destination) != nil || c.SourceFingerprint == "" || len(c.SourceFingerprint) > MaxDedicatedFingerprint || c.SourceFingerprint != identity.Fingerprint(pub) || c.Destination.Fingerprint != c.Readiness.DestinationFingerprint || len(c.Signature) != ed25519.SignatureSize {
		return false
	}
	// Recreate the unsigned bytes without possessing a private key.
	b, err := DedicatedReadinessBytes(c.Readiness)
	if err != nil {
		return false
	}
	b = append([]byte(dedicatedCutoverDomain), b...)
	for _, s := range []string{c.SourceFingerprint, c.Destination.Fingerprint, c.Destination.Name} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		b = append(b, n[:]...)
		b = append(b, s...)
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(c.Destination.Addrs)))
	b = append(b, n[:]...)
	for _, a := range c.Destination.Addrs {
		binary.BigEndian.PutUint32(n[:], uint32(len(a)))
		b = append(b, n[:]...)
		b = append(b, a...)
	}
	var p [8]byte
	binary.BigEndian.PutUint64(p[:], uint64(c.Destination.Priority))
	b = append(b, p[:]...)
	return identity.Verify(pub, b, c.Signature)
}

// DedicatedCommit is sent only after the source has durably fenced itself.
type DedicatedCommit struct {
	Certificate DedicatedCertificate `json:"certificate"`
}

// DedicatedAck is the idempotent destination receipt sent only after its
// committed record is durable.
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
