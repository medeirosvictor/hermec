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
	sums := []byte("abc  file\n")
	sig := Sign(priv, sums)
	if err := VerifySums(pub, sums, sig); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}
	if err := VerifySums(pub, []byte("abc  file\nextra  x\n"), sig); err == nil {
		t.Error("tampered sums accepted")
	}
	// A signature made without the domain prefix must not verify.
	raw := ed25519.Sign(priv, sums)
	bare := []byte(base64.StdEncoding.EncodeToString(raw))
	if err := VerifySums(pub, sums, bare); err == nil {
		t.Error("signature without the domain accepted")
	}
	otherSeed := make([]byte, ed25519.SeedSize)
	other := ed25519.NewKeyFromSeed(otherSeed)
	if err := VerifySums(pub, sums, Sign(other, sums)); err == nil {
		t.Error("signature by another key accepted")
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if err := VerifySums(pub, sums, []byte(bad)); err == nil {
			t.Errorf("malformed signature %q accepted", bad)
		}
	}
}

func TestPlaceholderKeyFailsClosed(t *testing.T) {
	priv, _ := testKey(t)
	sums := []byte("abc  file\n")
	if err := VerifySums(PublicKey, sums, Sign(priv, sums)); err == nil {
		t.Fatal("the placeholder public key must verify nothing")
	}
	if err := VerifySums("", sums, Sign(priv, sums)); err == nil {
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
