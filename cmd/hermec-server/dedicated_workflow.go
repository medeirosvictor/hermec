package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server"
)

// dedicatedHostPublic writes only public identity material. In particular it
// never reads or prints the seed stored in keyPath.
func dedicatedHostPublic(keyPath string, out io.Writer) error {
	key, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	pub := key.PublicKey()
	fmt.Fprintf(out, "host public key: %s\nhost fingerprint: %s\n", base64.RawStdEncoding.EncodeToString(pub), identity.Fingerprint(pub))
	return nil
}

// dedicatedAttestationStatus and dedicatedAttestationReset operate on the
// active source sidecar. The local authorized host key is the operator credential;
// server-side checks also refuse any source that is prepared, fenced, or has a
// durable cutover certificate.
func dedicatedAttestationStatus(keyPath, chainPath string, reset bool, out io.Writer) error {
	if err := requirePrivateFile(keyPath); err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	if err := requirePrivateFile(chainPath); err != nil {
		return fmt.Errorf("signed chain: %w", err)
	}
	key, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	c, err := chain.Load(chainPath)
	if err != nil {
		return fmt.Errorf("signed chain: %w", err)
	}
	s := server.New(server.Config{HostKey: key, Chain: c, DedicatedTransitionPath: dedicatedTransitionPath(chainPath)})
	operator := identity.Fingerprint(key.PublicKey())
	if reset {
		lock, err := server.AcquireDedicatedReadinessLock(dedicatedTransitionPath(chainPath))
		if err != nil {
			return err
		}
		defer lock.Close()
		if err := s.ResetDedicatedReadinessAttestation(operator); err != nil {
			return err
		}
		fmt.Fprintln(out, "dedicated readiness attestation: reset")
		fmt.Fprintf(out, "recovery: %s\n", server.DedicatedRecoveryRetryPreparation.Guidance())
		return nil
	}
	ready, err := s.InspectDedicatedReadinessAttestation(operator)
	if err != nil {
		return err
	}
	if ready == nil {
		fmt.Fprintln(out, "dedicated readiness attestation: none")
	} else {
		fmt.Fprintf(out, "dedicated readiness attestation: present\ndestination fingerprint: %s\ntransition nonce: %x\n", ready.DestinationFingerprint, ready.TransitionNonce)
	}
	fmt.Fprintf(out, "recovery: %s\n", server.DedicatedRecoveryRetryPreparation.Guidance())
	return nil
}

func dedicatedStatus(_ string, keyPath, chainPath, historyPath string, out io.Writer) error {
	r, err := server.LoadDedicatedTransition(dedicatedTransitionPath(chainPath))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "dedicated status: none\nrecovery: Keep the current host active; retry preparation.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("dedicated transition: %w", err)
	}
	if err := validateDedicatedRecord(r, keyPath, chainPath, historyPath); err != nil {
		return fmt.Errorf("dedicated transition is invalid: %w", err)
	}
	fmt.Fprintf(out, "dedicated status: %s\ndestination fingerprint: %s\ntransition nonce: %x\nrecovery: %s\n", r.State, r.DestinationFingerprint, r.TransitionNonce, r.Recovery.Guidance())
	return nil
}

func validateDedicatedRecord(r server.DedicatedTransitionRecord, keyPath, chainPath, historyPath string) error {
	key, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	c, err := chain.Load(chainPath)
	if err != nil {
		return fmt.Errorf("signed chain: %w", err)
	}
	h, err := server.OpenHistory(historyPath)
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	defer h.Close()
	switch r.State {
	case server.DedicatedTransitionPreparing:
		return server.ValidateDedicatedPreparing(r, key, c, h)
	case server.DedicatedTransitionPrepared:
		return server.ValidateDedicatedPrepared(r, key, c, h)
	case server.DedicatedTransitionCommitted:
		return server.ValidateDedicatedCommitted(r, key, c, h)
	case server.DedicatedTransitionFenced:
		return server.ValidateDedicatedFenced(r, key, c, h)
	default:
		return errors.New("unknown dedicated transition state")
	}
}

