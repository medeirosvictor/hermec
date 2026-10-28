package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server"
)

const dedicatedConfigName = "server.toml"

type dedicatedInitResult struct {
	Dir      string
	Config   string
	ServerID string
	Password string
}

// initDedicated creates a complete persistent state layout in a sibling temp
// directory and renames it into place, so partial initialization is never
// mistaken for a usable server.
func initDedicated(path string) (dedicatedInitResult, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return dedicatedInitResult{}, err
	}
	if _, err := os.Stat(abs); err == nil {
		return dedicatedInitResult{}, fmt.Errorf("dedicated server directory already exists: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return dedicatedInitResult{}, err
	}
	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return dedicatedInitResult{}, err
	}
	tmp, err := os.MkdirTemp(parent, ".hermec-dedicated-")
	if err != nil {
		return dedicatedInitResult{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()
	for _, dir := range []string{"tls", "logs", "backups"} {
		if err := os.Mkdir(filepath.Join(tmp, dir), 0o700); err != nil {
			return dedicatedInitResult{}, err
		}
	}
	keyPath := filepath.Join(tmp, "hermec-server.key")
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return dedicatedInitResult{}, err
	}
	cfg := server.DefaultConfig()
	cfg.Addr = ":7697"
	cfg.MediaPort = 7697
	cfg.Discoverable = false
	cfg.PublicMode = true
	// The operator must set the DNS name before the public listener can start.
	cfg.PublicDNS = ""
	cfg.ServerName = "Hermec Dedicated Server"
	cfg.VoiceChannels = []string{"voice"}
	cfg.TLSCert = filepath.Join(abs, "tls", "fullchain.pem")
	cfg.TLSKey = filepath.Join(abs, "tls", "privkey.pem")
	chainPath := filepath.Join(tmp, "chain.jsonl")
	c, created, err := server.OpenChain(chainPath, key, cfg)
	if err != nil {
		return dedicatedInitResult{}, err
	}
	if !created {
		return dedicatedInitResult{}, errors.New("dedicated bootstrap unexpectedly found a chain")
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return dedicatedInitResult{}, err
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	serverID := c.ID()
	cfg.PasswordKey, err = proto.PasswordKey(password, serverID[:])
	if err != nil {
		return dedicatedInitResult{}, err
	}
	raw, err := cfg.MarshalTOML()
	if err != nil {
		return dedicatedInitResult{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, dedicatedConfigName), raw, 0o600); err != nil {
		return dedicatedInitResult{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "history.jsonl"), nil, 0o600); err != nil {
		return dedicatedInitResult{}, err
	}
	for _, name := range []string{"hermec-server.key", "chain.jsonl", "history.jsonl", dedicatedConfigName} {
		if err := os.Chmod(filepath.Join(tmp, name), 0o600); err != nil {
			return dedicatedInitResult{}, err
		}
	}
	if err := os.Rename(tmp, abs); err != nil {
		return dedicatedInitResult{}, err
	}
	keep = true
	return dedicatedInitResult{Dir: abs, Config: filepath.Join(abs, dedicatedConfigName), ServerID: c.ID().String(), Password: password}, nil
}

func checkDedicatedState(cfg server.Config, keyPath, chainPath, historyPath string, out io.Writer) error {
	if err := validateListenAddress(cfg); err != nil {
		return err
	}
	key, err := identity.Load(keyPath)
	if err != nil {
		return fmt.Errorf("host identity: %w", err)
	}
	c, err := chain.Load(chainPath)
	if err != nil {
		return fmt.Errorf("signed chain: %w", err)
	}
	if err := checkHostKey(keyPath, c, key); err != nil {
		return err
	}
	f, err := os.OpenFile(historyPath, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	_ = f.Close()
	for _, path := range []string{keyPath, chainPath, historyPath} {
		if err := requirePrivateFile(path); err != nil {
			return err
		}
	}
	transitionPath := filepath.Join(filepath.Dir(chainPath), server.DedicatedTransitionFile)
	if _, err := os.Lstat(transitionPath); err == nil {
		r, err := server.LoadDedicatedTransition(transitionPath)
		if err == nil {
			var h *server.History
			h, err = server.OpenHistory(historyPath)
			if err == nil {
				switch r.State {
				case server.DedicatedTransitionPrepared:
					err = server.ValidateDedicatedPrepared(r, key, c, h)
				case server.DedicatedTransitionCommitted:
					err = server.ValidateDedicatedCommitted(r, key, c, h)
				case server.DedicatedTransitionFenced:
					err = server.ValidateDedicatedFenced(r, key, c, h)
				}
				_ = h.Close()
			}
		}
		if err != nil {
			guidance := server.DedicatedRecoveryRetryPreparation.Guidance()
			if r.Recovery.Guidance() != "" {
				guidance = r.Recovery.Guidance()
			}
			return fmt.Errorf("dedicated transition is invalid or stale: %w; %s", err, guidance)
		}
		fmt.Fprintf(out, "dedicated prepared transition recognized; %s\n", r.Recovery.Guidance())
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dedicated transition record: %w", err)
	}
	if cfg.TLSKey != "" {
		if err := requirePrivateFile(cfg.TLSKey); err != nil {
			return fmt.Errorf("TLS private key: %w", err)
		}
	}
	mediaPort := cfg.MediaPort
	if mediaPort == 0 {
		_, port, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			return fmt.Errorf("signaling address: %w", err)
		}
		if _, err := fmt.Sscanf(port, "%d", &mediaPort); err != nil || mediaPort == 0 {
			return fmt.Errorf("cannot derive media UDP port from %q", cfg.Addr)
		}
	}
	scheme := "ws"
	if cfg.TLSCert != "" {
		scheme = "wss"
	}
	fmt.Fprintf(out, "configuration valid\nserver id: %s\nsignaling: %s on %s\nmedia: UDP %d\n", c.ID(), scheme, cfg.Addr, mediaPort)
	return nil
}

func requirePrivateFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s permissions are %04o; want no group/other access", path, info.Mode().Perm())
	}
	return nil
}

