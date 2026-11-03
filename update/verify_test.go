package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 7
	priv := ed25519.NewKeyFromSeed(seed)
	return priv, base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}

func TestVerifySums(t *testing.T) {
	priv, pub := testKey(t)
	const tag = "v1.2.3"
	sums := []byte("abc  file\n")
	sig := Sign(priv, tag, sums)
	if err := VerifySums(pub, tag, sums, sig); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}
	if err := VerifySums(pub, tag, append(sums, "extra  x\n"...), sig); err == nil {
		t.Error("tampered sums accepted")
	}
	// The signature is bound to the tag: another tag, however it is written,
	// does not verify.
	for _, other := range []string{"v1.2.4", "v1.2.2", "v1.2.3 ", "", "v1.2.3\x00"} {
		if err := VerifySums(pub, other, sums, sig); err == nil {
			t.Errorf("signature for %q accepted for tag %q", tag, other)
		}
	}
	// A NUL in the tag could shift the tag/sums boundary: tag "a" + sums "b\x00c"
	// must not be confusable with tag "a\x00b" + sums "c".
	splitSig := Sign(priv, "a", []byte("b\x00c"))
	if err := VerifySums(pub, "a\x00b", []byte("c"), splitSig); err == nil {
		t.Error("tag containing NUL accepted")
	}
	// The v1 domain (no tag) must never verify.
	v1 := ed25519.Sign(priv, append([]byte("hermec-release-sums-v1\x00"), sums...))
	if err := VerifySums(pub, tag, sums, []byte(base64.StdEncoding.EncodeToString(v1))); err == nil {
		t.Error("v1 signature accepted")
	}
	// A signature made without the domain prefix must not verify.
	raw := ed25519.Sign(priv, sums)
	bare := []byte(base64.StdEncoding.EncodeToString(raw))
	if err := VerifySums(pub, tag, sums, bare); err == nil {
		t.Error("signature without the domain accepted")
	}
	otherSeed := make([]byte, ed25519.SeedSize)
	other := ed25519.NewKeyFromSeed(otherSeed)
	if err := VerifySums(pub, tag, sums, Sign(other, tag, sums)); err == nil {
		t.Error("signature by another key accepted")
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if err := VerifySums(pub, tag, sums, []byte(bad)); err == nil {
			t.Errorf("malformed signature %q accepted", bad)
		}
	}
}

func TestPlaceholderKeyFailsClosed(t *testing.T) {
	priv, _ := testKey(t)
	sums := []byte("abc  file\n")
	if err := VerifySums(PublicKey, "v1.2.3", sums, Sign(priv, "v1.2.3", sums)); err == nil {
		t.Fatal("the placeholder public key must verify nothing")
	}
	if err := VerifySums("", "v1.2.3", sums, Sign(priv, "v1.2.3", sums)); err == nil {
		t.Fatal("an empty public key must verify nothing")
	}
}

func TestParseSums(t *testing.T) {
	h := sha256.Sum256([]byte("x"))
	hx := hex.EncodeToString(h[:])
	got, err := ParseSums([]byte(hx + "  a.zip\n" + hx + " *b.tar.gz\r\n\n"))
	if err != nil || got["a.zip"] != h || got["b.tar.gz"] != h || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{
		"zz  a\n",
		hx + " a\n",
		hx + "\n",
		hx[:62] + "  a\n",
		hx + "  a\n" + strings.Repeat("0", 64) + "  a\n",
	} {
		if _, err := ParseSums([]byte(bad)); err == nil {
			t.Errorf("ParseSums(%q) accepted", bad)
		}
	}
}
