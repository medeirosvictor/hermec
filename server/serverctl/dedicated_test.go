package serverctl

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server"
)

func TestInitDedicatedCreatesSecurePersistentLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dedicated")
	result, err := InitDedicated(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.LoadConfig(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PublicMode || cfg.Discoverable || cfg.TLSCert == "" || cfg.TLSKey == "" {
		t.Fatalf("config is not strict public mode: %+v", cfg)
	}
	if len(cfg.PasswordKey) != 32 || result.Password == "" {
		t.Fatal("bootstrap did not create password-key admission")
	}
	c, err := chain.Load(filepath.Join(dir, "chain.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerID != c.ID().String() {
		t.Fatalf("server id = %q, chain = %q", result.ServerID, c.ID())
	}
	id := c.ID()
	want, err := proto.PasswordKey(result.Password, id[:])
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(cfg.PasswordKey) {
		t.Fatal("shown password does not match stored password key")
	}
	for _, path := range []string{"hermec-server.key", "chain.jsonl", "history.jsonl", dedicatedConfigName} {
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %v", path, info.Mode().Perm())
		}
	}
	before, err := os.ReadFile(filepath.Join(dir, "hermec-server.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InitDedicated(dir); err == nil {
		t.Fatal("repeat bootstrap replaced existing state")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "hermec-server.key"))
	if string(before) != string(after) {
		t.Fatal("repeat bootstrap changed identity")
	}
}

func TestInitDedicatedFailureLeavesNoState(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("block bootstrap"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "dedicated")
	if _, err := InitDedicated(dir); err == nil {
		t.Fatal("bootstrap unexpectedly succeeded below a file")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("partial dedicated state exists: %v", err)
	}
}

func TestCheckDedicatedStateRejectsInsecureState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dedicated")
	result, err := InitDedicated(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.LoadConfig(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TLSCert, cfg.TLSKey = testCertificate(t)
	var out bytes.Buffer
	if err := CheckDedicatedState(cfg, filepath.Join(dir, "hermec-server.key"), filepath.Join(dir, "chain.jsonl"), filepath.Join(dir, "history.jsonl"), &out); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("server id: "+result.ServerID)) {
		t.Fatalf("state check output = %q", out.String())
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(dir, "history.jsonl"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := CheckDedicatedState(cfg, filepath.Join(dir, "hermec-server.key"), filepath.Join(dir, "chain.jsonl"), filepath.Join(dir, "history.jsonl"), &out); err == nil {
			t.Fatal("state check accepted world-readable history")
		}
	}
}

func TestValidatePublicConfigFailsClosed(t *testing.T) {
	base := server.DefaultConfig()
	base.Discoverable = false
	base.PasswordKey = make([]byte, 32)
	if err := ValidatePublicConfig(base, false); err == nil {
		t.Fatal("wildcard listener accepted without TLS")
	}
	for _, addr := range []string{"0.0.0.0:7697", "[::]:7697"} {
		base.Addr = addr
		if err := ValidatePublicConfig(base, false); err == nil {
			t.Fatalf("explicit wildcard listener %q accepted without TLS", addr)
		}
	}
	base.Addr = "127.0.0.1:7697"
	if err := ValidatePublicConfig(base, false); err != nil {
		t.Fatalf("explicit loopback development listener rejected: %v", err)
	}
	base.Addr = "192.168.1.10:7697"
	if err := ValidatePublicConfig(base, false); err != nil {
		t.Fatalf("explicit LAN development listener rejected: %v", err)
	}
	base.Addr, base.PublicMode, base.PublicDNS = ":7697", true, "dedicated.example"
	cert, key := testCertificate(t)
	base.TLSCert, base.TLSKey = cert, key
	if err := os.WriteFile(cert, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePublicConfig(base, false); err == nil {
		t.Fatal("public mode accepted invalid TLS")
	}
	cert, key = testCertificate(t)
	base.TLSCert, base.TLSKey = cert, key
	if err := ValidatePublicConfig(base, false); err == nil {
		t.Fatal("public mode accepted untrusted TLS")
	}
	base.PasswordKey = nil
	if err := ValidatePublicConfig(base, false); err == nil {
		t.Fatal("public mode accepted without admission")
	}
	base.TLSCert, base.TLSKey, base.PasswordKey = "", "", nil
	if err := ValidatePublicConfig(base, true); err != nil {
		t.Fatalf("unsafe override: %v", err)
	}
}

func TestPublicTLSValidatesTrustNameAndExpiry(t *testing.T) {
	cert, key, roots := testTrustedCertificate(t, "dedicated.example", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err := validatePublicTLS(cert, key, "dedicated.example", roots); err != nil {
		t.Fatalf("trusted certificate rejected: %v", err)
	}
	if err := validatePublicTLS(cert, key, "other.example", roots); err == nil {
		t.Fatal("hostname mismatch accepted")
	}
	expiredCert, expiredKey, expiredRoots := testTrustedCertificate(t, "dedicated.example", time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if err := validatePublicTLS(expiredCert, expiredKey, "dedicated.example", expiredRoots); err == nil {
		t.Fatal("expired certificate accepted")
	}
}

func TestPublicConfigRejectsEphemeralSignalingPort(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.Addr, cfg.MediaPort, cfg.PublicMode = ":0", 7697, true
	if err := ValidatePublicConfig(cfg, false); err == nil || !strings.Contains(err.Error(), "fixed, non-zero") {
		t.Fatalf("explicit media port allowed ephemeral signaling: %v", err)
	}
	cfg.MediaPort = 0
	if err := ValidatePublicConfig(cfg, false); err == nil || !strings.Contains(err.Error(), "fixed, non-zero") {
		t.Fatalf("derived media port allowed ephemeral signaling: %v", err)
	}
}

func testTrustedCertificate(t *testing.T, dnsName string, notBefore, notAfter time.Time) (string, string, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := x509.Certificate{SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, &ca, &ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := x509.Certificate{SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: dnsName}, DNSNames: []string{dnsName}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, &leaf, &ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	pemData := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	if err := os.WriteFile(certPath, pemData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	return certPath, keyPath, roots
}

func testCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestInitDedicatedAcceptsEmptyPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := InitDedicated(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hermec-server.key", "chain.jsonl", "history.jsonl", dedicatedConfigName, "tls", "logs", "backups"} {
		if _, err := os.Stat(filepath.Join(result.Dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hermec-dedicated-") {
			t.Errorf("staging directory left behind: %s", e.Name())
		}
	}
}

func TestInitDedicatedRejectsUnsafeExistingDir(t *testing.T) {
	full := filepath.Join(t.TempDir(), "full")
	if err := os.Mkdir(full, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitDedicated(full); err == nil {
		t.Fatal("bootstrap accepted a non-empty directory")
	}
	if runtime.GOOS == "windows" {
		return
	}
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitDedicated(open); err == nil {
		t.Fatal("bootstrap accepted a group/other-accessible directory")
	}
}
