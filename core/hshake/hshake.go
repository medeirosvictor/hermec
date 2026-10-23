// Package hshake is the client side of the version 2 mutual handshake. The
// client package builds on it, and so does a host dialing another host; it
// lives apart so the server can use it without importing client.
package hshake

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

const (
	// DefaultAuthTimeout bounds the handshake when ctx has no earlier deadline.
	DefaultAuthTimeout = 10 * time.Second
	nonceSize          = 32
)

// Bounds on what a host may send before it has proven itself. The chain
// itself is capped at 2048 entries and 512 KiB; the margins allow for JSON
// overhead but stop a hostile host from streaming without end.
const (
	maxChainFrames = 64
	maxChainBytes  = 2 << 20
)

// ServerError is an error message sent by the server.
type ServerError struct {
	Code    string
	Message string
	Gen     uint32 // gen of the refused watch request, 0 if none
	// Hosts is the host list a standby sent just before refusing a full
	// session with not_active; nil otherwise.
	Hosts *proto.Hosts
}

func (e *ServerError) Error() string { return e.Code + ": " + e.Message }

// Opts are the optional parts of a handshake.
type Opts struct {
	// Pin is a chain.ID.String() the server must match; "" trusts the first ID.
	Pin string
	// PasswordKey, when set, is the key derived from the password for this
	// server; it skips the scrypt derivation and the password argument.
	PasswordKey []byte
	// Warm asks for a warm (paused) session, as a standby's pre-connection or
	// a host link.
	Warm bool
	// MinChainSeq is the lowest chain head Seq the host may present; a lower
	// head is a rollback and fails with bad_chain. 0 accepts any chain.
	MinChainSeq uint64
}

// Result is what a successful handshake yields.
type Result struct {
	OK       proto.AuthOK
	Chain    *chain.Chain // the host's verified chain
	ServerID string       // Chain.ID().String()
	Key      []byte       // the password key used, nil when none
}

// Run is handshake bounded by ctx: a deadline (DefaultAuthTimeout at most)
// applies to the socket, and ws is closed if ctx ends first. On success the
// deadlines are cleared. It does not close ws on error.
func Run(ctx context.Context, ws *websocket.Conn, id *identity.Identity, name, password string, o Opts) (*Result, error) {
	if o.Pin != "" {
		if _, err := chain.ParseID(o.Pin); err != nil {
			return nil, fmt.Errorf("client: bad pin: %w", err)
		}
	}
	ws.SetReadLimit(proto.MaxMessageSize)
	deadline := time.Now().Add(DefaultAuthTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = ws.SetReadDeadline(deadline)
	_ = ws.SetWriteDeadline(deadline)
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-ctx.Done():
			ws.Close()
		case <-stop:
		}
	}()
	res, err := handshake(ws, id, name, password, o)
	close(stop)
	<-watched
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	_ = ws.SetWriteDeadline(time.Time{})
	return res, nil
}