// runDedicatedPreparation performs one bounded, passive preparation attempt.
// It is deliberately not a service: after the durable record is written the
// normal service can be started in prepared mode to receive a later commit.
func runDedicatedPreparation(ctx context.Context, cfg server.Config, keyPath, chainPath, historyPath, activeURL, serverID string, retry bool, out io.Writer) error {
	if !retry {
		if err := validateDedicatedPrepareInputs(activeURL, serverID); err != nil {
			return err
		}
	} else {
		r, err := server.LoadDedicatedTransition(dedicatedTransitionPath(chainPath))
		if err != nil {
			return fmt.Errorf("dedicated retry requires an existing transition: %w", err)
		}
		if r.State != server.DedicatedTransitionPreparing && r.State != server.DedicatedTransitionPrepared {
			return fmt.Errorf("dedicated retry refuses %s transition", r.State)
		}
		if err := validateDedicatedRecord(r, keyPath, chainPath, historyPath); err != nil {
			return err
		}
		if r.ActiveWSSURL == "" {
			return errors.New("dedicated retry record has no pinned active URL; do not recreate it; inspect the active host")
		}
		activeURL, serverID = r.ActiveWSSURL, r.ServerID
	}
	if err := requirePrivateFile(keyPath); err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	if err := requirePrivateFile(chainPath); err != nil {
		return fmt.Errorf("signed chain: %w", err)
	}
	if err := requirePrivateFile(historyPath); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	key, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	bootstrap, err := chain.Load(chainPath)
	if err != nil {
		return fmt.Errorf("signed chain: %w", err)
	}
	transitionPath := dedicatedTransitionPath(chainPath)
	if !retry {
		if _, err := os.Lstat(transitionPath); err == nil {
			return errors.New("dedicated preparation refuses existing transition state; inspect it with -dedicated-status or use -dedicated-retry")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("dedicated transition record: %w", err)
		}
		bootstrap, err = server.AuthenticateDedicatedBootstrap(ctx, activeURL, serverID, bootstrap, key, chainPath)
		if err != nil {
			return err
		}
	}
	cfg.HostKey, cfg.Chain, cfg.HistoryPath = key, bootstrap, historyPath
	cfg.Prepared, cfg.Mode = true, server.ModePrepared
	cfg.DedicatedTransitionPath = transitionPath
	dst := server.New(cfg)
	if err := dst.Start(); err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = dst.Shutdown(shutdown)
	}()
	var readiness *proto.DedicatedReadiness
	if retry {
		r, _ := server.LoadDedicatedTransition(cfg.DedicatedTransitionPath)
		readiness = &proto.DedicatedReadiness{ServerID: r.ServerID, DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, ChainHeadHash: r.ChainHeadHash, HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, HistoryCount: r.HistoryCount, HistoryHash: r.HistoryHash, TransitionNonce: r.TransitionNonce}
	}
	link, err := server.DialDedicatedLink(ctx, activeURL, dst, readiness)
	if err != nil {
		return err
	}
	defer link.Close()
	go link.Run()
	if err := link.PersistDedicatedPreparationAt(ctx, cfg.DedicatedTransitionPath, activeURL); err != nil {
		return err
	}
	fmt.Fprintln(out, "dedicated status: prepared")
	fmt.Fprintln(out, "next: start the dedicated service; it remains passive until the founder explicitly commits this exact transition.")
	fmt.Fprintln(out, "next: use -dedicated-status to inspect it; use -dedicated-retry only after an interrupted preparation.")
	return nil
}

// validateDedicatedPrepareInputs is intentionally credential-free. Preparation
// authenticates as this VPS host identity; passwords and founder/member keys
// are neither accepted nor read by this command.
func validateDedicatedPrepareInputs(rawURL, serverID string) error {
	if _, err := server.CanonicalDedicatedWSS(rawURL); err != nil {
		return fmt.Errorf("dedicated preparation requires a pinned canonical wss:// active-host URL: %w", err)
	}
	id, err := chain.ParseID(serverID)
	if err != nil || id.String() != serverID {
		return fmt.Errorf("dedicated preparation requires a valid canonical pinned server ID")
	}
	return nil
}
