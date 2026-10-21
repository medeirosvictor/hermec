package client

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server"
)

// fakeHost is a small ws handler that speaks the host side of the version 2
// handshake on top of core/chain and core/proto, with knobs to misbehave.
type fakeHost struct {
	t        *testing.T
	url      string
	key      *identity.Identity // the host key on the chain
	ch       *chain.Chain
	proofKey *identity.Identity // signs host_proof; defaults to key
	v1       bool               // behave like a pre-v2 host
	perFrame int                // entries per chain frame; 0 = all in one
	tamper   func([]json.RawMessage) []json.RawMessage

	mu   sync.Mutex
	auth *proto.Auth // the auth message received, if any
}

// newChain builds a chain founded by key with key as host, plus extra grants.
func newChain(t *testing.T, key *identity.Identity, extra int) *chain.Chain {
	t.Helper()
	g, err := chain.NewEntry(key, nil, chain.OpGenesis, chain.GenesisBody{
		Name: "fake", Founder: []byte(key.PublicKey()), Salt: []byte("0123456789abcdef"),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ch, err := chain.New(g)
	if err != nil {
		t.Fatal(err)
	}
	add := func(op chain.Op, body any) {
		head := ch.Head()
		e, err := chain.NewEntry(key, &head, op, body, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ch.Append(e); err != nil {
			t.Fatalf("append %s (entry %d): %v", op, head.Seq+1, err)
		}
	}
	add(chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(key.PublicKey()), Priority: 1})
	for i := 0; i < extra; i++ {
		other, err := identity.Generate()
		if err != nil {
			t.Fatal(err)
		}
		add(chain.OpGrant, chain.GrantBody{Fingerprint: identity.Fingerprint(other.PublicKey()), Roles: []string{"user"}})
	}
	return ch
}

func newFakeHost(t *testing.T, mod func(*fakeHost)) *fakeHost {
	t.Helper()
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHost{t: t, key: key, ch: newChain(t, key, 0)}
	if mod != nil {
		mod(f)
	}
	f.listen(func(ws *websocket.Conn) {
		if a := f.handshake(ws); a != nil {
			raw, _ := proto.Encode(proto.TypeAuthOK, proto.AuthOK{
				Fingerprint: identity.Fingerprint(a.PubKey), ServerID: f.serverID(),
			})
			_ = ws.WriteMessage(websocket.TextMessage, raw)
			_, _, _ = ws.ReadMessage() // hold open until the client closes
		}
	})
	return f
}

// listen starts the ws endpoint and sets f.url.
func (f *fakeHost) listen(serve func(*websocket.Conn)) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		serve(ws)
	}))
	f.t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	f.url = "ws" + strings.TrimPrefix(srv.URL, "http")
}

func (f *fakeHost) serverID() string { return f.ch.ID().String() }

func (f *fakeHost) gotAuth() *proto.Auth {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth
}

// handshake plays the host side up to receiving auth. It returns that auth, or
// nil if the client did not get that far. It does not reply to the auth.
func (f *fakeHost) handshake(ws *websocket.Conn) *proto.Auth {
	send := func(v int, typ string, payload any) {
		raw, err := proto.EncodeV(v, typ, payload)
		if err != nil {
			f.t.Errorf("fake host encode: %v", err)
			return
		}
		_ = ws.WriteMessage(websocket.TextMessage, raw)
	}
	read := func() (proto.Envelope, bool) {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return proto.Envelope{}, false
		}
		env, err := proto.DecodeHandshake(raw)
		return env, err == nil
	}
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	send(proto.LegacyVersion, proto.TypeChallenge, proto.Challenge{Nonce: nonce})
	env, ok := read()
	if f.v1 {
		// A v1 host expects auth here and rejects anything else.
		send(proto.LegacyVersion, proto.TypeError, proto.ErrorMsg{Code: "auth_failed", Message: "authentication failed"})
		return nil
	}
	if !ok || env.Type != proto.TypeHostProofReq {
		return nil
	}
	var req proto.HostProofReq
	_ = json.Unmarshal(env.Data, &req)

	var raws []json.RawMessage
	for _, e := range f.ch.Entries() {
		raw, err := json.Marshal(e)
		if err != nil {
			f.t.Errorf("marshal: %v", err)
			return nil
		}
		raws = append(raws, raw)
	}
	if f.tamper != nil {
		raws = f.tamper(raws)
	}
	n := f.perFrame
	if n <= 0 {
		n = len(raws)
	}
	for i := 0; i < len(raws); i += n {
		end := min(i+n, len(raws))
		send(proto.Version, proto.TypeChain, proto.ChainFrame{Entries: raws[i:end], More: end < len(raws)})
	}
	pk := f.proofKey
	if pk == nil {
		pk = f.key
	}
	id := f.ch.ID()
	send(proto.Version, proto.TypeHostProof, proto.HostProof{
		PubKey: pk.PublicKey(),
		Sig:    pk.Sign(proto.HostProofBytes(req.Nonce, nonce, id[:])),
	})

	env, ok = read()
	if !ok || env.Type != proto.TypeAuth {
		return nil
	}
	var a proto.Auth
	if json.Unmarshal(env.Data, &a) != nil {
		return nil
	}
	// A real host checks the signature; so does this one.
	if !identity.Verify(a.PubKey, proto.ClientAuthBytes(nonce, req.Nonce, id[:]), a.Sig) {
		f.t.Errorf("client auth signature is not over the client-auth domain bytes")
	}
	f.mu.Lock()
	f.auth = &a
	f.mu.Unlock()
	return &a
}

