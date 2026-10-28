package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/medeirosvictor/hermec/core/proto"
)

// DedicatedTransitionState records recovery-safe progress only. It never
// contains credentials or private key material.
type DedicatedTransitionState string

const (
	DedicatedTransitionPrepared  DedicatedTransitionState = "prepared"
	DedicatedTransitionCommitted DedicatedTransitionState = "committed"
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
	HistoryLastID          string                   `json:"history_last_id,omitempty"`
	HistoryLastUnixMilli   int64                    `json:"history_last_unix_milli,omitempty"`
	TransitionNonce        []byte                   `json:"transition_nonce"`
	Recovery               DedicatedRecoveryCode    `json:"recovery"`
}

const maxDedicatedTransitionBytes = 16 << 10

func (r DedicatedTransitionRecord) validate() error {
	if r.Version != 1 {
		return errors.New("server: unsupported dedicated transition version")
	}
	if r.State != DedicatedTransitionPrepared && r.State != DedicatedTransitionCommitted {
		return errors.New("server: invalid dedicated transition state")
	}
	if r.Recovery.Guidance() == "" {
		return errors.New("server: invalid dedicated transition recovery code")
	}
	return (proto.DedicatedReadiness{ServerID: r.ServerID, DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, TransitionNonce: r.TransitionNonce}).Validate()
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
	if d, err := os.Open(dir); err == nil {
		err = d.Sync()
		_ = d.Close()
		if err != nil && runtime.GOOS != "windows" {
			return fmt.Errorf("server: sync dedicated transition directory: %w", err)
		}
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
