package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
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
	result, err := initDedicated(dir)
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
	if cfg.Password != "" || len(cfg.PasswordKey) != 32 || result.Password == "" {
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
	if _, err := initDedicated(dir); err == nil {
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
	if _, err := initDedicated(dir); err == nil {
		t.Fatal("bootstrap unexpectedly succeeded below a file")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("partial dedicated state exists: %v", err)
	}
}

func TestCheckDedicatedStateRejectsInsecureState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dedicated")
	result, err := initDedicated(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.LoadConfig(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	cfg.TLSCert, cfg.TLSKey = testCertificate(t)
	var out bytes.Buffer
	if err := checkDedicatedState(cfg, filepath.Join(dir, "hermec-server.key"), filepath.Join(dir, "chain.jsonl"), filepath.Join(dir, "history.jsonl"), &out); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("server id: "+result.ServerID)) {
		t.Fatalf("state check output = %q", out.String())
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(dir, "history.jsonl"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkDedicatedState(cfg, filepath.Join(dir, "hermec-server.key"), filepath.Join(dir, "chain.jsonl"), filepath.Join(dir, "history.jsonl"), &out); err == nil {
			t.Fatal("state check accepted world-readable history")
		}
	}
}

func TestUpdateAndRollbackPreserveState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	result, err := initDedicated(state)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.LoadConfig(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	// A loopback config makes the real candidate's -check independent of a
	// system trust store while still checking the generated persistent state.
	cfg.Addr, cfg.PublicMode, cfg.TLSCert, cfg.TLSKey = "127.0.0.1:7697", false, "", ""
	raw, err := cfg.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	contents := make(map[string][]byte)
	for _, name := range []string{"hermec-server.key", "chain.jsonl", "history.jsonl"} {
		contents[name], err = os.ReadFile(filepath.Join(state, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	bin, previous := filepath.Join(dir, "bin", "hermec-server"), filepath.Join(dir, "lib", "previous")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(dir, "new")
	if output, err := exec.Command("go", "build", "-o", newer, ".").CombinedOutput(); err != nil {
		t.Fatalf("build real candidate: %v\n%s", err, output)
	}
	systemctl := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\n[ \"$1\" = is-active ] && exit 0\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin, "HERMEC_PREVIOUS="+previous, "HERMEC_STATE="+state, "HERMEC_SYSTEMCTL="+systemctl, "HERMEC_HEALTH_DELAY=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	run("update", newer)
	run("rollback")
	for name, want := range contents {
		got, err := os.ReadFile(filepath.Join(state, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("state %s changed: %q, %v", name, got, err)
		}
	}
	got, err := os.ReadFile(bin)
	if err != nil || !bytes.Contains(got, []byte("echo old")) {
		t.Fatalf("rollback binary = %q, %v", got, err)
	}
}

func TestUpdateRejectsCandidateBeforeStoppingService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	systemctl := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$HERMEC_TEST_LOG\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, candidateBody := range map[string]string{
		"wrong version output": "echo not-hermec; exit 0",
		"state check failure":  "[ \"$1\" = -version ] && { echo 'hermec-server test'; exit 0; }; exit 1",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "bad-server")
			if err := os.WriteFile(candidate, []byte("#!/bin/sh\n"+candidateBody+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", script, "update", candidate)
			cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_SYSTEMCTL="+systemctl, "HERMEC_TEST_LOG="+logPath)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("bad candidate accepted: %s", output)
			}
		})
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("systemctl called before candidate validation: %q", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestUpdateRollsBackAfterPostStartHealthFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	dir := t.TempDir()
	bin, previous := filepath.Join(dir, "bin", "hermec-server"), filepath.Join(dir, "lib", "previous")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(dir, "new")
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\n[ \"$1\" = -version ] && { echo 'hermec-server test'; exit 0; }\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(dir, "starts")
	systemctl := filepath.Join(dir, "systemctl")
	fakeSystemctl := "#!/bin/sh\ncase \"$1\" in\nstart) n=$(cat \"$HERMEC_TEST_STARTS\" 2>/dev/null || echo 0); echo $((n + 1)) > \"$HERMEC_TEST_STARTS\" ;;\nis-active) n=$(cat \"$HERMEC_TEST_STARTS\" 2>/dev/null || echo 0); [ \"$n\" -ne 1 ] ;;\nesac\n"
	if err := os.WriteFile(systemctl, []byte(fakeSystemctl), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "update", candidate)
	cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin, "HERMEC_PREVIOUS="+previous, "HERMEC_SYSTEMCTL="+systemctl, "HERMEC_TEST_STARTS="+starts, "HERMEC_HEALTH_ATTEMPTS=1", "HERMEC_HEALTH_DELAY=0")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("unhealthy candidate accepted: %s", output)
	}
	got, err := os.ReadFile(bin)
	if err != nil || !bytes.Contains(got, []byte("echo old")) {
		t.Fatalf("rollback binary = %q, %v", got, err)
	}
	if data, err := os.ReadFile(starts); err != nil || strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("starts = %q, %v; want new and rollback starts", data, err)
	}
}

func TestValidatePublicConfigFailsClosed(t *testing.T) {
	base := server.DefaultConfig()
	base.Discoverable = false
	base.PasswordKey = make([]byte, 32)
	if err := validatePublicConfig(base, false); err == nil {
		t.Fatal("wildcard listener accepted without TLS")
	}
	for _, addr := range []string{"0.0.0.0:7697", "[::]:7697"} {
		base.Addr = addr
		if err := validatePublicConfig(base, false); err == nil {
			t.Fatalf("explicit wildcard listener %q accepted without TLS", addr)
		}
	}
	base.Addr = "127.0.0.1:7697"
	if err := validatePublicConfig(base, false); err != nil {
		t.Fatalf("explicit loopback development listener rejected: %v", err)
	}
	base.Addr = "192.168.1.10:7697"
	if err := validatePublicConfig(base, false); err != nil {
		t.Fatalf("explicit LAN development listener rejected: %v", err)
	}
	base.Addr, base.PublicMode, base.PublicDNS = ":7697", true, "dedicated.example"
	cert, key := testCertificate(t)
	base.TLSCert, base.TLSKey = cert, key
	if err := os.WriteFile(cert, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePublicConfig(base, false); err == nil {
		t.Fatal("public mode accepted invalid TLS")
	}
	cert, key = testCertificate(t)
	base.TLSCert, base.TLSKey = cert, key
	if err := validatePublicConfig(base, false); err == nil {
		t.Fatal("public mode accepted untrusted TLS")
	}
	base.PasswordKey = nil
	if err := validatePublicConfig(base, false); err == nil {
		t.Fatal("public mode accepted without admission")
	}
	base.TLSCert, base.TLSKey, base.PasswordKey = "", "", nil
	if err := validatePublicConfig(base, true); err != nil {
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
	if err := validatePublicConfig(cfg, false); err == nil || !strings.Contains(err.Error(), "fixed, non-zero") {
		t.Fatalf("explicit media port allowed ephemeral signaling: %v", err)
	}
	cfg.MediaPort = 0
	if err := validatePublicConfig(cfg, false); err == nil || !strings.Contains(err.Error(), "fixed, non-zero") {
		t.Fatalf("derived media port allowed ephemeral signaling: %v", err)
	}
}

func TestServerVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in -short mode")
	}
	name := "hermec-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	if output, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, exe, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("hermec-server -version: %v\n%s", err, output)
	}
	if got, want := string(output), "hermec-server dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
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
