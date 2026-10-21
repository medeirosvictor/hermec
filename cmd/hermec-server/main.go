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

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/version"
)

func main() {
	configPath := flag.String("config", "", "path to a TOML config file (defaults apply without it)")
	printConfig := flag.Bool("print-config", false, "print the effective config (password redacted) and exit")
	keyPath := flag.String("key", "", "host key file (default hermec-server.key next to -config; generated when missing)")
	chainPath := flag.String("chain", "", "server chain file (default chain.jsonl next to -config)")
	applyConfig := flag.Bool("apply-config", false, "append the config file's group fields to the chain, print the count, and exit")
	flag.Parse()

	cfg := server.DefaultConfig()
	if *configPath != "" {
		var err error
		if cfg, err = server.LoadConfig(*configPath); err != nil {
			log.Fatal(err)
		}
	}

	if *printConfig {
		shown := cfg
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
	if *configPath != "" {
		dir = filepath.Dir(*configPath)
	}
	if *keyPath == "" {
		*keyPath = filepath.Join(dir, "hermec-server.key")
	}
	if *chainPath == "" {
		*chainPath = filepath.Join(dir, "chain.jsonl")
	}
	key, err := loadOrCreateKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	c, created, err := server.OpenChain(*chainPath, key, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if *applyConfig {
		n := len(c.Entries()) // a fresh chain was just built from cfg
		if !created {
			if n, err = server.ApplyTOML(c, key, cfg); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Printf("appended %d entries to %s\n", n, *chainPath)
		return
	}
	cfg.HostKey, cfg.Chain = key, c
	if !created && *configPath != "" {
		if set, err := server.GroupFieldsSet(*configPath); err != nil {
			log.Print(err)
		} else if len(set) > 0 {
			log.Printf("WARNING: chain exists; ignoring group keys in %s: %s (use -apply-config to append them)",
				*configPath, strings.Join(set, ", "))
		}
	}

	srv := server.New(cfg)
	if err := srv.Start(); err != nil {
		log.Fatal(err)
	}
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
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
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
