package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func fixedLoad(clients int) func() (int, int) {
	return func() (int, int) { return 0, clients }
}

// hostChangedLines counts the "host changed" system lines in s's history.
func hostChangedLines(s *Server) int {
	n := 0
	for _, m := range histAll(s) {
		if m.System && strings.HasPrefix(m.Text, "host changed to") {
			n++
		}
	}
	return n
}

func TestColdStartSimultaneousStartsOneActive(t *testing.T) {
	fastHostTimers(t)
	base, a, b, d := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	hb := newTestHost(t, base, b, "beta")
	hc := newTestHost(t, base, d, "gamma")
	ha.setKnown(hb.url, hc.url)
	hb.setKnown(ha.url, hc.url)
	hc.setKnown(ha.url, hb.url)
	// Every server listens before any host starts looking for the others.
	var ready sync.WaitGroup
	ready.Add(3)
	gate := make(chan struct{})
	for _, h := range []*testHost{ha, hb, hc} {
		h.testHooks.beforeLoop = func() { ready.Done(); <-gate }
	}
	ha.start(t)
	hb.start(t)
	hc.start(t)
	ready.Wait()
	close(gate)
	waitRole(t, ha, RoleActive, 2*time.Second)
	waitRole(t, hb, RoleStandby, 3*time.Second)
	time.Sleep(2 * coldStartWindow)
	for _, h := range []*testHost{hb, hc} {
		if h.Role() == RoleActive {
			t.Fatalf("%s is active too", h.hc.Server.HostName)
		}
		for _, n := range h.notified() {
			if n == "hosting: active" {
				t.Fatalf("%s was active at some point: %v", h.hc.Server.HostName, h.notified())
			}
		}
	}
}

