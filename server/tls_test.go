package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
)

func writeSelfSigned(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestServerTLS(t *testing.T) {
	cert, key := writeSelfSigned(t)
	s := New(Config{Addr: "127.0.0.1:0", Channels: []string{"general"}, TLSCert: cert, TLSKey: key})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	d := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	ws, _, err := d.Dial("wss://"+s.Addr(), nil)
	if err != nil {
		t.Fatalf("wss dial: %v", err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.Decode(raw)
	if err != nil || env.Type != proto.TypeChallenge {
		t.Fatalf("first message = %+v, err %v", env, err)
	}
}

func TestServerTLSMismatchedConfig(t *testing.T) {
	s := New(Config{Addr: "127.0.0.1:0", TLSCert: "only-cert.pem"})
	if err := s.Start(); err == nil {
		t.Fatal("expected error when only TLSCert is set")
	}
	s = New(Config{Addr: "127.0.0.1:0", TLSCert: "missing.pem", TLSKey: "missing.key"})
	if err := s.Start(); err == nil {
		t.Fatal("expected error for unreadable keypair")
	}
}
