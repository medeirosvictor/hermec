package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

// fastHostTimers shortens the orchestrator and heartbeat timers for a test.
func fastHostTimers(t *testing.T) {
	t.Helper()
	ms := time.Millisecond
	t.Cleanup(server.SetHeartbeatForTest(50*ms, 150*ms))
	t.Cleanup(SetTimersForTest(300*ms, 300*ms, 300*ms, 50*ms))
}

// threeHostChain is a chain with three hosts at priorities 1, 2 and 3, and
// their keys (the first is the founder's).
func threeHostChain(t *testing.T) (c *chain.Chain, a, b, d *identity.Identity) {
	t.Helper()
	b, d = mustID(t), mustID(t)
	c, a = hostChain(t, b, d)
	return c, a, b, d
}

// freeAddr returns a loopback address that was free a moment ago, so a host
// can advertise it before it starts.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// testHost is a Host under test with a settable known-host list.
type testHost struct {
	*Host
	url string
	key *identity.Identity

	mu    sync.Mutex
	known []string
	notes []string
}

func (h *testHost) setKnown(urls ...string) {
	h.mu.Lock()
	h.known = urls
	h.mu.Unlock()
}

func (h *testHost) notified() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.notes...)
}

// newTestHost builds (without starting) a host on its own copy of base.
func newTestHost(t *testing.T, base *chain.Chain, key *identity.Identity, name string, known ...string) *testHost {
	t.Helper()
	cc, err := chain.FromEntries(base.Entries())
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	th := &testHost{url: "ws://" + addr, key: key, known: known}
	th.Host = New(Config{
		Server: server.Config{
			Addr: addr, Advertise: []string{th.url}, HostKey: key, Chain: cc,
			HostName: name, Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
			Channels:      []string{"general"},
			VoiceChannels: []string{"voice"},
		},
		Known: func() []string {
			th.mu.Lock()
			defer th.mu.Unlock()
			return append([]string(nil), th.known...)
		},
		Notify: func(s string) {
			th.mu.Lock()
			th.notes = append(th.notes, s)
			th.mu.Unlock()
		},
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = th.Shutdown(ctx)
	})
	return th
}

