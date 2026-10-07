package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func TestGenerateSignVerify(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello")
	sig := id.Sign(msg)
	if !Verify(id.PublicKey(), msg, sig) {
		t.Fatal("valid signature rejected")
	}
	if Verify(id.PublicKey(), []byte("hellO"), sig) {
		t.Fatal("tampered message accepted")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id.key")
	if err := id.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !id.PublicKey().Equal(got.PublicKey()) {
		t.Fatal("public key mismatch after load")
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("perm = %o, want 600", st.Mode().Perm())
		}
	}
}

func TestSaveRefusesOverwrite(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id.key")
	if err := id.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path); err == nil {
		t.Fatal("second Save should fail")
	}
}

func TestFingerprintStableAndFormatted(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	fa := Fingerprint(a.PublicKey())
	if fa != Fingerprint(a.PublicKey()) {
		t.Fatal("fingerprint not stable")
	}
	if !regexp.MustCompile(`^[a-z2-7]{4}(-[a-z2-7]{4}){3}$`).MatchString(fa) {
		t.Fatalf("bad format %q", fa)
	}
	if fa == Fingerprint(b.PublicKey()) {
		t.Fatal("different keys share fingerprint")
	}
}
