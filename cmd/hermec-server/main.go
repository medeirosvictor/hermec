// Command hermec-server runs a Hermec server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/version"
)

func main() {
	f, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	} else if err != nil {
		os.Exit(2)
	}
	if f.showVersion {
		fmt.Printf("hermec-server %s\n", version.Version)
		return
	}
	if f.initDedicated != "" {
		if f.config != "" || f.applyConfig || f.printConfig || f.check || f.key != "" || f.chain != "" || f.history != "" {
			log.Fatal("-init-dedicated cannot be combined with config/state operations")
		}
		result, err := initDedicated(f.initDedicated)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("dedicated server initialized in %s\n", result.Dir)
		fmt.Printf("config: %s\n", result.Config)
		fmt.Printf("server id: %s\n", result.ServerID)
		fmt.Printf("generated join password (shown once): %s\n", result.Password)
		fmt.Println("install a trusted TLS certificate at the configured paths before starting")
		return
	}
	if f.applyConfig && f.config == "" {
		log.Fatal("-apply-config requires -config")
	}

	cfg := server.DefaultConfig()
	if f.config != "" {
		var err error
		if cfg, err = server.LoadConfig(f.config); err != nil {
			log.Fatal(err)
		}
	}

	if f.printConfig {
		shown := cfg
		shown.PasswordKey = nil
		if shown.Password != "" {
			shown.Password = "<redacted>"
		}
		out, err := shown.MarshalTOML()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(out))
		return
	}

	dir := "."
	if f.config != "" {
		dir = filepath.Dir(f.config)
	}
	if f.key == "" {
		f.key = filepath.Join(dir, "hermec-server.key")
	}
	if f.chain == "" {
		f.chain = filepath.Join(dir, "chain.jsonl")
	}
	if f.history == "" {
		f.history = filepath.Join(dir, "history.jsonl")
	}
	if f.dedicatedHostKey {
		if f.dedicatedStatus || f.dedicatedPrepare != "" || f.dedicatedRetry || f.dedicatedAttestationStatus || f.dedicatedAttestationReset {
			log.Fatal("-dedicated-host-key cannot be combined with another dedicated workflow flag")
		}
		if err := dedicatedHostPublic(f.key, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedPrepare != "" && f.dedicatedRetry {
		log.Fatal("-dedicated-prepare and -dedicated-retry cannot be combined")
	}
	if f.dedicatedAttestationStatus || f.dedicatedAttestationReset {
		if f.dedicatedStatus || f.dedicatedPrepare != "" || f.dedicatedRetry {
			log.Fatal("-dedicated-attestation-status/-dedicated-attestation-reset cannot be combined with another dedicated workflow flag")
		}
		if err := dedicatedAttestationStatus(f.key, f.chain, f.dedicatedAttestationReset, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedStatus {
		if f.dedicatedPrepare != "" || f.dedicatedRetry {
			log.Fatal("-dedicated-status cannot be combined with prepare or retry")
		}
		if err := dedicatedStatus(dir, f.key, f.chain, f.history, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedPrepare != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := runDedicatedPreparation(ctx, cfg, f.key, f.chain, f.history, f.dedicatedPrepare, f.dedicatedServerID, false, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedRetry {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := runDedicatedPreparation(ctx, cfg, f.key, f.chain, f.history, "", "", true, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.applyConfig {
		if err := applyConfigFile(f.config, f.key, f.chain, cfg, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := validatePublicConfig(cfg, f.unsafePublicWS); err != nil {
		log.Fatal(err)
	}
	public := cfg.PublicMode || publicListener(cfg.Addr)
	if public && f.unsafePublicWS {
		log.Print("WARNING: unsafe public mode enabled; TLS/admission checks are bypassed")
	}
	if f.check || (public && !f.unsafePublicWS) {
		if err := checkDedicatedState(cfg, f.key, f.chain, f.history, os.Stdout); err != nil {
			log.Fatal(err)
		}
		if f.check {
			return
		}
	}
	key, err := loadOrCreateKey(f.key)
	if err != nil {
		log.Fatal(err)
	}
	c, created, err := openChain(f.chain, key, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := checkHostKey(f.key, c, key); err != nil {
		log.Fatal(err)
	}
	// A surviving preparation record is never permission to activate. Load it
	// before constructing Host, validate its local boundary, and keep even a
	// malformed or stale record passive with fixed recovery guidance.
	transitionPath := dedicatedTransitionPath(f.chain)
	if _, err := os.Lstat(transitionPath); err == nil {
		// Any unreadable or stale journal fails closed as a passive host.
		cfg.Prepared, cfg.Mode = true, server.ModePrepared
		r, loadErr := server.LoadDedicatedTransition(transitionPath)
		if loadErr == nil {
			var h *server.History
			h, loadErr = server.OpenHistory(f.history)
			if loadErr == nil {
				switch r.State {
				case server.DedicatedTransitionPreparing:
					loadErr = server.ValidateDedicatedPreparing(r, key, c, h)
					cfg.Prepared, cfg.Mode = true, server.ModePrepared
					if r.ActiveWSSURL != "" {
						cfg.KnownHosts, f.hosts = []string{r.ActiveWSSURL}, nil
					}
				case server.DedicatedTransitionPrepared:
					loadErr = server.ValidateDedicatedPrepared(r, key, c, h)
					cfg.Prepared, cfg.Mode = true, server.ModePrepared
					if r.ActiveWSSURL != "" {
						// A prepared destination may resume only its durable pinned
						// source, never a caller/config/learned-host alternative.
						cfg.KnownHosts = []string{r.ActiveWSSURL}
						f.hosts = nil
					}
				case server.DedicatedTransitionCommitted:
					loadErr = server.ValidateDedicatedCommitted(r, key, c, h)
				case server.DedicatedTransitionFenced:
					loadErr = server.ValidateDedicatedFenced(r, key, c, h)
					cfg.Prepared, cfg.Mode = false, server.ModeFenced
				}
				_ = h.Close()
			}
		}
		if loadErr != nil {
			guidance := server.DedicatedRecoveryRetryPreparation.Guidance()
			if r.Recovery.Guidance() != "" {
				guidance = r.Recovery.Guidance()
			}
			log.Printf("WARNING: dedicated transition is invalid or stale: %v; %s", loadErr, guidance)
		} else {
			log.Printf("dedicated %s transition recognized; %s", r.State, r.Recovery.Guidance())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("dedicated transition record: %v", err)
	}
	// Both the destination journal and the active source's readiness
	// attestation live beside the chain, regardless of -config.
	cfg.DedicatedTransitionPath = transitionPath
	cfg.HostKey, cfg.Chain, cfg.HistoryPath = key, c, f.history
	if !created && f.config != "" {
		if set, err := server.GroupFieldsSet(f.config); err != nil {
			log.Print(err)
		} else if len(set) > 0 {
			log.Printf("WARNING: chain exists; ignoring group keys in %s: %s (use -apply-config to append them)",
				f.config, strings.Join(set, ", "))
		}
	}

	if cfg.Password != "" && len(cfg.PasswordKey) == 0 {
		log.Print("NOTICE: server.toml stores the plain password; set password_key instead (the server derives it on every start)")
	}

	host := server.NewHost(buildHostConfig(f, cfg, dir, func(s string) { log.Print(s) }))
	if err := host.Start(); err != nil {
		log.Fatal(err)
	}
	srv := host.Server()
	scheme := "ws"
	if cfg.TLSCert != "" {
		scheme = "wss"
	}
	log.Printf("hermec-server %s", version.Version)
	log.Printf("server id %s", c.ID().Short())
	log.Printf("listening on %s (%s)", displayAddr(srv.Addr()), scheme)
	if cfg.TLSCert == "" && !isLoopback(srv.Addr()) {
		log.Print("WARNING: serving plain ws on a non-loopback address; traffic is unencrypted (set tls_cert/tls_key)")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Print("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := host.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

// openChain opens or creates the chain at path and warns about a torn tail.
func openChain(path string, key *identity.Identity, cfg server.Config) (*chain.Chain, bool, error) {
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

// applyConfigFile is -apply-config: it appends the group fields that the TOML
// at configPath sets (cfg is that file, loaded) to the chain at chainPath,
// signed with the key at keyPath, and prints one line per entry to out. It also
// serves an app host: pass the app's identity.key and chains/<id>.jsonl.
func applyConfigFile(configPath, keyPath, chainPath string, cfg server.Config, out io.Writer) error {
	// A running server holds the chain in memory; appending to the file
	// under it would fork the two.
	if err := checkNotRunning(cfg.Addr); err != nil {
		return err
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return err
	}
	c, created, err := openChain(chainPath, key, cfg)
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

// checkHostKey refuses to serve a chain with a key it does not list as a host:
// every client would reject the proof. keyPath only names the file in the error.
func checkHostKey(keyPath string, c *chain.Chain, key *identity.Identity) error {
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

// loadOrCreateKey loads the host key at path, generating and saving one when
// the file does not exist.
func loadOrCreateKey(path string) (*identity.Identity, error) {
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

// isLoopback reports whether addr is bound to a loopback host only.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// displayAddr renders a wildcard listen address as ":port".
func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return ":" + port
	}
	return addr
}