// stubHandshake is the shared first half of the hand-rolled stub servers in
// other tests: a valid v2 handshake from a throwaway host. It returns the
// client's auth and the server ID to put in auth_ok.
func stubHandshake(t *testing.T, ws *websocket.Conn) (*proto.Auth, string) {
	t.Helper()
	key, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHost{t: t, key: key, ch: newChain(t, key, 0)}
	return f.handshake(ws), f.serverID()
}

func dialErr(t *testing.T, url, pin string) *ServerError {
	t.Helper()
	id, _ := identity.Generate()
	c, err := DialPinned(testCtx(t), url, id, "alice", "s3cret", pin)
	if err == nil {
		c.Close()
		t.Fatal("DialPinned succeeded, want a server error")
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *ServerError", err)
	}
	return se
}

func TestDialPinsAndReportsServerID(t *testing.T) {
	key, _ := identity.Generate()
	cfg := server.DefaultConfig()
	ch, _, err := server.OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	url := startServer(t, server.Config{HostKey: key, Chain: ch})
	id, _ := identity.Generate()

	// Trust on first use.
	c, err := Dial(testCtx(t), url, id, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got, want := c.ServerID(), ch.ID().String(); got != want {
		t.Fatalf("ServerID = %q, want %q", got, want)
	}
	if got := c.Chain().ID(); got != ch.ID() {
		t.Fatalf("Chain().ID() = %v, want %v", got, ch.ID())
	}

	// And with the right pin.
	c2, err := DialPinned(testCtx(t), url, id, "alice", "", c.ServerID())
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	c2.Close()
}

func TestDialPinMismatch(t *testing.T) {
	f := newFakeHost(t, nil)
	otherKey, _ := identity.Generate()
	pin := newChain(t, otherKey, 0).ID().String()
	se := dialErr(t, f.url, pin)
	if se.Code != proto.CodeServerIDMismatch {
		t.Fatalf("code = %q, want server_id_mismatch", se.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if a := f.gotAuth(); a != nil {
		t.Fatalf("the client sent auth (password %q) to a host with the wrong server ID", a.Password)
	}
}

func TestDialHostNotAuthorized(t *testing.T) {
	f := newFakeHost(t, func(f *fakeHost) {
		rogue, _ := identity.Generate()
		f.proofKey = rogue // validly signs, but is not on the host list
	})
	se := dialErr(t, f.url, "")
	if se.Code != proto.CodeHostNotAuthorized {
		t.Fatalf("code = %q, want host_not_authorized", se.Code)
	}
	time.Sleep(100 * time.Millisecond)
	if f.gotAuth() != nil {
		t.Fatal("the client sent auth to an unauthorized host")
	}
}

func TestDialRejectsTamperedChain(t *testing.T) {
	f := newFakeHost(t, func(f *fakeHost) {
		f.ch = newChain(t, f.key, 3)
		f.tamper = func(raws []json.RawMessage) []json.RawMessage {
			// Edit an entry's signed content without re-signing it.
			var e chain.Entry
			if err := json.Unmarshal(raws[2], &e); err != nil {
				t.Fatal(err)
			}
			e.Time++
			raws[2], _ = json.Marshal(e)
			return raws
		}
	})
	se := dialErr(t, f.url, "")
	if se.Code != proto.CodeBadChain {
		t.Fatalf("code = %q (%s), want bad_chain", se.Code, se.Message)
	}
	time.Sleep(100 * time.Millisecond)
	if f.gotAuth() != nil {
		t.Fatal("the client sent auth to a host with a broken chain")
	}
}

func TestDialV1HostServerOutdated(t *testing.T) {
	f := newFakeHost(t, func(f *fakeHost) { f.v1 = true })
	se := dialErr(t, f.url, "")
	if se.Code != proto.CodeServerOutdated {
		t.Fatalf("code = %q, want server_outdated", se.Code)
	}
}

func TestDialLargeChainChunked(t *testing.T) {
	f := newFakeHost(t, func(f *fakeHost) {
		f.ch = newChain(t, f.key, 1500)
		f.perFrame = 150
	})
	id, _ := identity.Generate()
	c, err := Dial(testCtx(t), f.url, id, "alice", "")
	if err != nil {
		t.Fatalf("a 1500-entry chain in 11 frames did not verify: %v", err)
	}
	defer c.Close()
	if n := len(c.Chain().Entries()); n != 1502 {
		t.Fatalf("client chain has %d entries, want 1502", n)
	}
}
