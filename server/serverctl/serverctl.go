// Package serverctl holds the server's operator-facing lifecycle: dedicated
// bootstrap, state checks and preparation workflows, public-listener
// validation, and chain/key setup. cmd/hermec-server parses flags and wires
// these together; keeping them here makes them testable without the CLI.
package serverctl

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
)

// OpenChain opens or creates the chain at path and warns about a torn tail.
func OpenChain(path string, key *identity.Identity, cfg server.Config) (*chain.Chain, bool, error) {
	c, created, err := server.OpenChain(path, key, cfg)
	if err != nil {
		return nil, false, err
	}
	if n := c.TornTail(); n > 0 {
		log.Printf("WARNING: %s ended in a torn entry (a crash during a write); dropped %d bytes and kept the %d entries before it",
			path, n, len(c.Entries()))
	}
	return c, created, nil
}

// ApplyConfigFile is -apply-config: it appends the group fields that the TOML
// at configPath sets (cfg is that file, loaded) to the chain at chainPath,
// signed with the key at keyPath, and prints one line per entry to out. It also
// serves an app host: pass the app's identity.key and chains/<id>.jsonl.
func ApplyConfigFile(configPath, keyPath, chainPath string, cfg server.Config, out io.Writer) error {
	// A running server holds the chain in memory; appending to the file
	// under it would fork the two.
	if err := checkNotRunning(cfg.Addr); err != nil {
		return err
	}
	key, err := LoadOrCreateKey(keyPath)
	if err != nil {
		return err
	}
	c, created, err := OpenChain(chainPath, key, cfg)
	if err != nil {
		return err
	}
	var applied []string
	if created { // a fresh chain was just built from the config
		for _, e := range c.Entries() {
			applied = append(applied, string(e.Op))
		}
	} else {
		set, err := server.GroupFieldsSet(configPath)
		if err != nil {
			return err
		}
		if applied, err = server.ApplyTOML(c, key, cfg, set); err != nil {
			return err
		}
	}
	for _, a := range applied {
		fmt.Fprintln(out, a)
	}
	fmt.Fprintf(out, "appended %d entries to %s\n", len(applied), chainPath)
	return nil
}

// CheckHostKey refuses to serve a chain with a key it does not list as a host:
// every client would reject the proof. keyPath only names the file in the error.
func CheckHostKey(keyPath string, c *chain.Chain, key *identity.Identity) error {
	if err := server.CheckHost(c, key); err != nil {
		return fmt.Errorf("%s: %w (wrong key file, or the chain belongs to another server)", keyPath, err)
	}
	return nil
}

// checkNotRunning refuses when something already listens on addr, which for
// -apply-config means a server is running on this chain. A port of 0 or none
// cannot collide, so the check is skipped.
func checkNotRunning(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" || port == "0" {
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("-apply-config: %s is in use; stop the running server first (changes apply when it starts): %w", addr, err)
	}
	return ln.Close()
}

// LoadOrCreateKey loads the host key at path, generating and saving one when
// the file does not exist.
func LoadOrCreateKey(path string) (*identity.Identity, error) {
	key, err := identity.Load(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if key, err = identity.Generate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := key.Save(path); err != nil {
		return nil, err
	}
	return key, nil
}
