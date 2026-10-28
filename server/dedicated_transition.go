package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// DedicatedTransitionState records recovery-safe progress only. It never
// contains credentials or private key material.
type DedicatedTransitionState string

const (
	DedicatedTransitionPrepared  DedicatedTransitionState = "prepared"
	DedicatedTransitionCommitted DedicatedTransitionState = "committed"
	DedicatedTransitionFenced    DedicatedTransitionState = "fenced"
)

// DedicatedRecoveryCode selects fixed operator guidance. It is persisted
// instead of caller-supplied text so transition records cannot leak secrets.
type DedicatedRecoveryCode string

const (
	DedicatedRecoveryRetryPreparation DedicatedRecoveryCode = "retry_preparation"
	DedicatedRecoveryInspectCommit    DedicatedRecoveryCode = "inspect_commit"
)

// Guidance returns the fixed, safe operator instruction for this code.
func (c DedicatedRecoveryCode) Guidance() string {
	switch c {
	case DedicatedRecoveryRetryPreparation:
		return "Keep the current host active; retry preparation."
	case DedicatedRecoveryInspectCommit:
		return "Keep the current host active; inspect the committed transition before retrying."
	default:
		return ""
	}
}

// DedicatedTransitionRecord is the local, restart-safe record for a VPS
// transition. All fields are public identifiers, transfer boundaries, state,
// or a fixed recovery code. It intentionally has no credential or
// filesystem-path fields.
type DedicatedTransitionRecord struct {
	Version                int                      `json:"version"`
	State                  DedicatedTransitionState `json:"state"`
	ServerID               string                   `json:"server_id"`
	DestinationFingerprint string                   `json:"destination_fingerprint"`
	ChainHead              uint64                   `json:"chain_head"`
	ChainHeadHash          []byte                   `json:"chain_head_hash"`
	HistoryLastID          string                   `json:"history_last_id,omitempty"`
	HistoryLastUnixMilli   int64                    `json:"history_last_unix_milli,omitempty"`
	HistoryCount           uint64                   `json:"history_count"`
	HistoryHash            []byte                   `json:"history_hash"`
	TransitionNonce        []byte                   `json:"transition_nonce"`
	// Certificate is retained by both sides. A fenced source retransmits this
	// exact object after restart; it never generates another nonce or boundary.
	Certificate *proto.DedicatedCertificate `json:"certificate,omitempty"`
	Recovery    DedicatedRecoveryCode       `json:"recovery"`
	// ActiveWSSURL is local, non-secret retry metadata for a prepared
	// destination. It is optional for records created before retry support.
	ActiveWSSURL string `json:"active_wss_url,omitempty"`
}

// DedicatedTransitionFile is the restart record kept beside a dedicated
// server's chain and history.
const DedicatedTransitionFile = "transition.json"

const maxDedicatedTransitionBytes = 16 << 10

// ValidateDedicatedPrepared verifies that a completed preparation record
// belongs to this local destination and that its captured boundary still
// exists locally. It never activates the destination; callers must keep a
// failed or stale record passive and show its fixed recovery guidance.
func ValidateDedicatedPrepared(r DedicatedTransitionRecord, local *identity.Identity, c *chain.Chain, h *History) error {
	if err := r.validate(); err != nil {
		return err
	}
	if r.State != DedicatedTransitionPrepared {
		return errors.New("server: dedicated transition is not prepared")
	}
	return validateDedicatedDestination(r, local, c, h)
}

// ValidateDedicatedCommitted validates the terminal destination state on
// startup. Unlike Prepared, this must never be accepted merely by changing a
// record's State in memory: its committed certificate is part of the state.
func ValidateDedicatedCommitted(r DedicatedTransitionRecord, local *identity.Identity, c *chain.Chain, h *History) error {
	if err := r.validate(); err != nil {
		return err
	}
	if r.State != DedicatedTransitionCommitted {
		return errors.New("server: dedicated transition is not committed")
	}
	return validateDedicatedDestination(r, local, c, h)
}

// ValidateDedicatedFenced validates the terminal source state on startup.
// The source intentionally is not the destination, so it is checked against
// the certificate signer rather than DestinationFingerprint.
func ValidateDedicatedFenced(r DedicatedTransitionRecord, local *identity.Identity, c *chain.Chain, h *History) error {
	if err := r.validate(); err != nil {
		return err
	}
	if r.State != DedicatedTransitionFenced {
		return errors.New("server: dedicated transition is not fenced")
	}
	if local == nil || r.Certificate == nil || r.Certificate.SourceFingerprint != identity.Fingerprint(local.PublicKey()) {
		return errors.New("server: fenced transition names another source")
	}
	return validateDedicatedLocalBoundary(r, c, h)
}

func validateDedicatedDestination(r DedicatedTransitionRecord, local *identity.Identity, c *chain.Chain, h *History) error {
	if local == nil || r.DestinationFingerprint != identity.Fingerprint(local.PublicKey()) {
		return errors.New("server: dedicated transition names another destination")
	}
	return validateDedicatedLocalBoundary(r, c, h)
}

