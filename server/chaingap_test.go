package server

import (
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// An entry appended while a client is between host_proof and auth must still
// reach it: the server sends the missed entries right after auth_ok, so the
// next live frame links to the client's copy.
func TestChainAppendDuringHandshakeIsCaughtUp(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)

	b := mustID(t)
	ws, _, err := websocket.DefaultDialer.Dial(f.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_, raw, _ := ws.ReadMessage()
	env, _ := proto.DecodeHandshake(raw)
	var chl proto.Challenge
	_ = json.Unmarshal(env.Data, &chl)
	cn := make([]byte, 32)
	_, _ = rand.Read(cn)
	r, _ := proto.Encode(proto.TypeHostProofReq, proto.HostProofReq{Nonce: cn})
	_ = ws.WriteMessage(websocket.TextMessage, r)
	var es []chain.Entry
	for {
		_, raw, _ = ws.ReadMessage()
		env, _ = proto.Decode(raw)
		if env.Type != proto.TypeChain {
			break // host_proof
		}
		var cf proto.ChainFrame
		_ = json.Unmarshal(env.Data, &cf)
		for _, x := range cf.Entries {
			var e chain.Entry
			_ = json.Unmarshal(x, &e)
			es = append(es, e)
		}
	}
	local, err := chain.FromEntries(es)
	if err != nil {
		t.Fatal(err)
	}

	// The founder appends while b has not yet sent auth.
	other := mustID(t)
	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: identity.Fingerprint(other.PublicKey()), Roles: []string{"user"}}))
	readChainEntry(t, a)

	id := f.ch.ID()
	r, _ = proto.Encode(proto.TypeAuth, proto.Auth{PubKey: b.PublicKey(), Name: "b",
		Sig: b.Sign(proto.ClientAuthBytes(chl.Nonce, cn, id[:]))})
	_ = ws.WriteMessage(websocket.TextMessage, r)
	if env := readEnv(t, ws); env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: %q %s", env.Type, env.Data)
	}
	// The missed entry arrives before anything live. Bounded, so a server
	// that never sends it fails the test instead of hanging it.
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := local.Append(readChainEntry(t, ws)); err != nil {
		t.Fatalf("catch-up entry: %v", err)
	}

	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: identity.Fingerprint(b.PublicKey()), Roles: []string{"user"}}))
	e := readChainEntry(t, ws)
	if err := local.Append(e); err != nil {
		t.Fatalf("client copy head %d, live entry seq %d: %v", local.Head().Seq, e.Seq, err)
	}
}
