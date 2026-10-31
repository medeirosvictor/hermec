package proto

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

func TestDedicatedReadinessIsDomainSeparatedAndNonceBound(t *testing.T) {
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, DedicatedTransitionNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	r := DedicatedReadiness{ServerID: "server", DestinationFingerprint: identity.Fingerprint(key.PublicKey()), ChainHead: 7, ChainHeadHash: make([]byte, 32), HistoryLastID: "line", HistoryLastUnixMilli: 12, HistoryHash: make([]byte, 32), TransitionNonce: nonce}
	sig, err := SignDedicatedReadiness(key, r)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyDedicatedReadiness(key.PublicKey(), r, sig) {
		t.Fatal("valid readiness proof rejected")
	}
	other := r
	other.TransitionNonce = append([]byte(nil), nonce...)
	other.TransitionNonce[0] ^= 1
	if VerifyDedicatedReadiness(key.PublicKey(), other, sig) {
		t.Fatal("proof replayed for another nonce")
	}
	other = r
	other.DestinationFingerprint = "another-destination"
	if VerifyDedicatedReadiness(key.PublicKey(), other, sig) {
		t.Fatal("proof accepted for a fingerprint other than its public key")
	}
	b, err := DedicatedReadinessBytes(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("hermec-dedicated-readiness-v1\x00")) {
		t.Fatalf("missing readiness domain: %q", b)
	}
}

func TestDedicatedHostInfoIsBoundedBeforeSigning(t *testing.T) {
	r := DedicatedReadiness{ServerID: "server", DestinationFingerprint: "destination", ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, DedicatedTransitionNonceSize)}
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignDedicatedCutover(key, r, identity.Fingerprint(key.PublicKey()), HostInfo{Fingerprint: "destination", Name: string(make([]byte, MaxDedicatedHostName+1))}); err == nil {
		t.Fatal("signed oversized host info")
	}
}

func TestDedicatedCutoverBindsBothHostsAndHostInfo(t *testing.T) {
	source, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	destination, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r := DedicatedReadiness{ServerID: "server", DestinationFingerprint: identity.Fingerprint(destination.PublicKey()), ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, DedicatedTransitionNonceSize)}
	dst := HostInfo{Fingerprint: r.DestinationFingerprint, Name: "vps", Addrs: []string{"wss://vps.example"}, Priority: 2}
	sig, err := SignDedicatedCutover(source, r, identity.Fingerprint(source.PublicKey()), dst)
	if err != nil {
		t.Fatal(err)
	}
	cert := DedicatedCertificate{Readiness: r, SourceFingerprint: identity.Fingerprint(source.PublicKey()), Destination: dst, Signature: sig}
	if !VerifyDedicatedCutover(source.PublicKey(), cert) {
		t.Fatal("valid cutover rejected")
	}
	cert.Destination.Addrs[0] = "wss://attacker.example"
	if VerifyDedicatedCutover(source.PublicKey(), cert) {
		t.Fatal("changed destination host info accepted")
	}
}

func TestDedicatedResultValidationAndCorrelationFields(t *testing.T) {
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ready := DedicatedReadiness{ServerID: "s", DestinationFingerprint: identity.Fingerprint(key.PublicKey()), ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, DedicatedTransitionNonceSize)}
	for _, result := range []DedicatedResult{
		{Ref: "request", Status: "pending"},
		{Ref: "request", Status: "retry"},
		{Ref: "request", Status: "error"},
		{Ref: "request", Status: "ready", Readiness: &ready},
		{Ref: "request", Status: "success", Readiness: &ready},
	} {
		if err := result.Validate(); err != nil {
			t.Fatalf("valid result %+v: %v", result, err)
		}
	}
	for _, result := range []DedicatedResult{
		{Status: "pending"},
		{Ref: "request", Status: "unknown"},
		{Ref: "request", Status: "success"},
		{Ref: "request", Status: "retry", Readiness: &ready},
	} {
		if err := result.Validate(); err == nil {
			t.Fatalf("invalid result accepted: %+v", result)
		}
	}
}

func TestDedicatedReadinessBounds(t *testing.T) {
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	r := DedicatedReadiness{ServerID: "s", DestinationFingerprint: identity.Fingerprint(key.PublicKey()), ChainHeadHash: make([]byte, 32), HistoryHash: make([]byte, 32), TransitionNonce: make([]byte, DedicatedTransitionNonceSize)}
	p := DedicatedReadinessProof{Readiness: r, Signature: make([]byte, 64)}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	r.TransitionNonce = nil
	if err := r.Validate(); err == nil {
		t.Fatal("short nonce accepted")
	}
}

func TestCanonicalDedicatedWSS(t *testing.T) {
	for _, raw := range []string{
		"", "ws://example.test/", "wss://Example.test/", "wss://example.test/path", "wss://example.test/?q=1",
		"wss://example.test.:443/", "wss://example.test:0443/", " wss://example.test/", "wss://exa mple.test/",
		"wss://[2001:DB8::1]/", "wss://[2001:0db8::1]/",
		"wss://user@example.test/", "wss://example.test/#x",
	} {
		if _, err := CanonicalDedicatedWSS(raw); err == nil {
			t.Fatalf("accepted noncanonical URL %q", raw)
		}
	}
	for _, raw := range []string{"wss://example.test/", "wss://example.test:443/", "wss://[2001:db8::1]/"} {
		if got, err := CanonicalDedicatedWSS(raw); err != nil || got != raw {
			t.Fatalf("canonical URL %q = %q, %v", raw, got, err)
		}
	}
}