// handshake runs the version 2 mutual handshake: the host proves itself with
// the signed chain before the password is sent, then the client authenticates
// with a signature bound to the server ID.
func handshake(ws *websocket.Conn, id *identity.Identity, name, password string, o Opts) (*Result, error) {
	// The challenge is a v1 envelope, so a v1 host looks the same until it
	// answers host_proof_req.
	env, err := readHS(ws)
	if err != nil {
		return nil, fmt.Errorf("client: read challenge: %w", err)
	}
	if env.Type != proto.TypeChallenge {
		return nil, fmt.Errorf("client: expected challenge, got %q", env.Type)
	}
	var ch proto.Challenge
	if err := json.Unmarshal(env.Data, &ch); err != nil || len(ch.Nonce) == 0 {
		return nil, fmt.Errorf("client: bad challenge")
	}
	clientNonce := make([]byte, nonceSize)
	if _, err := rand.Read(clientNonce); err != nil {
		return nil, err
	}
	raw, err := proto.Encode(proto.TypeHostProofReq, proto.HostProofReq{Nonce: clientNonce})
	if err != nil {
		return nil, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return nil, fmt.Errorf("client: send host_proof_req: %w", err)
	}

	var entries []chain.Entry
	var total int
	for frames := 0; ; frames++ {
		env, err = readHS(ws)
		if err != nil {
			return nil, fmt.Errorf("client: read chain: %w", err)
		}
		if busy := busyError(env); busy != nil {
			return nil, fmt.Errorf("client: %w", busy)
		}
		if env.V != proto.Version {
			// A v1 host answers host_proof_req with an error (or nothing it
			// understands) instead of a chain.
			return nil, fmt.Errorf("client: %w", errOutdated())
		}
		if env.Type == proto.TypeError {
			return nil, fmt.Errorf("client: %w", handshakeError(env))
		}
		if env.Type != proto.TypeChain {
			return nil, fmt.Errorf("client: %w", badChain("expected chain, got %q", env.Type))
		}
		if frames >= maxChainFrames {
			return nil, fmt.Errorf("client: %w", badChain("too many chain frames"))
		}
		var cf proto.ChainFrame
		if err := json.Unmarshal(env.Data, &cf); err != nil {
			return nil, fmt.Errorf("client: %w", badChain("malformed chain frame"))
		}
		for _, r := range cf.Entries {
			total += len(r)
		}
		if total > maxChainBytes {
			return nil, fmt.Errorf("client: %w", badChain("chain too large"))
		}
		es, err := DecodeEntries(cf.Entries)
		if err != nil {
			return nil, fmt.Errorf("client: %w", err)
		}
		entries = append(entries, es...)
		if !cf.More {
			break
		}
	}

	env, err = readHS(ws)
	if err != nil {
		return nil, fmt.Errorf("client: read host_proof: %w", err)
	}
	if env.V != proto.Version {
		return nil, fmt.Errorf("client: %w", errOutdated())
	}
	if env.Type == proto.TypeError {
		return nil, fmt.Errorf("client: %w", handshakeError(env))
	}
	var hp proto.HostProof
	if env.Type != proto.TypeHostProof || json.Unmarshal(env.Data, &hp) != nil {
		return nil, fmt.Errorf("client: %w", badChain("expected host_proof, got %q", env.Type))
	}
	verified, err := verifyHost(entries, o, hp, clientNonce, ch.Nonce)
	if err != nil {
		return nil, fmt.Errorf("client: %w", err)
	}

	sid := verified.ID()
	pwKey := o.PasswordKey
	if len(pwKey) == 0 && password != "" {
		if pwKey, err = proto.PasswordKey(password, sid[:]); err != nil {
			return nil, fmt.Errorf("client: derive password key: %w", err)
		}
	}
	a := proto.Auth{
		PubKey: id.PublicKey(), Name: name, Warm: o.Warm,
		Sig: id.Sign(proto.ClientAuthBytes(ch.Nonce, clientNonce, sid[:])),
	}
	if len(pwKey) > 0 {
		a.Proof = proto.PasswordProof(pwKey, ch.Nonce, clientNonce, sid[:])
	}
	raw, err = proto.Encode(proto.TypeAuth, a)
	if err != nil {
		return nil, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return nil, fmt.Errorf("client: send auth: %w", err)
	}
	env, err = readEnv(ws)
	if err != nil {
		return nil, fmt.Errorf("client: read auth result: %w", err)
	}
	switch env.Type {
	case proto.TypeAuthOK:
		var ok proto.AuthOK
		if err := json.Unmarshal(env.Data, &ok); err != nil {
			return nil, fmt.Errorf("client: bad auth_ok: %w", err)
		}
		if ok.ServerID != sid.String() {
			return nil, fmt.Errorf("client: %w", &ServerError{Code: proto.CodeServerIDMismatch, Message: "the host reported a different server id"})
		}
		return &Result{OK: ok, Chain: verified, ServerID: ok.ServerID, Key: pwKey}, nil
	case proto.TypeHosts:
		// A standby lists the hosts, then refuses with not_active.
		var hs proto.Hosts
		if err := json.Unmarshal(env.Data, &hs); err != nil {
			return nil, fmt.Errorf("client: bad hosts: %w", err)
		}
		env, err = readEnv(ws)
		if err != nil {
			return nil, fmt.Errorf("client: read auth result: %w", err)
		}
		if env.Type != proto.TypeError {
			return nil, fmt.Errorf("client: unexpected %q during auth", env.Type)
		}
		se := handshakeError(env)
		se.Hosts = &hs
		return nil, fmt.Errorf("client: auth rejected: %w", se)
	case proto.TypeError:
		return nil, fmt.Errorf("client: auth rejected: %w", handshakeError(env))
	default:
		return nil, fmt.Errorf("client: unexpected %q during auth", env.Type)
	}
}

