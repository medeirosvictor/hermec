// Command hermec-server runs a Hermec server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"github.com/medeirosvictor/hermec/server/dedicated"
	"github.com/medeirosvictor/hermec/server/history"
	"github.com/medeirosvictor/hermec/server/host"
	"github.com/medeirosvictor/hermec/server/serverctl"
	"github.com/medeirosvictor/hermec/version"
)

func main() {
	f, err := parseFlags(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	} else if err != nil {
		os.Exit(2)
	}
	if err := run(f); err != nil {
		log.Fatal(err)
	}
}

// run executes the parsed command line: a one-shot command, or the server
// until it is signalled.
func run(f flags) error {
	if f.showVersion {
		fmt.Printf("hermec-server %s\n", version.Version)
		return nil
	}
	if f.initDedicated != "" {
		return runInitDedicated(f)
	}
	if f.applyConfig && f.config == "" {
		return errors.New("-apply-config requires -config")
	}

	cfg := server.DefaultConfig()
	if f.config != "" {
		var err error
		if cfg, err = server.LoadConfig(f.config); err != nil {
			return err
		}
	}
	dir := defaultStatePaths(&f)
	if done, err := runOneShot(f, cfg); done || err != nil {
		return err
	}

	key, c, cfg, created, err := loadState(f, cfg)
	if err != nil {
		return err
	}
	if !created && f.config != "" {
		if err := warnIgnoredGroupFields(f.config, cfg); err != nil {
			return err
		}
	}
	if done, err := checkStartup(f, cfg); done || err != nil {
		return err
	}
	transitionPath, err := restoreTransition(&f, &cfg, key, c)
	if err != nil {
		return err
	}
	// Both the destination journal and the active source's readiness
	// attestation live beside the chain, regardless of -config.
	cfg.DedicatedTransitionPath = transitionPath
	cfg.HostKey, cfg.Chain, cfg.HistoryPath = key, c, f.history
	return serve(f, cfg, dir, c)
}

// runInitDedicated is -init-dedicated.
func runInitDedicated(f flags) error {
	if f.config != "" || f.applyConfig || f.printConfig || f.check || f.key != "" || f.chain != "" || f.history != "" {
		return errors.New("-init-dedicated cannot be combined with config/state operations")
	}
	result, err := serverctl.InitDedicated(f.initDedicated)
	if err != nil {
		return err
	}
	fmt.Printf("dedicated server initialized in %s\n", result.Dir)
	fmt.Printf("config: %s\n", result.Config)
	fmt.Printf("server id: %s\n", result.ServerID)
	fmt.Printf("generated join password (shown once): %s\n", result.Password)
	fmt.Println("install a trusted TLS certificate at the configured paths before starting")
	return nil
}

// defaultStatePaths fills the key, chain and history paths that were not
// given with files next to -config (or the working directory) and returns
// that directory.
func defaultStatePaths(f *flags) string {
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
	return dir
}

// runOneShot runs the command selected by the flags, if any, and reports
// whether it handled the invocation.
func runOneShot(f flags, cfg server.Config) (bool, error) {
	if f.printConfig {
		return true, printConfig(f.chain, cfg)
	}
	if done, err := runDedicatedCommand(f, cfg); done || err != nil {
		return done, err
	}
	if f.applyConfig {
		return true, serverctl.ApplyConfigFile(f.config, f.key, f.chain, cfg, os.Stdout)
	}
	return false, nil
}

