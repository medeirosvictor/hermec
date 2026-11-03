package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeygenSignVerify(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "release.key")
	if err := run([]string{"keygen", "-out", keyPath}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"keygen", "-out", keyPath}); err == nil {
		t.Fatal("keygen overwrote an existing key")
	}
	seed, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(keyPath); st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("key mode %v", st.Mode())
	}

	// Recover the public key the way an operator would: from keygen's stdout.
	r, w, _ := os.Pipe()
	stdout := os.Stdout
	os.Stdout = w
	keyPath2 := filepath.Join(dir, "second.key")
	err = run([]string{"keygen", "-out", keyPath2})
	w.Close()
	os.Stdout = stdout
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	pub := strings.TrimSpace(string(buf[:n]))
	seed2, _ := os.ReadFile(keyPath2)

	sums := filepath.Join(dir, "SHA256SUMS.txt")
	os.WriteFile(sums, []byte(strings.Repeat("0", 64)+"  a.zip\n"), 0o644)
	t.Setenv("TEST_RELEASE_KEY", strings.TrimSpace(string(seed2)))
	if err := run([]string{"sign", "-key-env", "TEST_RELEASE_KEY", sums}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-pub", pub, sums}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// The first key's seed must not sign for the second key's public key.
	t.Setenv("TEST_RELEASE_KEY", strings.TrimSpace(string(seed)))
	run([]string{"sign", "-key-env", "TEST_RELEASE_KEY", sums})
	if err := run([]string{"verify", "-pub", pub, sums}); err == nil {
		t.Fatal("verified a signature from another key")
	}
}

func TestSignRefusesMissingKey(t *testing.T) {
	sums := filepath.Join(t.TempDir(), "SHA256SUMS.txt")
	os.WriteFile(sums, []byte("x\n"), 0o644)
	t.Setenv("TEST_RELEASE_KEY", "")
	if err := run([]string{"sign", "-key-env", "TEST_RELEASE_KEY", sums}); err == nil {
		t.Fatal("signed without a key")
	}
	if _, err := os.Stat(sums + ".sig"); err == nil {
		t.Fatal("wrote a signature without a key")
	}
}
