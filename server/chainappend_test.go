package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// appendFixture is a running server founded by founder, with the chain the
// server holds so tests can build entries on its head.
type appendFixture struct {
	url     string
	founder *identity.Identity
	ch      *chain.Chain
}

func newAppendFixture(t *testing.T) *appendFixture {
	t.Helper()
	founder := mustID(t)
	cfg := Config{
		HostKey:       founder,
		Roles:         roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:      []string{"general"},
		VoiceChannels: []string{"lounge"},
	}
	ch, err := newChain(founder, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Chain = ch
	return &appendFixture{url: startServer(t, cfg), founder: founder, ch: ch}
}

func (f *appendFixture) connect(t *testing.T, id *identity.Identity) *websocket.Conn {
	t.Helper()
	c, env := authAs(t, f.url, id, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: type = %q", env.Type)
	}
	return c
}

func (f *appendFixture) entry(t *testing.T, by *identity.Identity, op chain.Op, body any) chain.Entry {
	t.Helper()
	head := f.ch.Head()
	e, err := chain.NewEntry(by, &head, op, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sendAppend(t *testing.T, c *websocket.Conn, e chain.Entry) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	sendEnv(t, c, proto.TypeChainAppend, proto.ChainAppend{Entry: raw})
}

// readChainEntry returns the first entry of the next chain frame, skipping
// roster and voice_state frames.
func readChainEntry(t *testing.T, c *websocket.Conn) chain.Entry {
	t.Helper()
	for {
		env := readEnv(t, c)
		switch env.Type {
		case proto.TypeRoster, proto.TypeVoiceState:
			continue
		case proto.TypeChain:
			var f proto.ChainFrame
			if err := json.Unmarshal(env.Data, &f); err != nil || len(f.Entries) != 1 {
				t.Fatalf("chain frame = %s (%v)", env.Data, err)
			}
			var e chain.Entry
			if err := json.Unmarshal(f.Entries[0], &e); err != nil {
				t.Fatal(err)
			}
			return e
		default:
			t.Fatalf("type = %q (%s), want chain", env.Type, env.Data)
		}
	}
}

func readErrCode(t *testing.T, c *websocket.Conn) proto.ErrorMsg {
	t.Helper()
	env := readEnvSkipRoster(t, c)
	if env.Type != proto.TypeError {
		t.Fatalf("type = %q (%s), want error", env.Type, env.Data)
	}
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	return em
}

func TestChainAppendBroadcasts(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)
	b := f.connect(t, mustID(t))
	x := identity.Fingerprint(mustID(t).PublicKey())
	e := f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: x, Roles: []string{"bot"}})
	sendAppend(t, a, e)
	for _, c := range []*websocket.Conn{a, b} {
		got := readChainEntry(t, c)
		if got.Seq != e.Seq || got.Op != chain.OpGrant {
			t.Fatalf("got entry %+v, want seq %d grant", got, e.Seq)
		}
	}
	if got := f.ch.State().Grants[x]; len(got) != 1 || got[0] != "bot" {
		t.Errorf("grants[x] = %v", got)
	}
}

func TestChainAppendAppliesRolesImmediately(t *testing.T) {
	f := newAppendFixture(t)
	user := mustID(t)
	a := f.connect(t, f.founder)
	u := f.connect(t, user)
	fp := identity.Fingerprint(user.PublicKey())
	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: fp, Roles: nil}))
	readChainEntry(t, u)
	// No roles left: joining is now forbidden.
	sendEnv(t, u, proto.TypeJoin, proto.Join{Channel: "general"})
	if em := readErrCode(t, u); em.Code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", em.Code)
	}
}

func TestHistoryBeforeAfterJoinRevoked(t *testing.T) {
	f := newAppendFixture(t)
	user := mustID(t)
	a := f.connect(t, f.founder)
	u := f.connect(t, user)
	fp := identity.Fingerprint(user.PublicKey())
	sendEnv(t, u, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, u)
	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: fp, Roles: nil}))
	readChainEntry(t, u)
	// Still in the channel, but no longer allowed to read its history.
	sendEnv(t, u, proto.TypeHistoryBefore, proto.HistoryBefore{Channel: "general", BeforeID: "x"})
	if em := readErrCode(t, u); em.Code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", em.Code)
	}
}

