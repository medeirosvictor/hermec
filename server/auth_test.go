package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func startServer(t *testing.T, cfg Config) string {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return "ws://" + s.Addr()
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c
}

func readEnv(t *testing.T, c *websocket.Conn) proto.Envelope {
	t.Helper()
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := proto.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

// readEnvSkipRoster is readEnv that ignores roster frames, which the server
// broadcasts on every connect and disconnect.
func readEnvSkipRoster(t *testing.T, c *websocket.Conn) proto.Envelope {
	t.Helper()
	for {
		if env := readEnv(t, c); env.Type != proto.TypeRoster {
			return env
		}
	}
}

func readChallenge(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := proto.DecodeHandshake(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.V != proto.LegacyVersion {
		t.Fatalf("challenge envelope v = %d, want %d", env.V, proto.LegacyVersion)
	}
	if env.Type != proto.TypeChallenge {
		t.Fatalf("first message type = %q, want challenge", env.Type)
	}
	var ch proto.Challenge
	if err := json.Unmarshal(env.Data, &ch); err != nil {
		t.Fatalf("challenge payload: %v", err)
	}
	if len(ch.Nonce) != 32 {
		t.Fatalf("nonce length = %d, want 32", len(ch.Nonce))
	}
	return ch.Nonce
}

func sendEnv(t *testing.T, c *websocket.Conn, typ string, payload any) {
	t.Helper()
	raw, err := proto.Encode(typ, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := c.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// authAs dials url, answers the challenge as id (signing the nonce), and
// returns the connection plus the server's verdict.
func authAs(t *testing.T, url string, id *identity.Identity, password string) (*websocket.Conn, proto.Envelope) {
	t.Helper()
	c := dial(t, url)
	h := hello(t, c)
	h.sendAuth(t, c, id, "tester", password)
	env := readEnv(t, c)
	if env.Type == proto.TypeAuthOK {
		expectHosts(t, c)
	}
	return c, env
}

// expectHosts reads the hosts frame a full session gets right after auth_ok.
func expectHosts(t *testing.T, c *websocket.Conn) {
	t.Helper()
	if env := readEnv(t, c); env.Type != proto.TypeHosts {
		t.Fatalf("after auth_ok: %q, want hosts", env.Type)
	}
}

// hs is the state of a version 2 handshake after the host has proven itself.
type hs struct {
	serverNonce, clientNonce []byte
	serverID                 chain.ID
	chain                    *chain.Chain
	hostProof                proto.HostProof
	frames                   int
}

// hello runs steps 1-3 of the handshake on a fresh connection: read the
// challenge, send host_proof_req, collect the chain frames and the proof.
func hello(t *testing.T, c *websocket.Conn) *hs {
	t.Helper()
	h := &hs{serverNonce: readChallenge(t, c), clientNonce: make([]byte, 32)}
	if _, err := rand.Read(h.clientNonce); err != nil {
		t.Fatal(err)
	}
	sendEnv(t, c, proto.TypeHostProofReq, proto.HostProofReq{Nonce: h.clientNonce})
	var entries []chain.Entry
	for {
		env := readEnv(t, c)
		if env.Type != proto.TypeChain {
			t.Fatalf("type = %q, want chain", env.Type)
		}
		var f proto.ChainFrame
		if err := json.Unmarshal(env.Data, &f); err != nil {
			t.Fatal(err)
		}
		h.frames++
		for _, raw := range f.Entries {
			var e chain.Entry
			if err := json.Unmarshal(raw, &e); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, e)
		}
		if !f.More {
			break
		}
	}
	var err error
	if h.chain, err = chain.FromEntries(entries); err != nil {
		t.Fatalf("chain: %v", err)
	}
	h.serverID = h.chain.ID()
	env := readEnv(t, c)
	if env.Type != proto.TypeHostProof {
		t.Fatalf("type = %q, want host_proof", env.Type)
	}
	if err := json.Unmarshal(env.Data, &h.hostProof); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *hs) sendAuth(t *testing.T, c *websocket.Conn, id *identity.Identity, name, password string) {
	t.Helper()
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: name, Password: password,
		Sig: id.Sign(proto.ClientAuthBytes(h.serverNonce, h.clientNonce, h.serverID[:])),
	})
}

func TestHostProofVerifies(t *testing.T) {
	key := mustID(t)
	cfg := Config{HostKey: key}
	url := startServer(t, cfg)
	c := dial(t, url)
	h := hello(t, c)
	if !identity.Verify(ed25519.PublicKey(h.hostProof.PubKey), proto.HostProofBytes(h.clientNonce, h.serverNonce, h.serverID[:]), h.hostProof.Sig) {
		t.Fatal("host proof does not verify")
	}
	if !bytes.Equal(h.hostProof.PubKey, key.PublicKey()) {
		t.Fatal("host proof key is not the configured host key")
	}
	st := h.chain.State()
	if !st.IsHost(ed25519.PublicKey(h.hostProof.PubKey)) {
		t.Fatal("proving key is not on the chain's host list")
	}
}

// A v1 client (auth right after the challenge) is told to update, once, in a
// v1 envelope, and is not counted as an auth failure.
func TestV1ClientGetsUpdateRequired(t *testing.T) {
	url := startServer(t, Config{})
	id := mustID(t)
	c := dial(t, url)
	nonce := readChallenge(t, c)
	raw, err := proto.EncodeV(proto.LegacyVersion, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "old", Sig: id.Sign(nonce),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatal(err)
	}
	_, raw, err = c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.DecodeHandshake(raw)
	if err != nil {
		t.Fatal(err)
	}
	if env.V != proto.LegacyVersion || env.Type != proto.TypeError {
		t.Fatalf("got v%d %q, want v1 error", env.V, env.Type)
	}
	var em proto.ErrorMsg
	if err := json.Unmarshal(env.Data, &em); err != nil {
		t.Fatal(err)
	}
	if em.Code != proto.CodeUpdateRequired {
		t.Fatalf("code = %q, want update_required", em.Code)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection still open")
	}
}

func TestBannedKeyRefused(t *testing.T) {
	hostKey, bad := mustID(t), mustID(t)
	cfg := DefaultConfig()
	ch, err := newChain(hostKey, cfg)
	if err != nil {
		t.Fatal(err)
	}
	head := ch.Head()
	e, err := chain.NewEntry(hostKey, &head, chain.OpBan, chain.FPBody{Fingerprint: identity.Fingerprint(bad.PublicKey())}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Append(e); err != nil {
		t.Fatal(err)
	}
	url := startServer(t, Config{HostKey: hostKey, Chain: ch})
	c, env := authAs(t, url, bad, "")
	if env.Type != proto.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	if em.Code != proto.CodeBanned {
		t.Fatalf("code = %q, want banned", em.Code)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection still open")
	}
	// A refused ban does not lock the IP out: another key still gets in.
	if _, env := authAs(t, url, mustID(t), ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("other key: type = %q, want auth_ok", env.Type)
	}
}

// A signature over the old raw nonce (no domain, no server id) is refused.
func TestClientSigBoundToServerID(t *testing.T) {
	url := startServer(t, Config{})
	id := mustID(t)
	c := dial(t, url)
	h := hello(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{PubKey: id.PublicKey(), Name: "x", Sig: id.Sign(h.serverNonce)})
	requireAuthFailed(t, c, readEnv(t, c))

	// Signed for another server ID: also refused.
	c = dial(t, url)
	h = hello(t, c)
	other := h.serverID
	other[0] ^= 1
	sendEnv(t, c, proto.TypeAuth, proto.Auth{PubKey: id.PublicKey(), Name: "x",
		Sig: id.Sign(proto.ClientAuthBytes(h.serverNonce, h.clientNonce, other[:]))})
	requireAuthFailed(t, c, readEnv(t, c))
}

func TestAuthOKCarriesServerID(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	h := hello(t, c)
	h.sendAuth(t, c, mustID(t), "x", "")
	env := readEnv(t, c)
	var ok proto.AuthOK
	if env.Type != proto.TypeAuthOK || json.Unmarshal(env.Data, &ok) != nil {
		t.Fatalf("type = %q", env.Type)
	}
	if ok.ServerID != h.serverID.String() {
		t.Fatalf("server_id = %q, want %q", ok.ServerID, h.serverID.String())
	}
}

func TestChainFramesChunk(t *testing.T) {
	key := mustID(t)
	c, err := newChain(key, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		head := c.Head()
		e, err := chain.NewEntry(key, &head, chain.OpGrant, chain.GrantBody{
			Fingerprint: identity.Fingerprint(mustID(t).PublicKey()), Roles: []string{"user"},
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	frames, err := chainFrames(c.Entries())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 2 {
		t.Fatalf("only %d frame(s); chain too small to test chunking", len(frames))
	}
	total := 0
	for i, raw := range frames {
		if len(raw) >= proto.MaxMessageSize {
			t.Fatalf("frame %d is %d bytes", i, len(raw))
		}
		env, err := proto.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		var f proto.ChainFrame
		_ = json.Unmarshal(env.Data, &f)
		if f.More != (i < len(frames)-1) {
			t.Fatalf("frame %d More = %v", i, f.More)
		}
		total += len(f.Entries)
	}
	if total != len(c.Entries()) {
		t.Fatalf("%d entries in frames, want %d", total, len(c.Entries()))
	}
}

func requireAuthFailed(t *testing.T, c *websocket.Conn, env proto.Envelope) {
	t.Helper()
	if env.Type != proto.TypeError {
		t.Fatalf("type = %q, want error", env.Type)
	}
	var em proto.ErrorMsg
	if err := json.Unmarshal(env.Data, &em); err != nil {
		t.Fatal(err)
	}
	if em.Code != "auth_failed" {
		t.Fatalf("code = %q, want auth_failed", em.Code)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection still open after auth failure")
	}
}

func mustID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAuthHappyPath(t *testing.T) {
	url := startServer(t, Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general", "dev"},
	})
	id := mustID(t)
	_, env := authAs(t, url, id, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("type = %q, want auth_ok", env.Type)
	}
	var ok proto.AuthOK
	if err := json.Unmarshal(env.Data, &ok); err != nil {
		t.Fatal(err)
	}
	if want := identity.Fingerprint(id.PublicKey()); ok.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", ok.Fingerprint, want)
	}
	if want := []string{"user"}; !reflect.DeepEqual(ok.Roles, want) {
		t.Errorf("roles = %v, want %v", ok.Roles, want)
	}
	if want := []proto.ChannelInfo{{Name: "general", Type: "text"}, {Name: "dev", Type: "text"}}; !reflect.DeepEqual(ok.Channels, want) {
		t.Errorf("channels = %v, want %v", ok.Channels, want)
	}
}

func TestAuthBadSignature(t *testing.T) {
	url := startServer(t, Config{})
	id := mustID(t)
	c := dial(t, url)
	_ = hello(t, c)
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "x", Sig: id.Sign([]byte("not the nonce")),
	})
	requireAuthFailed(t, c, readEnv(t, c))
}

func TestAuthWrongPassword(t *testing.T) {
	url := startServer(t, Config{Password: "s3cret"})
	c, env := authAs(t, url, mustID(t), "nope")
	requireAuthFailed(t, c, env)

	_, env = authAs(t, url, mustID(t), "s3cret")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("correct password: type = %q, want auth_ok", env.Type)
	}
}

func TestAuthAllowlist(t *testing.T) {
	a, b := mustID(t), mustID(t)
	url := startServer(t, Config{AllowedKeys: []string{identity.Fingerprint(a.PublicKey())}})

	c, env := authAs(t, url, b, "")
	requireAuthFailed(t, c, env)

	_, env = authAs(t, url, a, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("allowed key: type = %q, want auth_ok", env.Type)
	}
}

func TestAuthNonceReplayRejected(t *testing.T) {
	url := startServer(t, Config{})
	id := mustID(t)
	a := dial(t, url)
	ha := hello(t, a)
	b := dial(t, url)
	hb := hello(t, b)
	// A signature made for connection a's nonces, replayed on b.
	sendEnv(t, b, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "x",
		Sig: id.Sign(proto.ClientAuthBytes(ha.serverNonce, ha.clientNonce, hb.serverID[:])),
	})
	requireAuthFailed(t, b, readEnv(t, b))
}

func TestOversizedFrameCloses(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	_ = readChallenge(t, c)
	big := make([]byte, 70*1024)
	_ = c.WriteMessage(websocket.TextMessage, big)
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected connection to close after oversized frame")
	}

	_, env := authAs(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("server not alive after oversized frame: type = %q", env.Type)
	}
}