func TestIdleHostFollowsPromotedStandby(t *testing.T) {
	fastHostTimers(t)
	base, a, b, d := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	ha.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	hb := newTestHost(t, base, b, "beta", ha.url)
	hb.start(t)
	waitRole(t, hb, RoleStandby, 2*time.Second)
	hc := newTestHost(t, base, d, "gamma", ha.url)
	hc.start(t)
	waitRole(t, hc, RoleIdle, 2*time.Second)
	waitFor(t, "C linked to A", func() bool { return len(ha.Server().Links()) == 2 })
	waitFor(t, "C saw the standby", func() bool {
		hc.Host.mu.Lock()
		l := hc.Host.link
		hc.Host.mu.Unlock()
		if l == nil {
			return false
		}
		hs, ok := l.Hosts()
		return ok && hs.Standby != nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ha.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitRole(t, hb, RoleActive, heartbeatGrace+coldStartWindow+300*time.Millisecond)
	waitFor(t, "C linked to B", func() bool {
		for _, p := range hb.Server().Links() {
			if p.Fingerprint == identity.Fingerprint(d.PublicKey()) {
				return true
			}
		}
		return false
	})
	time.Sleep(2 * coldStartWindow)
	if hc.Role() == RoleActive {
		t.Fatal("C became active")
	}
	for _, n := range hc.notified() {
		if n == "hosting: active" {
			t.Fatalf("C was active at some point: %v", hc.notified())
		}
	}
	if n := hostChangedLines(hb.Server()); n != 1 {
		t.Fatalf("%d host-changed lines on B, want 1", n)
	}
}

// plainFollower starts a Server (not a Host) with the chain's host a as a
// standby that ignores handovers, linked to url.
func plainFollower(t *testing.T, base *chain.Chain, key *identity.Identity, url string) {
	t.Helper()
	cc, err := chain.FromEntries(base.Entries())
	if err != nil {
		t.Fatal(err)
	}
	addr := freeAddr(t)
	s, _ := startModeServer(t, Config{
		Addr: addr, HostKey: key, Chain: cc, Mode: ModeStandby, HostName: "alpha",
		Advertise: []string{"ws://" + addr},
		Channels:  []string{"general"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := DialLink(ctx, url, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	go l.Run()
}

func TestHandbackStopsWritesWhileWaitingAndResumes(t *testing.T) {
	fastHostTimers(t)
	oldWait := handoverWait
	handoverWait = 600 * time.Millisecond
	t.Cleanup(func() { handoverWait = oldWait })
	base, a, b, _ := threeHostChain(t)
	hb := newTestHost(t, base, b, "beta")
	hb.start(t)
	waitRole(t, hb, RoleActive, time.Second)
	client := clientOn(t, hb.url, mustID(t))
	plainFollower(t, base, a, hb.url) // takes the handover and never acts on it

	var sawNotActive, sawEchoAfter atomic.Bool
	go func() {
		for {
			_, raw, err := client.ReadMessage()
			if err != nil {
				return
			}
			env, err := proto.Decode(raw)
			if err != nil {
				continue
			}
			switch env.Type {
			case proto.TypeError:
				var em proto.ErrorMsg
				if json.Unmarshal(env.Data, &em) == nil && em.Code == proto.CodeNotActive {
					sawNotActive.Store(true)
				}
			case proto.TypeChatMessage:
				if sawNotActive.Load() {
					sawEchoAfter.Store(true)
				}
			}
		}
	}()
	end := time.Now().Add(6 * time.Second)
	for time.Now().Before(end) && !(sawNotActive.Load() && sawEchoAfter.Load()) {
		raw, _ := proto.Encode(proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "x"})
		_ = client.WriteMessage(websocket.TextMessage, raw)
		time.Sleep(15 * time.Millisecond)
	}
	if !sawNotActive.Load() {
		t.Fatal("the old active host kept accepting writes while it waited for the handover")
	}
	if !sawEchoAfter.Load() {
		t.Fatalf("the host did not resume serving after the handover went unanswered (mode %v role %s %v)", hb.Server().Mode(), hb.Role(), hb.notified())
	}
}

func TestHandbackKeepsAcceptedChat(t *testing.T) {
	ha, hb, client := handbackFixture(t)
	var mu sync.Mutex
	accepted := map[string]bool{}
	moved := make(chan struct{})
	go func() {
		defer close(moved)
		for {
			_, raw, err := client.ReadMessage()
			if err != nil {
				return
			}
			env, err := proto.Decode(raw)
			if err != nil {
				continue
			}
			if env.Type == proto.TypeChatMessage {
				var m proto.ChatMessage
				if json.Unmarshal(env.Data, &m) == nil && !m.System {
					mu.Lock()
					accepted[m.ID] = true
					mu.Unlock()
				}
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		select {
		case <-moved:
			i = 1000
		default:
			raw, _ := proto.Encode(proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "m"})
			if client.WriteMessage(websocket.TextMessage, raw) != nil {
				i = 1000
			}
			time.Sleep(4 * time.Millisecond)
		}
	}
	waitRole(t, ha, RoleActive, 3*time.Second)
	<-moved
	waitFor(t, "accepted lines on A", func() bool {
		have := map[string]bool{}
		for _, m := range histAll(ha.Server()) {
			have[m.ID] = true
		}
		mu.Lock()
		defer mu.Unlock()
		for id := range accepted {
			if !have[id] {
				return false
			}
		}
		return len(accepted) > 0
	})
	_ = hb
}

func TestMutualYieldNeverLeavesNoneActive(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	hb := newTestHost(t, base, b, "beta")
	var bClients atomic.Int32
	bClients.Store(1)
	ha.Server().load = func() (int, int) { return 1, 2 } // voice keeps B from handing back
	hb.Server().load = func() (int, int) { return 1, int(bClients.Load()) }
	// B loses when it first looks; before it commits its load flips.
	hb.testHooks.beforeYieldRecheck = func() { bClients.Store(5) }
	ha.start(t)
	hb.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	waitRole(t, hb, RoleActive, time.Second)
	ha.setKnown(hb.url)
	hb.setKnown(ha.url)

	end := time.Now().Add(2 * time.Second)
	for time.Now().Before(end) {
		if ha.Server().Mode() != ModeActive && hb.Server().Mode() != ModeActive {
			t.Fatalf("neither host was active: A=%v %v B=%v %v", ha.Server().Mode(), ha.notified(), hb.Server().Mode(), hb.notified())
		}
		time.Sleep(3 * time.Millisecond)
	}
	if hb.Server().Mode() != ModeActive || ha.Server().Mode() == ModeActive {
		t.Fatalf("modes: A=%v %v B=%v %v, want B alone active", ha.Server().Mode(), ha.notified(), hb.Server().Mode(), hb.notified())
	}
}

func TestMergeRefusedWhenWinnerWouldLose(t *testing.T) {
	a, b, aURL, _, _, _, _ := linkPair(t, "pw")
	a.load = fixedLoad(1)
	b.load = fixedLoad(3) // the loser claims more clients than the winner has
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := dialMerge(ctx, aURL, b)
	if err == nil {
		ws.Close()
		t.Fatal("the winner accepted a merge it would itself lose")
	}
}

func TestYieldNeedsWinnerOnOurChain(t *testing.T) {
	fastHostTimers(t)
	base, _, b, d := threeHostChain(t)
	es := base.Entries()
	short, err := chain.FromEntries(es[:len(es)-1]) // lacks D's host_add
	if err != nil {
		t.Fatal(err)
	}
	hd := newTestHost(t, base, d, "gamma")
	hb := newTestHost(t, short, b, "beta")
	hd.Server().load = fixedLoad(3)
	hb.Server().load = fixedLoad(0)
	hd.start(t)
	hb.start(t)
	waitRole(t, hd, RoleActive, time.Second)
	waitRole(t, hb, RoleActive, time.Second)
	hb.setKnown(hd.url)
	time.Sleep(2 * probeEvery) // at least one probe tick after setKnown
	if hb.Server().Mode() != ModeActive {
		t.Fatal("B yielded to a host that is not on its chain")
	}
}

func TestYieldKeepsNewerEntriesOnWinner(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	hb := newTestHost(t, base, b, "beta")
	ha.Server().load = fixedLoad(2)
	hb.Server().load = fixedLoad(1)
	ha.start(t)
	hb.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	waitRole(t, hb, RoleActive, time.Second)
	head := hb.Server().chain.Head()
	e, err := chain.NewEntry(a, &head, chain.OpBan, chain.FPBody{Fingerprint: identity.Fingerprint(mustID(t).PublicKey())}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := hb.Server().chain.Append(e); err != nil {
		t.Fatal(err)
	}
	want := hb.Server().chain.Head()
	ha.setKnown(hb.url)
	hb.setKnown(ha.url)
	waitRole(t, hb, RoleStandby, 3*time.Second)
	waitFor(t, "A has B's newer entry", func() bool { return ha.Server().chain.Head().Hash() == want.Hash() })
	if hb.Server().chain.Head().Hash() != want.Hash() {
		t.Fatal("B's chain was rolled back")
	}
}

func TestYieldAbortsWhenHistoryFails(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	hb := newTestHost(t, base, b, "beta")
	ha.Server().load = fixedLoad(2)
	hb.Server().load = fixedLoad(1)
	hb.testHooks.beforeMergeHistory = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ha.Shutdown(ctx) // the merge link dies under B
	}
	ha.start(t)
	hb.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	waitRole(t, hb, RoleActive, time.Second)
	client := clientOn(t, hb.url, mustID(t))
	ha.setKnown(hb.url)
	hb.setKnown(ha.url)
	time.Sleep(2 * probeEvery) // at least one probe tick after setKnown
	if hb.Server().Mode() != ModeActive {
		t.Fatal("B did not stay active after the merge failed")
	}
	// The client was neither moved nor disconnected.
	sendEnv(t, client, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "still here"})
	for {
		env := readType(t, client, proto.TypeChatMessage)
		var m proto.ChatMessage
		if json.Unmarshal(env.Data, &m) == nil && m.Text == "still here" {
			break
		}
	}
}

func TestShutdownMidMergeReturns(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	hb := newTestHost(t, base, b, "beta")
	var stall atomic.Bool
	release := make(chan struct{})
	ha.Server().load = func() (int, int) {
		if stall.Load() {
			<-release
		}
		return 0, 2
	}
	hb.Server().load = fixedLoad(1)
	t.Cleanup(func() { close(release) }) // runs before the hosts shut down
	hb.testHooks.beforeMergeHistory = func() {
		stall.Store(true) // A stops answering the probe that ends the merge
	}
	ha.start(t)
	hb.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	waitRole(t, hb, RoleActive, time.Second)
	ha.setKnown(hb.url)
	hb.setKnown(ha.url)
	waitFor(t, "merge to stall", stall.Load)
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := hb.Shutdown(ctx)
	if err != nil || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("Shutdown mid-merge: err=%v after %v", err, time.Since(start))
	}
}

func TestApplyRoleKeepsPromotion(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	ha.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	hb := newTestHost(t, base, b, "beta", ha.url)
	hb.start(t)
	waitRole(t, hb, RoleStandby, 2*time.Second)
	hb.Server().Promote() // a client's takeover does this
	hb.Host.mu.Lock()
	l := hb.Host.link
	hb.Host.mu.Unlock()
	hb.applyRole(l)
	if hb.Server().Mode() != ModeActive {
		t.Fatalf("applyRole overwrote the promotion: mode %v", hb.Server().Mode())
	}
}

func TestMergeLinkIsNotAStandbyCandidate(t *testing.T) {
	a, b, aURL, _, _, _, _ := linkPair(t, "pw")
	a.load = fixedLoad(1)
	b.load = fixedLoad(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := dialMerge(ctx, aURL, b)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	waitFor(t, "merge link", func() bool { return len(a.Links()) == 1 })
	if p := a.PickStandby(); p != nil {
		t.Fatalf("a merge link was picked as standby: %+v", p)
	}
}

func TestSendMergeHistoryOversizeLineErrors(t *testing.T) {
	s, _ := startModeServer(t, Config{Channels: []string{"general"}})
	big := proto.ChatMessage{ID: "big", Channel: "general", Text: strings.Repeat("x", 70*1024), TS: time.Now().UTC()}
	if err := s.hist.Add(big); err != nil {
		t.Skipf("history refuses the line: %v", err)
	}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("sendMergeHistory panicked: %v", r)
			}
		}()
		err = s.sendMergeHistory(nil)
	}()
	if err == nil {
		t.Fatal("want an error for a line that cannot fit a frame")
	}
}

func TestReplaceChainKicksBannedAndDropsRemovedLinks(t *testing.T) {
	_, b, _, bURL, ak, _, ac := linkPair(t, "pw")
	m := mustID(t)
	mc, env := authWarm(t, bURL, m, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	fc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	head := fc.Head()
	e, err := chain.NewEntry(ak, &head, chain.OpBan, chain.FPBody{Fingerprint: identity.Fingerprint(m.PublicKey())}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := fc.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := b.replaceChain(fc.Entries()); err != nil {
		t.Fatal(err)
	}
	_ = mc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := mc.ReadMessage(); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatal("the banned warm session was not closed by replaceChain")
			}
			return
		}
	}
}
