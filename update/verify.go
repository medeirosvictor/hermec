package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// PublicKey is the base64 Ed25519 public key that release checksums must be
// signed with (see docs/release-signing.md). While it is the placeholder below
// it does not decode to a key, so every verification fails and no update is
// ever installed: the app fails closed until the real key is set here.
const PublicKey = "UNSET-run-hermec-release-keygen-and-paste-the-public-key-here"

// SigDomain prefixes the signed bytes so a signature over SHA256SUMS.txt cannot
// be confused with a signature made for anything else with the same key. The
// release tag is signed too (SigDomain || tag || 0x00 || sums), so a validly
// signed sums file of an older release cannot be served as a newer one. The
// domain was bumped from v1 when the tag was added; v1 is never accepted.
const SigDomain = "hermec-release-v2\x00"

// SumsName and SigName are the release assets holding the checksums and their
// detached signature.
const (
	SumsName = "SHA256SUMS.txt"
	SigName  = SumsName + ".sig"
)

// Sign returns the detached signature file content for the sums of release
// tag: base64 of ed25519.Sign(priv, SigDomain || tag || 0x00 || sums).
func Sign(priv ed25519.PrivateKey, tag string, sums []byte) []byte {
	sig := ed25519.Sign(priv, signedMessage(tag, sums))
	return []byte(base64.StdEncoding.EncodeToString(sig) + "\n")
}

// VerifySums checks that sig (the content of a .sig file) is a valid signature
// of sums for release tag under the base64 public key pub. A tag that is empty
// or contains a NUL (which would blur the tag/sums boundary) never verifies.
func VerifySums(pub, tag string, sums, sig []byte) error {
	if tag == "" || strings.ContainsRune(tag, 0) {
		return errors.New("update: bad release tag")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pub))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return errors.New("update: no valid release public key is set")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return errors.New("update: malformed signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(raw), signedMessage(tag, sums), s) {
		return errors.New("update: signature does not match")
	}
	return nil
}

func signedMessage(tag string, sums []byte) []byte {
	m := append([]byte(SigDomain), tag...)
	m = append(m, 0)
	return append(m, sums...)
}

// ParseSums reads sha256sum output ("<64 hex>  name" or "<64 hex> *name").
// Malformed lines are an error rather than skipped, since the file is signed
// and a bad line means something is wrong with it. A name listed twice with
// different hashes is also an error.
func ParseSums(sums []byte) (map[string][32]byte, error) {
	out := map[string][32]byte{}
	for _, line := range strings.Split(string(sums), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		hexSum, name, ok := strings.Cut(line, " ")
		if !ok || len(hexSum) != 64 || len(name) < 2 || (name[0] != ' ' && name[0] != '*') {
			return nil, fmt.Errorf("update: malformed checksum line %q", line)
		}
		name = name[1:]
		var h [32]byte
		if b, err := hex.DecodeString(hexSum); err != nil || len(b) != 32 {
			return nil, fmt.Errorf("update: malformed checksum line %q", line)
		} else {
			copy(h[:], b)
		}
		if prev, dup := out[name]; dup && prev != h {
			return nil, fmt.Errorf("update: conflicting checksums for %s", name)
		}
		out[name] = h
	}
	return out, nil
}
