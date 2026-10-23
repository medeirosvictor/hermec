package proto

import (
	"crypto/hmac"
	"crypto/sha256"

	"golang.org/x/crypto/scrypt"
)

// pwDomain separates the password key salt and proof from every other use of
// the server ID and nonces.
const pwDomain = "hermec-pw\x00"

// PasswordKey derives the 32-byte key a host keeps instead of the server
// password: scrypt(password, "hermec-pw\0" || server_id) with N=32768, r=8,
// p=1. The same server ID and password always give the same key.
func PasswordKey(password string, serverID []byte) ([]byte, error) {
	salt := append([]byte(pwDomain), serverID...)
	return scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
}

// PasswordProof is the client's proof that it knows the password behind key:
// HMAC-SHA256(key, "hermec-pw\0" || server_nonce || client_nonce || server_id).
// It replaces Auth.Password, so the password never goes over the wire.
func PasswordProof(key, serverNonce, clientNonce, serverID []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(pwDomain))
	m.Write(serverNonce)
	m.Write(clientNonce)
	m.Write(serverID)
	return m.Sum(nil)
}
