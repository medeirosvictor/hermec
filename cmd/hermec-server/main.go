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
	configPath := flag.String("config", "", "path to a TOML config file (defaults apply without it)")
	printConfig := flag.Bool("print-config", false, "print the effective config (password redacted) and exit")
	keyPath := flag.String("key", "", "host key file (default hermec-server.key next to -config; generated when missing)")
	chainPath := flag.String("chain", "", "server chain file (default chain.jsonl next to -config)")
	historyPath := flag.String("history", "", "chat history file (default history.jsonl next to -config)")
	applyConfig := flag.Bool("apply-config", false, "append the config file's group fields to the chain, print the count, and exit; stop the server first, the changes apply when it starts")
	flag.Parse()
	if *applyConfig && *configPath == "" {
		log.Fatal("-apply-config requires -config")
	}

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
	if *historyPath == "" {
		*historyPath = filepath.Join(dir, "history.jsonl")
	}
	if *applyConfig {
		if err := applyConfigFile(*configPath, *keyPath, *chainPath, cfg, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	key, err := loadOrCreateKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	c, created, err := openChain(*chainPath, key, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if err := checkHostKey(*keyPath, c, key); err != nil {
		log.Fatal(err)
	}
	cfg.HostKey, cfg.Chain, cfg.HistoryPath = key, c, *historyPath
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
