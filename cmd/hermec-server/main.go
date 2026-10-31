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
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/server/dedicated"
	"github.com/medeirosvictor/hermec/server/history"
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
	if f.showVersion {
		fmt.Printf("hermec-server %s\n", version.Version)
		return
	}
	if f.initDedicated != "" {
		if f.config != "" || f.applyConfig || f.printConfig || f.check || f.key != "" || f.chain != "" || f.history != "" {
			log.Fatal("-init-dedicated cannot be combined with config/state operations")
		}
		result, err := serverctl.InitDedicated(f.initDedicated)
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
	if f.printConfig {
		if _, err := os.Stat(f.chain); err == nil {
			c, err := chain.Load(f.chain)
			if err != nil {
				log.Fatalf("signed chain: %v", err)
			}
			if cfg, err = server.EffectiveConfig(cfg, c.State()); err != nil {
				log.Fatalf("signed chain: %v", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Fatalf("signed chain: %v", err)
		}
		shown := cfg
		shown.PasswordKey = nil
		out, err := shown.MarshalTOML()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(out))
		return
	}
	if f.dedicatedHostKey {
		if f.dedicatedStatus || f.dedicatedPrepare != "" || f.dedicatedRetry || f.dedicatedAttestationStatus || f.dedicatedAttestationReset {
			log.Fatal("-dedicated-host-key cannot be combined with another dedicated workflow flag")
		}
		if err := serverctl.DedicatedHostPublic(f.key, os.Stdout); err != nil {
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
		if err := serverctl.DedicatedAttestationStatus(f.key, f.chain, f.dedicatedAttestationReset, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedStatus {
		if f.dedicatedPrepare != "" || f.dedicatedRetry {
			log.Fatal("-dedicated-status cannot be combined with prepare or retry")
		}
		if err := serverctl.DedicatedStatus(f.key, f.chain, f.history, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedPrepare != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := serverctl.RunDedicatedPreparation(ctx, cfg, f.key, f.chain, f.history, f.dedicatedPrepare, f.dedicatedServerID, false, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.dedicatedRetry {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := serverctl.RunDedicatedPreparation(ctx, cfg, f.key, f.chain, f.history, "", "", true, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if f.applyConfig {
		if err := serverctl.ApplyConfigFile(f.config, f.key, f.chain, cfg, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	// Load an existing chain before touching the identity. Its signed config is
	// authoritative; a config-less chain must be repaired with
	// -apply-config rather than falling back to local TOML group fields.
	var c *chain.Chain
	var created bool
	if _, statErr := os.Stat(f.chain); statErr == nil {
		if c, err = chain.Load(f.chain); err != nil {
			log.Fatalf("signed chain: %v", err)
		}
		if cfg, err = server.EffectiveConfig(cfg, c.State()); err != nil {
			log.Fatalf("signed chain: %v", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		log.Fatalf("signed chain: %v", statErr)
	} else {
		// Do every local preflight before generating or persisting a new
		// identity/chain. A failed public TLS or admission check therefore leaves
		// no durable state behind.
		if err := server.ValidateInitialConfig(cfg); err != nil {
			log.Fatal(err)
		}
		if err := serverctl.ValidatePublicConfig(cfg, f.unsafePublicWS); err != nil {
			log.Fatal(err)
		}
		created = true
	}
	key, err := serverctl.LoadOrCreateKey(f.key)
	if err != nil {
		log.Fatal(err)
	}
	if created {
		c, created, err = serverctl.OpenChain(f.chain, key, cfg)
		if err != nil {
			log.Fatal(err)
		}
		if cfg, err = server.EffectiveConfig(cfg, c.State()); err != nil {
			log.Fatalf("signed chain: %v", err)
		}
	}
	if err := serverctl.CheckHostKey(f.key, c, key); err != nil {
		log.Fatal(err)
	}
	if !created && f.config != "" {
		if set, err := server.GroupFieldsSet(f.config); err != nil {
			log.Print(err)
		} else if len(set) > 0 {
			if len(cfg.AllowedKeys) == 0 && strings.Contains(strings.Join(set, ","), "allowed_keys") && len(cfg.PasswordKey) == 0 {
				log.Fatal("chain has no admission allowlist; TOML allowed_keys is ignored and cannot secure this server (use -apply-config)")
			}
			log.Printf("WARNING: chain exists; ignoring group keys in %s: %s (use -apply-config to append them)", f.config, strings.Join(set, ", "))
		}
	}
	if err := serverctl.ValidatePublicConfig(cfg, f.unsafePublicWS); err != nil {
		log.Fatal(err)
	}
	public := cfg.PublicMode || serverctl.PublicListener(cfg.Addr)
	if public && f.unsafePublicWS {
		log.Print("WARNING: unsafe public mode enabled; TLS/admission checks are bypassed")
	}
	if f.check || (public && !f.unsafePublicWS) {
		if err := serverctl.CheckDedicatedState(cfg, f.key, f.chain, f.history, os.Stdout); err != nil {
			log.Fatal(err)
		}
		if f.check {
			return
		}
	}
	// A surviving preparation record is never permission to activate. Load it
	// before constructing Host, validate its local boundary, and keep even a
	// malformed or stale record passive with fixed recovery guidance.
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
		log.Fatalf("dedicated transition record: %v", err)
	}
	// Both the destination journal and the active source's readiness
	// attestation live beside the chain, regardless of -config.
	cfg.DedicatedTransitionPath = transitionPath
	cfg.HostKey, cfg.Chain, cfg.HistoryPath = key, c, f.history

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