func (h *testHost) start(t *testing.T) {
	t.Helper()
	if err := h.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func waitRole(t *testing.T, h *testHost, role string, within time.Duration) {
	t.Helper()
	end := time.Now().Add(within)
	for time.Now().Before(end) {
		if h.Role() == role {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("role = %q after %v, want %q", h.Role(), within, role)
}

// readErrCode reads frames until an error frame with code arrives.
func readErrOfCode(t *testing.T, c *websocket.Conn, code string) {
	t.Helper()
	for {
		env := readType(t, c, proto.TypeError)
		var em proto.ErrorMsg
		if json.Unmarshal(env.Data, &em) == nil && em.Code == code {
			return
		}
	}
}

// clientOn dials url, authenticates as id and joins general.
func clientOn(t *testing.T, url string, id *identity.Identity) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := hshake.Run(ctx, c, id, "tester", "pw", hshake.Opts{}); err != nil {
		t.Fatalf("auth on %s: %v", url, err)
	}
	sendEnv(t, c, proto.TypeJoin, proto.Join{Channel: "general"})
	return c
}

func mustID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// hostChain is a chain founded by a new key, with others added as hosts at
// priorities 2, 3, ...
func hostChain(t *testing.T, others ...*identity.Identity) (*chain.Chain, *identity.Identity) {
	t.Helper()
	a := mustID(t)
	c := server.New(server.Config{
		HostKey:  a,
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	}).Chain()
	for i, o := range others {
		head := c.Head()
		e, err := chain.NewEntry(a, &head, chain.OpHostAdd,
			chain.HostAddBody{PubKey: []byte(o.PublicKey()), Priority: i + 2}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	return c, a
}

func twoHostChain(t *testing.T) (c *chain.Chain, a, b *identity.Identity) {
	t.Helper()
	b = mustID(t)
	c, a = hostChain(t, b)
	return c, a, b
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

// readType reads frames until one of type typ arrives and returns it.
func readType(t *testing.T, c *websocket.Conn, typ string) proto.Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		env, err := proto.Decode(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Type == typ {
			return env
		}
	}
}

// startServer starts a plain server on a loopback port.
func startServer(t *testing.T, cfg server.Config) (*server.Server, string) {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := server.New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, "ws://" + s.Addr()
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func histAll(s *server.Server) []proto.ChatMessage {
	msgs, _ := s.HistoryForTest().After(time.Time{}, "", 100)
	return msgs
}

// seedHistory adds n lines to s's history, oldest first.
func seedHistory(t *testing.T, s *server.Server, n int) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < n; i++ {
		m := proto.ChatMessage{
			ID: fmt.Sprintf("%032x", i+1), Channel: "general",
			Text: fmt.Sprintf("m%d", i), TS: base.Add(time.Duration(i) * time.Second),
		}
		if err := s.HistoryForTest().Add(m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestColdStartAlone(t *testing.T) {
	fastHostTimers(t)
	base, a, _, _ := threeHostChain(t)
	h := newTestHost(t, base, a, "alpha")
	h.start(t)
	waitRole(t, h, RoleActive, 500*time.Millisecond)
	if h.Server().Mode() != server.ModeActive {
		t.Fatal("server is not in active mode")
	}
	if got := h.notified(); len(got) == 0 || got[len(got)-1] != "hosting: active" {
		t.Fatalf("notifications = %v", got)
	}
}

func TestColdStartJoinsActive(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	ha.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	hb := newTestHost(t, base, b, "beta", ha.url)
	hb.start(t)
	waitRole(t, hb, RoleStandby, 2*time.Second)
	waitFor(t, "B in A's links", func() bool {
		for _, p := range ha.Server().Links() {
			if p.Fingerprint == identity.Fingerprint(b.PublicKey()) {
				return true
			}
		}
		return false
	})
	if got := hb.Active(); got.Name != "alpha" {
		t.Fatalf("B's active host = %+v", got)
	}
	if hb.Server().Mode() != server.ModeStandby {
		t.Fatal("B's server is not standby")
	}
}

func TestStandbyBecomesActiveWithoutClients(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	ha.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	hb := newTestHost(t, base, b, "beta", ha.url)
	hb.start(t)
	waitRole(t, hb, RoleStandby, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ha.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitRole(t, hb, RoleActive, heartbeatGrace()+coldStartWindow+200*time.Millisecond)
	if hb.Server().Mode() != server.ModeActive {
		t.Fatal("B's server is not active")
	}
}

func TestSplitBrainMerge(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	hb := newTestHost(t, base, b, "beta")
	ha.start(t)
	hb.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	waitRole(t, hb, RoleActive, time.Second)

	// A has two clients and one chat line; B has the founder, one chat line and
	// one chain change only it has.
	a1, a2 := clientOn(t, ha.url, mustID(t)), clientOn(t, ha.url, mustID(t))
	_ = a2
	sendEnv(t, a1, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "line on A"})
	fc := clientOn(t, hb.url, a)
	sendEnv(t, fc, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "line on B"})
	head := hb.Server().Chain().Head()
	e, err := chain.NewEntry(a, &head, chain.OpGrant,
		chain.GrantBody{Fingerprint: identity.Fingerprint(mustID(t).PublicKey()), Roles: []string{"admin"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sendAppend(t, fc, e)
	waitFor(t, "grant on B", func() bool { return hb.Server().Chain().Head().Seq == e.Seq })
	// A makes a different change at the same position: the chains fork, so B's
	// grant is lost when B yields (a mere prefix would be kept; see
	// TestYieldKeepsNewerEntriesOnWinner).
	headA := ha.Server().Chain().Head()
	ea, err := chain.NewEntry(a, &headA, chain.OpGrant,
		chain.GrantBody{Fingerprint: identity.Fingerprint(mustID(t).PublicKey()), Roles: []string{"admin"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ha.Server().Chain().Append(ea); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "lines", func() bool { return len(histAll(ha.Server())) == 1 && len(histAll(hb.Server())) == 1 })

	ha.setKnown(hb.url)
	hb.setKnown(ha.url)
	// The loser yields, then may already have relinked as the winner's standby.
	waitFor(t, "loser not active", func() bool { return hb.Role() != RoleActive })
	if ha.Role() != RoleActive {
		t.Fatalf("A is %q, want active", ha.Role())
	}
	readErrOfCode(t, fc, proto.CodeChainLost)
	mv := readType(t, fc, proto.TypeMove)
	var m proto.Move
	if err := json.Unmarshal(mv.Data, &m); err != nil || m.Reason != "merge" || m.To.Name != "alpha" {
		t.Fatalf("move = %+v (%v)", m, err)
	}
	waitFor(t, "both lines on A", func() bool {
		var got []string
		for _, msg := range histAll(ha.Server()) {
			got = append(got, msg.Text)
		}
		return len(got) == 2
	})
	waitFor(t, "B's chain equals A's", func() bool {
		return hb.Server().Chain().Head().Hash() == ha.Server().Chain().Head().Hash()
	})
	waitRole(t, hb, RoleStandby, 2*time.Second)
	waitFor(t, "B's history", func() bool { return len(histAll(hb.Server())) == 2 })
	_ = fc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := fc.ReadMessage(); err != nil {
			break
		}
	}
}

// handbackFixture starts B (priority 2) active with one client, then A
// (priority 1), which links to it.
func handbackFixture(t *testing.T) (ha, hb *testHost, client *websocket.Conn) {
	t.Helper()
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	hb = newTestHost(t, base, b, "beta")
	hb.start(t)
	waitRole(t, hb, RoleActive, time.Second)
	client = clientOn(t, hb.url, mustID(t))
	ha = newTestHost(t, base, a, "alpha", hb.url)
	ha.start(t)
	return ha, hb, client
}

func TestHandbackWhenVoiceEmpty(t *testing.T) {
	ha, hb, client := handbackFixture(t)
	waitRole(t, ha, RoleActive, 3*time.Second)
	mv := readType(t, client, proto.TypeMove)
	var m proto.Move
	if err := json.Unmarshal(mv.Data, &m); err != nil || m.Reason != "handback" || m.To.Name != "alpha" {
		t.Fatalf("move = %+v (%v)", m, err)
	}
	waitFor(t, "B to leave active", func() bool { return hb.Role() != RoleActive })
	waitFor(t, "B linked", func() bool { return hb.Role() == RoleStandby || hb.Role() == RoleIdle })
	if hb.Server().Mode() == server.ModeActive {
		t.Fatal("B's server is still active")
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := client.ReadMessage(); err != nil {
			break
		}
	}
	waitFor(t, "A lists B", func() bool { return len(ha.Server().Links()) == 1 })
}

func TestNoHandbackDuringVoice(t *testing.T) {
	ha, hb, _ := handbackFixture(t)
	hb.Server().AddVoiceMemberForTest("voice")
	time.Sleep(3 * handbackIdle)
	if hb.Role() != RoleActive {
		t.Fatalf("B is %q during voice, want active", hb.Role())
	}
	if ha.Role() == RoleActive {
		t.Fatal("A became active during voice")
	}
}

func heartbeatGrace() time.Duration {
	_, grace := server.HeartbeatTiming()
	return grace
}

func fixedLoad(clients int) func() (int, int) {
	return func() (int, int) { return 0, clients }
}

// historyPage is the server's history page size, the unit of a merge's pacing.
const historyPage = 100

func sendAppend(t *testing.T, c *websocket.Conn, e chain.Entry) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	sendEnv(t, c, proto.TypeChainAppend, proto.ChainAppend{Entry: raw})
}
