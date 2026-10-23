package proto

import (
	"bytes"
	"testing"
)

func TestPasswordKeyDeterministic(t *testing.T) {
	a, err := PasswordKey("pw", []byte("server-one"))
	if err != nil || len(a) != 32 {
		t.Fatalf("key = %x err=%v", a, err)
	}
	b, _ := PasswordKey("pw", []byte("server-one"))
	if !bytes.Equal(a, b) {
		t.Fatal("same input gave different keys")
	}
	c, _ := PasswordKey("pw", []byte("server-two"))
	if bytes.Equal(a, c) {
		t.Fatal("different server id gave the same key")
	}
	d, _ := PasswordKey("pw2", []byte("server-one"))
	if bytes.Equal(a, d) {
		t.Fatal("different password gave the same key")
	}
}

func TestPasswordProofBindsInputs(t *testing.T) {
	k := bytes.Repeat([]byte{1}, 32)
	base := PasswordProof(k, []byte("s"), []byte("c"), []byte("id"))
	for name, p := range map[string][]byte{
		"key":          PasswordProof(bytes.Repeat([]byte{2}, 32), []byte("s"), []byte("c"), []byte("id")),
		"server nonce": PasswordProof(k, []byte("x"), []byte("c"), []byte("id")),
		"client nonce": PasswordProof(k, []byte("s"), []byte("x"), []byte("id")),
		"server id":    PasswordProof(k, []byte("s"), []byte("c"), []byte("xx")),
	} {
		if bytes.Equal(base, p) {
			t.Errorf("proof ignores %s", name)
		}
	}
	if !bytes.Equal(base, PasswordProof(k, []byte("s"), []byte("c"), []byte("id"))) {
		t.Error("proof not deterministic")
	}
}