// busyError is the host's explicit server_busy refusal (the pre-auth chain
// budget), sent as a v1-compatible error frame so any client can read it. It
// is retryable. Nothing else maps to it: a bare close is just a lost
// connection.
func busyError(env proto.Envelope) *ServerError {
	if env.Type != proto.TypeError {
		return nil
	}
	if se := handshakeError(env); se.Code == proto.CodeServerBusy {
		return se
	}
	return nil
}

func errOutdated() *ServerError {
	return &ServerError{Code: proto.CodeServerOutdated, Message: "the host runs an older HERMEC"}
}

func handshakeError(env proto.Envelope) *ServerError {
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	return &ServerError{Code: em.Code, Message: em.Message}
}

// readHS reads one handshake message, accepting both protocol versions.
func readHS(ws *websocket.Conn) (proto.Envelope, error) {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return proto.Envelope{}, err
	}
	return proto.DecodeHandshake(raw)
}

func readEnv(ws *websocket.Conn) (proto.Envelope, error) {
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return proto.Envelope{}, err
	}
	return proto.Decode(raw)
}

// BadChain is a bad_chain ServerError.
func BadChain(format string, args ...any) *ServerError { return badChain(format, args...) }

func badChain(format string, args ...any) *ServerError {
	return &ServerError{Code: proto.CodeBadChain, Message: fmt.Sprintf(format, args...)}
}

// DecodeEntries parses the raw entries of a chain transfer.
func DecodeEntries(raws []json.RawMessage) ([]chain.Entry, error) {
	out := make([]chain.Entry, 0, len(raws))
	for i, r := range raws {
		var e chain.Entry
		if err := json.Unmarshal(r, &e); err != nil {
			return nil, badChain("entry %d is malformed", i)
		}
		out = append(out, e)
	}
	return out, nil
}

// verifyHost checks everything the host presented before the client sends
// its password: the chain verifies and matches o.Pin (if any), is not older than o.MinChainSeq, the proving key
// is on the chain's host list, and the proof signature is valid for these
// nonces. It returns the verified chain.
func verifyHost(entries []chain.Entry, o Opts, hp proto.HostProof, clientNonce, serverNonce []byte) (*chain.Chain, error) {
	if len(entries) == 0 {
		return nil, badChain("the host sent an empty chain")
	}
	// The genesis hash is the server ID: compare it to the pin first, so a
	// different server is reported as such rather than as a broken chain.
	if o.Pin != "" && chain.IDOf(entries[0]).String() != o.Pin {
		return nil, &ServerError{Code: proto.CodeServerIDMismatch, Message: "this is not the server you saved"}
	}
	ch, err := chain.FromEntries(entries)
	if err != nil {
		if errors.Is(err, chain.ErrFull) || errors.Is(err, chain.ErrBadChain) {
			return nil, badChain("%v", err)
		}
		return nil, badChain("chain rejected")
	}
	if ch.Head().Seq < o.MinChainSeq {
		return nil, badChain("host served an older chain")
	}
	id := ch.ID()
	if len(hp.PubKey) != ed25519.PublicKeySize {
		return nil, &ServerError{Code: proto.CodeHostNotAuthorized, Message: "the host sent no valid key"}
	}
	pub := ed25519.PublicKey(hp.PubKey)
	st := ch.State()
	if !st.IsHost(pub) {
		return nil, &ServerError{Code: proto.CodeHostNotAuthorized, Message: "the host is not an approved host of this server"}
	}
	if !identity.Verify(pub, proto.HostProofBytes(clientNonce, serverNonce, id[:]), hp.Sig) {
		return nil, &ServerError{Code: proto.CodeHostNotAuthorized, Message: "the host could not prove its key"}
	}
	return ch, nil
}
