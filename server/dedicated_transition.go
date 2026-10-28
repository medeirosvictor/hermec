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
	"strings"

	"github.com/medeirosvictor/hermec/core/proto"
)

// DedicatedTransitionState records recovery-safe progress only. It never
// contains credentials or private key material.
type DedicatedTransitionState string

const (
	DedicatedTransitionPrepared  DedicatedTransitionState = "prepared"
	DedicatedTransitionCommitted DedicatedTransitionState = "committed"
)

// DedicatedTransitionRecord is the local, restart-safe record for a VPS
// transition. All fields are public identifiers, transfer boundaries, state,
// or operator-facing recovery text. It intentionally has no credential or
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
	Recovery               string                   `json:"recovery"`
}

const maxDedicatedRecoveryText = 1024

func (r DedicatedTransitionRecord) validate() error {
	if r.Version != 1 {
		return errors.New("server: unsupported dedicated transition version")
	}
	if r.State != DedicatedTransitionPrepared && r.State != DedicatedTransitionCommitted {
		return errors.New("server: invalid dedicated transition state")
	}
	if len(r.Recovery) == 0 || len(r.Recovery) > maxDedicatedRecoveryText || strings.ContainsRune(r.Recovery, '\x00') {
		return errors.New("server: invalid dedicated transition recovery text")
	}
	lower := strings.ToLower(r.Recovery)
	for _, secret := range []string{"password", "private key", "private_key", "password_key", "ed25519 seed"} {
		if strings.Contains(lower, secret) {
			return errors.New("server: dedicated transition recovery text must not contain credentials")
		}
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
	info, err := os.Stat(path)
	if err != nil {
		return DedicatedTransitionRecord{}, err
	}
	if !info.Mode().IsRegular() {
		return DedicatedTransitionRecord{}, errors.New("server: dedicated transition is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return DedicatedTransitionRecord{}, errors.New("server: dedicated transition is not private")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DedicatedTransitionRecord{}, err
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