// printConfig is -print-config: the effective config, password redacted.
func printConfig(chainPath string, cfg server.Config) error {
	if _, err := os.Stat(chainPath); err == nil {
		c, err := chain.Load(chainPath)
		if err != nil {
			return fmt.Errorf("signed chain: %w", err)
		}
		if cfg, err = server.EffectiveConfig(cfg, c.State()); err != nil {
			return fmt.Errorf("signed chain: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("signed chain: %w", err)
	}
	shown := cfg
	shown.PasswordKey = nil
	out, err := shown.MarshalTOML()
	if err != nil {
		return err
	}
	fmt.Print(string(out))
	return nil
}

// runDedicatedCommand runs the -dedicated-* workflow selected by the flags and
// reports whether there was one.
func runDedicatedCommand(f flags, cfg server.Config) (bool, error) {
	if f.dedicatedHostKey {
		if f.dedicatedStatus || f.dedicatedPrepare != "" || f.dedicatedRetry || f.dedicatedAttestationStatus || f.dedicatedAttestationReset {
			return true, errors.New("-dedicated-host-key cannot be combined with another dedicated workflow flag")
		}
		return true, serverctl.DedicatedHostPublic(f.key, os.Stdout)
	}
	if f.dedicatedPrepare != "" && f.dedicatedRetry {
		return true, errors.New("-dedicated-prepare and -dedicated-retry cannot be combined")
	}
	if f.dedicatedAttestationStatus || f.dedicatedAttestationReset {
		if f.dedicatedStatus || f.dedicatedPrepare != "" || f.dedicatedRetry {
			return true, errors.New("-dedicated-attestation-status/-dedicated-attestation-reset cannot be combined with another dedicated workflow flag")
		}
		return true, serverctl.DedicatedAttestationStatus(f.key, f.chain, f.dedicatedAttestationReset, os.Stdout)
	}
	if f.dedicatedStatus {
		if f.dedicatedPrepare != "" || f.dedicatedRetry {
			return true, errors.New("-dedicated-status cannot be combined with prepare or retry")
		}
		return true, serverctl.DedicatedStatus(f.key, f.chain, f.history, os.Stdout)
	}
	if f.dedicatedPrepare != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return true, serverctl.RunDedicatedPreparation(ctx, cfg, f.key, f.chain, f.history, f.dedicatedPrepare, f.dedicatedServerID, false, os.Stdout)
	}
	if f.dedicatedRetry {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return true, serverctl.RunDedicatedPreparation(ctx, cfg, f.key, f.chain, f.history, "", "", true, os.Stdout)
	}
	return false, nil
}

// loadState loads the signed chain and host key, founding a new chain from cfg
// when there is none, and returns cfg with the chain's signed config applied.
// created reports whether a new chain was founded.
func loadState(f flags, cfg server.Config) (*identity.Identity, *chain.Chain, server.Config, bool, error) {
	var (
		c       *chain.Chain
		created bool
		err     error
	)
	// Load an existing chain before touching the identity. Its signed config is
	// authoritative; a config-less chain must be repaired with
	// -apply-config rather than falling back to local TOML group fields.
	if _, statErr := os.Stat(f.chain); statErr == nil {
		if c, err = chain.Load(f.chain); err != nil {
			return nil, nil, cfg, false, fmt.Errorf("signed chain: %w", err)
		}
		if cfg, err = server.EffectiveConfig(cfg, c.State()); err != nil {
			return nil, nil, cfg, false, fmt.Errorf("signed chain: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, nil, cfg, false, fmt.Errorf("signed chain: %w", statErr)
	} else {
		// Do every local preflight before generating or persisting a new
		// identity/chain. A failed public TLS or admission check therefore leaves
		// no durable state behind.
		if err := server.ValidateInitialConfig(cfg); err != nil {
			return nil, nil, cfg, false, err
		}
		if err := serverctl.ValidatePublicConfig(cfg, f.unsafePublicWS); err != nil {
			return nil, nil, cfg, false, err
		}
		created = true
	}
	key, err := serverctl.LoadOrCreateKey(f.key)
	if err != nil {
		return nil, nil, cfg, false, err
	}
	if created {
		c, created, err = serverctl.OpenChain(f.chain, key, cfg)
		if err != nil {
			return nil, nil, cfg, false, err
		}
		if cfg, err = server.EffectiveConfig(cfg, c.State()); err != nil {
			return nil, nil, cfg, false, fmt.Errorf("signed chain: %w", err)
		}
	}
	if err := serverctl.CheckHostKey(f.key, c, key); err != nil {
		return nil, nil, cfg, false, err
	}
	return key, c, cfg, created, nil
}

// warnIgnoredGroupFields tells the operator which group keys of the TOML at
// path an existing chain ignores.
func warnIgnoredGroupFields(path string, cfg server.Config) error {
	set, err := server.GroupFieldsSet(path)
	if err != nil {
		log.Print(err)
	} else if len(set) > 0 {
		if len(cfg.AllowedKeys) == 0 && strings.Contains(strings.Join(set, ","), "allowed_keys") && len(cfg.PasswordKey) == 0 {
			return errors.New("chain has no admission allowlist; TOML allowed_keys is ignored and cannot secure this server (use -apply-config)")
		}
		log.Printf("WARNING: chain exists; ignoring group keys in %s: %s (use -apply-config to append them)", path, strings.Join(set, ", "))
	}
	return nil
}

// checkStartup validates a public listener and, for -check or a public
// server, the persistent dedicated state. done is true after -check.
func checkStartup(f flags, cfg server.Config) (done bool, err error) {
	if err := serverctl.ValidatePublicConfig(cfg, f.unsafePublicWS); err != nil {
		return false, err
	}
	public := cfg.PublicMode || serverctl.PublicListener(cfg.Addr)
	if public && f.unsafePublicWS {
		log.Print("WARNING: unsafe public mode enabled; TLS/admission checks are bypassed")
	}
	if f.check || (public && !f.unsafePublicWS) {
		if err := serverctl.CheckDedicatedState(cfg, f.key, f.chain, f.history, os.Stdout); err != nil {
			return false, err
		}
		if f.check {
			return true, nil
		}
	}
	return false, nil
}

// restoreTransition returns the transition journal path. A surviving
// preparation record is never permission to activate. Load it before
// constructing Host, validate its local boundary, and keep even a malformed or
// stale record passive with fixed recovery guidance.
func restoreTransition(f *flags, cfg *server.Config, key *identity.Identity, c *chain.Chain) (string, error) {
	transitionPath := dedicated.TransitionPath(f.chain)
	if _, err := os.Lstat(transitionPath); err == nil {
		// Any unreadable or stale journal fails closed as a passive host.
		cfg.Prepared, cfg.Mode = true, server.ModePrepared
		r, loadErr := dedicated.Load(transitionPath)
		if loadErr == nil {
			var h *history.Store
			h, loadErr = history.Open(f.history)
			if loadErr == nil {
				switch r.State {
				case dedicated.Preparing:
					loadErr = dedicated.ValidatePreparing(r, key, c, h)
					cfg.Prepared, cfg.Mode = true, server.ModePrepared
					if r.ActiveWSSURL != "" {
						cfg.KnownHosts, f.hosts = []string{r.ActiveWSSURL}, nil
					}
				case dedicated.Prepared:
					loadErr = dedicated.ValidatePrepared(r, key, c, h)
					cfg.Prepared, cfg.Mode = true, server.ModePrepared
					if r.ActiveWSSURL != "" {
						// A prepared destination may resume only its durable pinned
						// source, never a caller/config/learned-host alternative.
						cfg.KnownHosts = []string{r.ActiveWSSURL}
						f.hosts = nil
					}
				case dedicated.Committed:
					loadErr = dedicated.ValidateCommitted(r, key, c, h)
				case dedicated.Fenced:
					loadErr = dedicated.ValidateFenced(r, key, c, h)
					cfg.Prepared, cfg.Mode = false, server.ModeFenced
				}
				_ = h.Close()
			}
		}
		if loadErr != nil {
			guidance := dedicated.RecoveryRetryPreparation.Guidance()
			if r.Recovery.Guidance() != "" {
				guidance = r.Recovery.Guidance()
			}
			log.Printf("WARNING: dedicated transition is invalid or stale: %v; %s", loadErr, guidance)
		} else {
			log.Printf("dedicated %s transition recognized; %s", r.State, r.Recovery.Guidance())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("dedicated transition record: %w", err)
	}
	return transitionPath, nil
}

// serve starts the host and runs it until SIGINT or SIGTERM.
func serve(f flags, cfg server.Config, dir string, c *chain.Chain) error {
	h := host.New(buildHostConfig(f, cfg, dir, func(s string) { log.Print(s) }))
	if err := h.Start(); err != nil {
		return err
	}
	srv := h.Server()
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
	if err := h.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
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