// validateListenAddress catches malformed and unusable ports before Start
// attempts to bind. It deliberately accepts wildcard and private addresses:
// those remain supported for existing LAN/friend servers.
func validateListenAddress(cfg server.Config) error {
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("signaling address %q: %w", cfg.Addr, err)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return fmt.Errorf("signaling address %q has invalid TCP port", cfg.Addr)
	}
	if cfg.MediaPort != 0 && (cfg.UDPPortMin != 0 || cfg.UDPPortMax != 0) {
		return errors.New("media_port conflicts with udp_port_min/udp_port_max")
	}
	return nil
}

// publicListener reports whether addr can accept internet traffic. Explicit
// loopback, RFC1918/ULA, and link-local bindings retain the LAN development
// behavior; wildcard, named, and globally-routable bindings are public.
func publicListener(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func validatePublicConfig(cfg server.Config, unsafe bool) error {
	if err := validateListenAddress(cfg); err != nil {
		return err
	}
	if !cfg.PublicMode && !publicListener(cfg.Addr) {
		return nil
	}
	if unsafe {
		return nil
	}
	_, port, _ := net.SplitHostPort(cfg.Addr)
	if port == "0" {
		return errors.New("public server requires a fixed, non-zero signaling TCP port")
	}
	if cfg.Password == "" && len(cfg.PasswordKey) == 0 && len(cfg.AllowedKeys) == 0 {
		return errors.New("public server requires password/password_key or allowed_keys")
	}
	if cfg.Discoverable {
		return errors.New("public server must set discoverable = false")
	}
	if cfg.TLSCert == "" || cfg.TLSKey == "" {
		return errors.New("public server requires tls_cert and tls_key (or -unsafe-public-ws for disposable development only)")
	}
	if err := requirePrivateFile(cfg.TLSKey); err != nil {
		return fmt.Errorf("TLS private key: %w", err)
	}
	if err := validatePublicTLS(cfg.TLSCert, cfg.TLSKey, cfg.PublicDNS, nil); err != nil {
		return fmt.Errorf("public server TLS: %w", err)
	}
	return nil
}

// validatePublicTLS verifies the configured certificate as a client would:
// it must be a currently-valid server certificate chained to a system trust
// root and cover the configured public DNS name. roots is test-only; nil uses
// the platform trust store.
func validatePublicTLS(certPath, keyPath, dnsName string, roots *x509.CertPool) error {
	if !validDNSName(dnsName) {
		return errors.New("public_dns must be a DNS hostname, not an IP address")
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		return err
	}
	pemData, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return errors.New("certificate file contains no certificates")
	}
	if roots == nil {
		roots, err = x509.SystemCertPool()
		if err != nil {
			return fmt.Errorf("system trust store: %w", err)
		}
		if roots == nil {
			return errors.New("system trust store is unavailable")
		}
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	_, err = certs[0].Verify(x509.VerifyOptions{DNSName: dnsName, Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

func validDNSName(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || len(name) > 253 || net.ParseIP(name) != nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r == '-' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}
