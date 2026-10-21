package proto

import "testing"

func TestHandshakeSignatureBytes(t *testing.T) {
	cn, sn, id := []byte("C"), []byte("S"), []byte("I")
	if got, want := string(HostProofBytes(cn, sn, id)), "hermec-host-proof\x00CSI"; got != want {
		t.Errorf("host proof = %q, want %q", got, want)
	}
	if got, want := string(ClientAuthBytes(sn, cn, id)), "hermec-client-auth\x00SCI"; got != want {
		t.Errorf("client auth = %q, want %q", got, want)
	}
}
