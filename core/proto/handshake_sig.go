package proto

// Signature domains of the version 2 handshake. Each message starts with a
// distinct prefix so a signature made for one purpose cannot be replayed as
// another.
const (
	hostProofDomain  = "hermec-host-proof\x00"
	clientAuthDomain = "hermec-client-auth\x00"
)

func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// HostProofBytes is what the host key signs to prove itself to a client:
// the domain, then the client nonce, the server nonce and the server ID
// (the raw 32 bytes of chain.ID).
func HostProofBytes(clientNonce, serverNonce, serverID []byte) []byte {
	return join([]byte(hostProofDomain), clientNonce, serverNonce, serverID)
}

// ClientAuthBytes is what the client signs in its auth message: the domain,
// then the server nonce, the client nonce and the server ID. Binding the
// server ID stops a host from relaying the signature to another server.
func ClientAuthBytes(serverNonce, clientNonce, serverID []byte) []byte {
	return join([]byte(clientAuthDomain), serverNonce, clientNonce, serverID)
}
