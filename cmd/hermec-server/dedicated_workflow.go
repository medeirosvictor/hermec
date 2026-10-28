package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
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

func dedicatedStatus(dir string, out io.Writer) error {
	r, err := server.LoadDedicatedTransition(filepath.Join(dir, server.DedicatedTransitionFile))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "dedicated status: none\nrecovery: Keep the current host active; retry preparation.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("dedicated transition: %w", err)
	}
	fmt.Fprintf(out, "dedicated status: %s\ndestination fingerprint: %s\nrecovery: %s\n", r.State, r.DestinationFingerprint, r.Recovery.Guidance())
	return nil
}

// validateDedicatedPrepareInputs is intentionally credential-free. Preparation
// authenticates as this VPS host identity; passwords and founder/member keys
// are neither accepted nor read by this command.
func validateDedicatedPrepareInputs(rawURL, serverID string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "wss" || u.Host == "" {
		return errors.New("dedicated preparation requires a pinned wss:// active-host URL")
	}
	if _, err := chain.ParseID(strings.TrimSpace(serverID)); err != nil {
		return fmt.Errorf("dedicated preparation requires a valid pinned server ID: %w", err)
	}
	return nil
}
