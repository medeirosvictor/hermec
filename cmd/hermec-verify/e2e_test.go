package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

// TestVerifierCommandsEndToEnd runs two independently configured verifier
// commands against a TLS server whose CA is trusted by each command. It covers
// password admission, server-ID pinning, reciprocal peer-bound Opus proofs,
// and the safe reports produced by the actual command binary.
func TestVerifierCommandsEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Go creates a new temporary test executable on every run, so Windows
		// Firewall permissions cannot be made durable. Linux CI runs the full
		// trusted-WSS, two-endpoint media test.
		t.Skip("command E2E test is disabled on Windows to avoid Firewall prompts")
	}

	d := t.TempDir()
	ip := net.IPv4(127, 0, 0, 1)
	assertIPv4Loopback(t, ip.String())
	ca, cert, key := testCAAndServerCert(t, d, ip)
	cfg := server.Config{
		// Keep signaling and the server-owned UDP mux on an explicit IPv4
		// loopback literal. A wildcard/LAN binding causes Windows Firewall
		// prompts even though this is only an E2E test.
		Addr:             net.JoinHostPort(ip.String(), "0"),
		AllowLoopbackICE: true,
		Password:         "test-password",
		TLSCert:          cert,
		TLSKey:           key,
		Roles:            roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		VoiceChannels:    []string{"verify"},
	}
	s := server.New(cfg)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	assertIPv4Loopback(t, host)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("shutdown verifier test server: %v", err)
		}
	})

	bin := filepath.Join(d, "hermec-verify")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	testCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(testCtx, "go", "build", "-o", bin, ".").Run(); err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	ids := make([]*identity.Identity, 2)
	paths := make([]string, 2)
	fps := make([]string, 2)
	for i := range ids {
		var err error
		ids[i], err = identity.Generate()
		if err != nil {
			t.Fatal(err)
		}
		paths[i] = filepath.Join(d, string(rune('a'+i))+".identity")
		if err := ids[i].Save(paths[i]); err != nil {
			t.Fatal(err)
		}
		fps[i] = identity.Fingerprint(ids[i].PublicKey())
	}
	peers := []string{filepath.Join(d, "a.peer"), filepath.Join(d, "b.peer")}
	if err := os.WriteFile(peers[0], []byte(fps[1]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peers[1], []byte(fps[0]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reports := []string{filepath.Join(d, "a.json"), filepath.Join(d, "b.json")}

	cmds := make([]*exec.Cmd, 2)
	output := make([]bytes.Buffer, 2)
	// CommandContext handles the deadline; this cleanup also reaps every child
	// on an early test exit so Windows never retains a verifier/socket process.
	t.Cleanup(func() {
		cancel()
		for _, cmd := range cmds {
			if cmd != nil && cmd.Process != nil && cmd.ProcessState == nil {
				_ = cmd.Wait()
			}
		}
	})
	for i := range cmds {
		cmds[i] = exec.CommandContext(testCtx, bin, "-url", "wss://"+s.Addr()+"/", "-server-id", s.ID(), "-identity", paths[i], "-expected-peer", peers[i], "-channel", "verify", "-frames", "2", "-timeout", "20s", "-diagnostics", reports[i])
		// This test-only switch makes both child Pion stacks use IPv4 loopback
		// exclusively. It is not present in production verifier invocations.
		cmds[i].Env = append(os.Environ(), "SSL_CERT_FILE="+ca, testLoopbackICEEnv+"=1")
		cmds[i].Stdin = strings.NewReader("test-password\n")
		cmds[i].Stdout, cmds[i].Stderr = &output[i], &output[i]
		if err := cmds[i].Start(); err != nil {
			cancel()
			for _, started := range cmds[:i] {
				_ = started.Wait()
			}
			t.Fatal(err)
		}
	}
	errs := make([]error, len(cmds))
	for i, cmd := range cmds {
		errs[i] = cmd.Wait()
		if errs[i] != nil {
			// Do not leave the peer verifier running when its counterpart fails.
			cancel()
		}
	}
	for i, err := range errs {
		if err != nil {
			b, _ := os.ReadFile(reports[i])
			t.Fatalf("verifier failed: %v\n%s\nreport: %s", err, output[i].String(), b)
		}
	}
	for i, p := range reports {
		b, err := os.ReadFile(p)
		var got report
		if err != nil || json.Unmarshal(b, &got) != nil || !got.Success || got.ServerID != s.ID() || got.ExpectedPeer != fps[1-i] || got.PeerPresent != 1 || got.ValidFrames < 2 {
			t.Fatalf("unsafe or unsuccessful report %s: %s (%v)", p, b, err)
		}
	}
}

// assertIPv4Loopback guards the addresses added by this test. Do not replace
// the literal with localhost: name resolution can select IPv6 or an external
// address on misconfigured Windows hosts.
func assertIPv4Loopback(t *testing.T, host string) {
	t.Helper()
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || !ip.IsLoopback() {
		t.Fatalf("test address %q is not an IPv4 loopback literal", host)
	}
}

func testCAAndServerCert(t *testing.T, dir string, ip net.IP) (caPath, certPath, keyPath string) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caT := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "hermec verify test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caT, caT, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafT := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{ip}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafT, caT, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath, certPath, keyPath = filepath.Join(dir, "ca.pem"), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	write := func(path string, block *pem.Block) {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(caPath, &pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	write(certPath, &pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	write(keyPath, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	return
}