func TestChainAppendForbiddenForUser(t *testing.T) {
	f := newAppendFixture(t)
	user := mustID(t)
	u := f.connect(t, user)
	e := f.entry(t, user, chain.OpGrant, chain.GrantBody{Fingerprint: "x", Roles: []string{"admin"}})
	sendAppend(t, u, e)
	if em := readErrCode(t, u); em.Code != "forbidden" || em.Message == "" {
		t.Fatalf("error = %+v, want forbidden with reason", em)
	}
	if f.ch.Head().Seq >= e.Seq {
		t.Error("entry was appended")
	}
}

func TestChainAppendAuthorMismatch(t *testing.T) {
	f := newAppendFixture(t)
	u := f.connect(t, mustID(t))
	// A valid founder-signed entry sent over someone else's connection.
	e := f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: "x", Roles: []string{"admin"}})
	sendAppend(t, u, e)
	if em := readErrCode(t, u); em.Code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", em.Code)
	}
	if f.ch.Head().Seq >= e.Seq {
		t.Error("entry was appended")
	}
}

func TestChainAppendMalformedAndConflict(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)
	sendEnv(t, a, proto.TypeChainAppend, proto.ChainAppend{Entry: json.RawMessage(`"junk"`)})
	if em := readErrCode(t, a); em.Code != "bad_request" {
		t.Fatalf("code = %q, want bad_request", em.Code)
	}
	stale := f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: "p", Roles: []string{"bot"}})
	win := f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: "q", Roles: []string{"bot"}})
	sendAppend(t, a, win)
	readChainEntry(t, a)
	sendAppend(t, a, stale)
	if em := readErrCode(t, a); em.Code != proto.CodeChainConflict {
		t.Fatalf("code = %q, want chain_conflict", em.Code)
	}
}

func TestBanDisconnectsLiveMember(t *testing.T) {
	f := newAppendFixture(t)
	bad := mustID(t)
	a := f.connect(t, f.founder)
	b := f.connect(t, bad)
	sendEnv(t, b, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	for {
		env := readEnv(t, a)
		if env.Type == proto.TypeVoiceState {
			break
		}
	}
	bfp := identity.Fingerprint(bad.PublicKey())
	sendAppend(t, a, f.entry(t, f.founder, chain.OpBan, chain.FPBody{Fingerprint: bfp}))

	sawBanned := false
	for {
		_, raw, err := b.ReadMessage()
		if err != nil {
			break
		}
		env, _ := proto.Decode(raw)
		if env.Type == proto.TypeError {
			var em proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &em)
			sawBanned = sawBanned || em.Code == proto.CodeBanned
		}
	}
	if !sawBanned {
		t.Error("banned member did not receive a banned error")
	}
	// The founder's view: the ban entry arrives and the lounge empties.
	gotBan, gotEmpty := false, false
	for !(gotBan && gotEmpty) {
		env := readEnv(t, a)
		switch env.Type {
		case proto.TypeChain:
			gotBan = true
		case proto.TypeVoiceState:
			var v proto.VoiceState
			_ = json.Unmarshal(env.Data, &v)
			if v.Channel == "lounge" && len(v.Members) == 0 {
				gotEmpty = true
			}
		}
	}
	_, env := authAs(t, f.url, bad, "")
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	if env.Type != proto.TypeError || em.Code != proto.CodeBanned {
		t.Fatalf("redial = %q %q, want error banned", env.Type, em.Code)
	}
}

func TestAllowedKeysChangeAppliesLive(t *testing.T) {
	founder, friend, stranger := mustID(t), mustID(t), mustID(t)
	cfg := Config{HostKey: founder, Channels: []string{"general"}, Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}}}
	ch, err := newChain(founder, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Chain = ch
	f := &appendFixture{url: startServer(t, cfg), founder: founder, ch: ch}
	a := f.connect(t, founder)
	if _, env := authAs(t, f.url, stranger, ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("open server: type = %q", env.Type)
	}
	body := ch.State().Config
	body.AllowedKeys = []string{identity.Fingerprint(friend.PublicKey())}
	sendAppend(t, a, f.entry(t, founder, chain.OpConfig, body))
	readChainEntry(t, a)
	if _, env := authAs(t, f.url, stranger, ""); env.Type != proto.TypeError {
		t.Fatalf("stranger after allowlist: type = %q, want error", env.Type)
	}
	if _, env := authAs(t, f.url, friend, ""); env.Type != proto.TypeAuthOK {
		t.Fatalf("friend: type = %q", env.Type)
	}
}