// validateDedicatedLocalBoundary verifies the exact chain and history snapshot
// named by a transition. Fenced sources use it too: after fencing neither side
// may silently continue from a different boundary.
func validateDedicatedLocalBoundary(r DedicatedTransitionRecord, c *chain.Chain, h *History) error {
	if c == nil || h == nil {
		return errors.New("server: incomplete local dedicated state")
	}
	if r.ServerID != c.ID().String() {
		return errors.New("server: dedicated transition names another server")
	}
	head := c.Head()
	headHash := head.Hash()
	if head.Seq != r.ChainHead || !bytes.Equal(headHash[:], r.ChainHeadHash) {
		return errors.New("server: dedicated transition chain differs from its boundary")
	}
	snapshot, _, _ := h.Snapshot()
	count, hash := proto.DedicatedHistoryCommitment(snapshot)
	if count != r.HistoryCount || !bytes.Equal(hash, r.HistoryHash) {
		return errors.New("server: dedicated transition history differs from its snapshot")
	}
	if r.HistoryLastID == "" {
		ts, id := h.Last()
		if id != "" || !ts.IsZero() {
			return errors.New("server: dedicated transition expected empty history")
		}
		return nil
	}
	if !h.Has(r.HistoryLastID, time.UnixMilli(r.HistoryLastUnixMilli)) {
		return errors.New("server: dedicated transition history boundary is missing")
	}
	return nil
}

func (r DedicatedTransitionRecord) validate() error {
	if r.Version != 1 {
		return errors.New("server: unsupported dedicated transition version")
	}
	if r.State != DedicatedTransitionPrepared && r.State != DedicatedTransitionCommitted && r.State != DedicatedTransitionFenced {
		return errors.New("server: invalid dedicated transition state")
	}
	if r.Recovery.Guidance() == "" {
		return errors.New("server: invalid dedicated transition recovery code")
	}
	if r.ActiveWSSURL != "" {
		u, err := url.Parse(r.ActiveWSSURL)
		if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return errors.New("server: invalid dedicated active URL")
		}
	}
	if err := (proto.DedicatedReadiness{ServerID: r.ServerID, DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, ChainHeadHash: r.ChainHeadHash, HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, HistoryCount: r.HistoryCount, HistoryHash: r.HistoryHash, TransitionNonce: r.TransitionNonce}).Validate(); err != nil {
		return err
	}
	if r.State == DedicatedTransitionCommitted || r.State == DedicatedTransitionFenced {
		if r.Certificate == nil || !sameReadiness(r.Certificate.Readiness, proto.DedicatedReadiness{ServerID: r.ServerID, DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, ChainHeadHash: r.ChainHeadHash, HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, HistoryCount: r.HistoryCount, HistoryHash: r.HistoryHash, TransitionNonce: r.TransitionNonce}) || proto.ValidateDedicatedHostInfo(r.Certificate.Destination) != nil {
			return errors.New("server: missing or mismatched dedicated certificate")
		}
	}
	return nil
}

// SaveDedicatedTransition atomically replaces path with a mode-0600 JSON
// record. The temporary file and rename are in the same directory, so a crash
// leaves either the prior complete record or the new complete record.
func SaveDedicatedTransition(path string, r DedicatedTransitionRecord) error {
	if err := r.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("server: encode dedicated transition: %w", err)
	}
	if len(data) > maxDedicatedTransitionBytes {
		return errors.New("server: dedicated transition is too large")
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".hermec-transition-")
	if err != nil {
		return fmt.Errorf("server: create dedicated transition: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("server: replace dedicated transition: %w", err)
	}
	// Persist the directory entry too where the platform permits it.
	d, err := os.Open(dir)
	if err != nil {
		if runtime.GOOS != "windows" {
			return fmt.Errorf("server: open dedicated transition directory: %w", err)
		}
		return nil
	}
	err = d.Sync()
	_ = d.Close()
	if err != nil && runtime.GOOS != "windows" {
		return fmt.Errorf("server: sync dedicated transition directory: %w", err)
	}
	return nil
}

// LoadDedicatedTransition loads a complete private transition record. On
// POSIX it rejects a record readable by group or other users.
func LoadDedicatedTransition(path string) (DedicatedTransitionRecord, error) {
	// Lstat rejects the common symlink attack. Opening once and reading that
	// descriptor avoids the stat/read replacement race; SameFile also rejects
	// replacement between Lstat and Open where the platform can identify it.
	before, err := os.Lstat(path)
	if err != nil {
		return DedicatedTransitionRecord{}, err
	}
	if !before.Mode().IsRegular() {
		return DedicatedTransitionRecord{}, errors.New("server: dedicated transition is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return DedicatedTransitionRecord{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return DedicatedTransitionRecord{}, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return DedicatedTransitionRecord{}, errors.New("server: dedicated transition changed while opening")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return DedicatedTransitionRecord{}, errors.New("server: dedicated transition is not private")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDedicatedTransitionBytes+1))
	if err != nil {
		return DedicatedTransitionRecord{}, err
	}
	if len(data) > maxDedicatedTransitionBytes {
		return DedicatedTransitionRecord{}, errors.New("server: dedicated transition is too large")
	}
	var r DedicatedTransitionRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		if err == nil {
			err = errors.New("trailing data")
		}
		return DedicatedTransitionRecord{}, fmt.Errorf("server: decode dedicated transition: %w", err)
	}
	if err := r.validate(); err != nil {
		return DedicatedTransitionRecord{}, err
	}
	return r, nil
}
