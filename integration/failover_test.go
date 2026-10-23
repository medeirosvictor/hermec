package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

// fastHosts shortens the host and heartbeat timers for a test.
func fastHosts(t *testing.T) {
	t.Helper()
	r1 := server.SetHeartbeatForTest(50*time.Millisecond, 150*time.Millisecond)
	r2 := server.SetHostTimersForTest(300*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond)
	t.Cleanup(func() { r2(); r1() })
}

// hostSet is n hosts on one chain: host 0 is the founder at priority 1, host i
// has priority i+1. Each host's known set is settable.
type hostSet struct {
	t     *testing.T
	hosts []*server.Host
	keys  []*identity.Identity
	urls  []string
	fps   []string

	mu    sync.Mutex
	known [][]string
}

// newHostSet builds, without starting, n hosts with loopback ICE and fast
// timers. With linked, each host knows all the others.
func newHostSet(t *testing.T, n int, linked bool) *hostSet {
	t.Helper()
	fastHosts(t)
	hs := &hostSet{t: t, known: make([][]string, n)}
	cfg := server.Config{
		Roles:            roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:         []string{"general"},
		VoiceChannels:    []string{voiceCh},
		AllowLoopbackICE: true,
	}
	for i := 0; i < n; i++ {
		id := newID(t)
		hs.keys = append(hs.keys, id)
		hs.fps = append(hs.fps, identity.Fingerprint(id.PublicKey()))
	}
	base, _, err := server.OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), hs.keys[0], cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < n; i++ {
		head := base.Head()
		e, err := chain.NewEntry(hs.keys[0], &head, chain.OpHostAdd,
			chain.HostAddBody{PubKey: []byte(hs.keys[i].PublicKey()), Priority: i + 1}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := base.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		addr := freeLoopbackAddr(t)
		hs.urls = append(hs.urls, "ws://"+addr+"/")
	}
	for i := 0; i < n; i++ {
		cc, err := chain.FromEntries(base.Entries())
		if err != nil {
			t.Fatal(err)
		}
		hcfg := cfg
		hcfg.Addr = hs.urls[i][len("ws://") : len(hs.urls[i])-1]
		hcfg.Advertise = []string{hs.urls[i]}
		hcfg.HostKey, hcfg.Chain = hs.keys[i], cc
		hcfg.HostName = fmt.Sprintf("host-%d", i)
		if linked {
			for j, u := range hs.urls {
				if j != i {
					hs.known[i] = append(hs.known[i], u)
				}
			}
		}
		h := server.NewHost(server.HostConfig{
			Server: hcfg,
			Known: func() []string {
				hs.mu.Lock()
				defer hs.mu.Unlock()
				return slices.Clone(hs.known[i])
			},
		})
		hs.hosts = append(hs.hosts, h)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = h.Shutdown(ctx)
		})
	}
	return hs
}

func (hs *hostSet) setKnown(i int, urls ...string) {
	hs.mu.Lock()
	hs.known[i] = urls
	hs.mu.Unlock()
}

func (hs *hostSet) start(i int) {
	hs.t.Helper()
	if err := hs.hosts[i].Start(); err != nil {
		hs.t.Fatalf("start host %d: %v", i, err)
	}
}

func (hs *hostSet) kill(i int) {
	hs.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.hosts[i].Shutdown(ctx); err != nil {
		hs.t.Fatalf("kill host %d: %v", i, err)
	}
}

func (hs *hostSet) waitRole(i int, role string, d time.Duration) {
	hs.t.Helper()
	eventually(hs.t, d, fmt.Sprintf("host %d role %s", i, role), func() bool { return hs.hosts[i].Role() == role })
}

// startHostSet starts n linked hosts: host 0 active, the rest following it.
func startHostSet(t *testing.T, n int) *hostSet {
	t.Helper()
	hs := newHostSet(t, n, true)
	hs.start(0)
	hs.waitRole(0, server.RoleActive, 5*time.Second)
	for i := 1; i < n; i++ {
		hs.start(i)
	}
	eventually(t, 10*time.Second, "all hosts linked", func() bool { return len(hs.hosts[0].Server().Links()) == n-1 })
	if n > 1 {
		hs.waitRole(1, server.RoleStandby, 5*time.Second)
	}
	return hs
}

// startHosts is startHostSet for tests that need only the hosts.
func startHosts(t *testing.T, n int) []*server.Host { return startHostSet(t, n).hosts }

// freeLoopbackAddr returns a loopback address that was free a moment ago, so
// a host can advertise it before it starts.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func hostURL(h *server.Host) string { return "ws://" + h.Server().Addr() + "/" }

// follower is a client that does what the UI does around host failover: it
// keeps a warm session on the standby from the hosts event, opens a warm voice
// peer while in a call, and on the loss of the active host takes over on the
// standby and adopts the call, or follows a move.
type follower struct {
	t    *testing.T
	id   *identity.Identity
	name string
	fp   string
	url  string // the first address dialed
	// urlOnly makes recovery redial only url, to model a network blip that cuts
	// every path to the active host.
	urlOnly bool
	src     *markerSource
	sink    *recSink

	mu        sync.Mutex
	pin       string
	cur, warm *client.Client
	standby   proto.HostInfo
	active    proto.HostInfo
	warmGen   int
	peerFor   *client.Client
	inVoice   bool
	switching bool
	closed    bool
	lastHosts *proto.Hosts
	lastFrom  *client.Client
	chats     []proto.ChatMessage
	history   []proto.ChatMessage
	moves     []proto.Move
	alive     int // Takeover refusals with active_alive
	takeovers int // promotions of the warm session
	redials   int // recoveries by redialing a full session
}

// followFailover dials url and returns a follower on it that is joined to
// general.
func followFailover(t *testing.T, url, name string, marker byte) *follower {
	t.Helper()
	id := newID(t)
	f := &follower{t: t, id: id, name: name, url: url, src: &markerSource{marker: marker}, sink: newRecSink()}
	t.Cleanup(f.close)
	c, err := f.dial([]string{url}, client.DialOpts{})
	if err != nil {
		t.Fatalf("dial %s: %v", name, err)
	}
	f.fp = c.Fingerprint()
	f.mu.Lock()
	f.cur, f.pin = c, c.ServerID()
	f.mu.Unlock()
	f.attach(c)
	if err := c.Join(testCtx(t), "general"); err != nil {
		t.Fatalf("join %s: %v", name, err)
	}
	return f
}

// dial tries urls in order.
func (f *follower) dial(urls []string, o client.DialOpts) (*client.Client, error) {
	var last error
	for _, u := range urls {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c, err := client.DialWith(ctx, u, f.id, f.name, "", o)
		cancel()
		if err == nil {
			c.SetLoopbackICE(true)
			return c, nil
		}
		last = err
	}
	return nil, last
}

func (f *follower) fullOpts() client.DialOpts {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := client.DialOpts{Pin: f.pin}
	if f.cur != nil {
		o.MinChainSeq = f.cur.ChainSeq()
	}
	return o
}

func (f *follower) fail(format string, args ...any) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if !closed {
		f.t.Errorf(f.name+": "+format, args...)
	}
}

func (f *follower) close() {
	f.mu.Lock()
	f.closed = true
	cur, warm := f.cur, f.warm
	f.mu.Unlock()
	if cur != nil {
		cur.Close()
	}
	if warm != nil {
		warm.Close()
	}
}

// attach starts draining c's events.
func (f *follower) attach(c *client.Client) {
	go func() {
		for ev := range c.Events() {
			f.handle(c, ev)
		}
		f.lost(c) // closed without ActiveLost (no heartbeat seen yet)
	}()
}

func (f *follower) handle(c *client.Client, ev client.Event) {
	f.mu.Lock()
	switch {
	case ev.Chat != nil:
		f.chats = append(f.chats, *ev.Chat)
	case ev.History != nil:
		f.history = append(f.history, ev.History.Messages...)
	case ev.Hosts != nil:
		h := *ev.Hosts
		f.lastHosts, f.lastFrom = &h, c
		if c == f.cur {
			f.applyHostsLocked(h)
		}
	}
	isCur, isWarm := c == f.cur, c == f.warm
	f.mu.Unlock()
	switch {
	case ev.ActiveLost && isCur:
		f.lost(c)
	case ev.ActiveLost && isWarm:
		f.warmLost(c)
	case ev.Move != nil && isCur:
		f.move(*ev.Move)
	}
}

// applyHostsLocked follows a hosts event: it keeps one warm session on the
// standby.
func (f *follower) applyHostsLocked(h proto.Hosts) {
	f.active = h.Active
	if h.Standby == nil {
		f.dropWarmLocked()
		f.standby = proto.HostInfo{}
		return
	}
	sb := *h.Standby
	if sb.Fingerprint == f.standby.Fingerprint && slices.Equal(sb.Addrs, f.standby.Addrs) {
		return
	}
	f.dropWarmLocked()
	f.standby = sb
	go f.dialWarm(sb, f.warmGen)
}

func (f *follower) dropWarmLocked() {
	if f.warm != nil {
		_ = f.warm.Close()
		f.warm, f.peerFor = nil, nil
	}
	f.warmGen++
}

// dialWarm opens the warm session to sb and reports whether it was reachable.
func (f *follower) dialWarm(sb proto.HostInfo, gen int) {
	o := f.fullOpts()
	o.Warm = true
	c, err := f.dial(sb.Addrs, o)
	f.mu.Lock()
	cur := f.cur
	f.mu.Unlock()
	if cur != nil {
		_ = cur.ReportReach(sb.Fingerprint, err == nil)
	}
	if err != nil {
		time.AfterFunc(200*time.Millisecond, func() { f.retryWarm(gen) })
		return
	}
	f.mu.Lock()
	if f.closed || gen != f.warmGen {
		f.mu.Unlock()
		c.Close()
		return
	}
	f.warm = c
	f.mu.Unlock()
	f.attach(c)
	f.maybePeer()
}

func (f *follower) retryWarm(gen int) {
	f.mu.Lock()
	if f.closed || gen != f.warmGen || f.warm != nil || f.standby.Fingerprint == "" {
		f.mu.Unlock()
		return
	}
	sb := f.standby
	f.mu.Unlock()
	f.dialWarm(sb, gen)
}

func (f *follower) warmLost(c *client.Client) {
	f.mu.Lock()
	if c != f.warm {
		f.mu.Unlock()
		return
	}
	f.warm, f.peerFor = nil, nil
	gen := f.warmGen
	f.mu.Unlock()
	c.Close()
	time.AfterFunc(200*time.Millisecond, func() { f.retryWarm(gen) })
}

// maybePeer opens the warm voice peer on the warm session while in a call.
func (f *follower) maybePeer() {
	f.mu.Lock()
	w := f.warm
	if !f.inVoice || w == nil || f.peerFor == w {
		f.mu.Unlock()
		return
	}
	f.peerFor = w
	f.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := w.OpenWarmPeer(ctx); err != nil {
			f.t.Logf("%s: warm peer: %v", f.name, err)
		}
	}()
}

// lost handles the loss of c, the current session: take over on the standby,
// or redial the active host when the standby says it is alive.
func (f *follower) lost(c *client.Client) {
	f.mu.Lock()
	if f.closed || c != f.cur || f.switching {
		f.mu.Unlock()
		return
	}
	f.switching = true
	w := f.warm
	f.mu.Unlock()
	go f.recover(c, w)
}

func (f *follower) recover(old, w *client.Client) {
	// Like the UI: Takeover is repeated while the standby answers active_alive,
	// for takeoverWindow; the first refusal also starts one redial of the
	// previous active host, which wins if the host only dropped us.
	const takeoverWindow = 3 * time.Second
	var decided atomic.Bool
	f.mu.Lock()
	urls := []string{f.url}
	if !f.urlOnly {
		urls = append(urls, f.active.Addrs...)
	}
	f.mu.Unlock()
	redial := func() {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) && !decided.Load() {
			if c2, err := f.dial(urls, f.fullOpts()); err == nil {
				if !decided.CompareAndSwap(false, true) {
					c2.Close()
					return
				}
				f.mu.Lock()
				f.redials++
				f.mu.Unlock()
				f.swap(c2, old, false)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if w == nil { // nothing warm: only the redial can recover
		redial()
		if !decided.Load() {
			f.fail("no recovery after losing the active host")
		}
		return
	}
	deadline := time.Now().Add(takeoverWindow)
	started := false
	for !decided.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		err := w.Takeover(ctx)
		cancel()
		if err == nil {
			if !decided.CompareAndSwap(false, true) {
				f.fail("promoted after the redial won")
				return
			}
			f.installPromoted(w, old)
			return
		}
		var se *client.ServerError
		if !errors.As(err, &se) || se.Code != proto.CodeActiveAlive || time.Now().After(deadline) {
			if decided.CompareAndSwap(false, true) {
				f.fail("takeover failed: %v", err)
			}
			return
		}
		f.mu.Lock()
		f.alive++
		f.mu.Unlock()
		if !started {
			started = true
			go redial()
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// installPromoted makes the promoted warm session the current one.
func (f *follower) installPromoted(w, old *client.Client) {
	f.mu.Lock()
	f.cur, f.warm, f.peerFor = w, nil, nil
	f.standby = proto.HostInfo{}
	f.warmGen++
	f.takeovers++
	if f.lastFrom == w && f.lastHosts != nil {
		f.applyHostsLocked(*f.lastHosts)
	}
	f.mu.Unlock()
	f.finishSwap(w, old)
}

// swap makes c2, a freshly dialed full session, the current one.
func (f *follower) swap(c2, old *client.Client, dropWarm bool) {
	f.mu.Lock()
	f.cur = c2
	if dropWarm {
		f.dropWarmLocked()
		f.standby = proto.HostInfo{}
	}
	f.mu.Unlock()
	f.attach(c2)
	f.finishSwap(c2, old)
}

// finishSwap joins general on nu, moves the call over from old and closes old.
func (f *follower) finishSwap(nu, old *client.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := nu.Join(ctx, "general"); err != nil {
		f.fail("join after the swap: %v", err)
	}
	f.mu.Lock()
	voice := f.inVoice
	f.mu.Unlock()
	if voice {
		if err := nu.AdoptCall(ctx, old); err != nil {
			f.fail("adopt the call: %v", err)
		}
	}
	old.Close()
	f.mu.Lock()
	f.switching = false
	f.mu.Unlock()
	f.maybePeer()
}

// move follows a host's move with a full session on the host it names.
func (f *follower) move(m proto.Move) {
	f.mu.Lock()
	if f.closed || f.switching || len(m.To.Addrs) == 0 {
		f.mu.Unlock()
		return
	}
	f.switching = true
	f.moves = append(f.moves, m)
	old := f.cur
	f.mu.Unlock()
	go func() {
		c2, err := f.dial(m.To.Addrs, f.fullOpts())
		if err != nil {
			f.fail("move to %s: %v", m.To.Name, err)
			return
		}
		f.swap(c2, old, true)
	}()
}

func (f *follower) joinVoice(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	c := f.cur
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.JoinVoice(ctx, voiceCh, f.src, f.sink); err != nil {
		t.Fatalf("%s: JoinVoice: %v", f.name, err)
	}
	f.mu.Lock()
	f.inVoice = true
	f.mu.Unlock()
	f.maybePeer()
}

func (f *follower) leaveVoice(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	c := f.cur
	f.inVoice = false
	f.mu.Unlock()
	if err := c.LeaveVoice(testCtx(t)); err != nil {
		t.Fatalf("%s: LeaveVoice: %v", f.name, err)
	}
}

func (f *follower) say(t *testing.T, text string) {
	t.Helper()
	f.mu.Lock()
	c := f.cur
	f.mu.Unlock()
	if err := c.SendChat(testCtx(t), "general", text); err != nil {
		t.Fatalf("%s: say %q: %v", f.name, text, err)
	}
}

func (f *follower) hasChat(text string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.chats {
		if m.Text == text {
			return true
		}
	}
	return false
}

// warmReady reports a warm session on the standby, and when in a call, a
// connected warm peer.
func (f *follower) warmReady() bool {
	f.mu.Lock()
	w, voice := f.warm, f.inVoice
	f.mu.Unlock()
	return w != nil && w.Warm() && (!voice || w.WarmPeerReady())
}

func (f *follower) standbyFP() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.standby.Fingerprint
}

// fullOn reports a settled full session on the host with fingerprint fp.
func (f *follower) fullOn(fp string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cur != nil && !f.cur.Warm() && !f.switching && f.active.Fingerprint == fp
}

func (f *follower) counts() (alive, takeovers, redials int, moves []proto.Move) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive, f.takeovers, f.redials, slices.Clone(f.moves)
}

// historyOf returns the general history a fresh client reads from url.
func historyOf(t *testing.T, url string) []proto.ChatMessage {
	t.Helper()
	c := dialJoined(t, url, newID(t), "reader")
	ev := waitEvent(t, c, "history", func(e client.Event) bool { return e.History != nil })
	return ev.History.Messages
}

func hasText(msgs []proto.ChatMessage, text string, system bool) bool {
	for _, m := range msgs {
		if m.Text == text && m.System == system {
			return true
		}
	}
	return false
}

// bothWays waits for media between a and b in both directions, then returns.
func bothWays(t *testing.T, a, b *follower, n int) {
	t.Helper()
	ca, cb := a.sink.count(b.fp), b.sink.count(a.fp)
	eventually(t, 15*time.Second, "media a<->b", func() bool {
		return a.sink.count(b.fp) >= ca+n && b.sink.count(a.fp) >= cb+n
	})
}

func TestFailoverWithin2s(t *testing.T) {
	hs := startHostSet(t, 3)
	a := followFailover(t, hs.urls[0], "alice", 0xA1)
	b := followFailover(t, hs.urls[0], "bob", 0xB2)
	eventually(t, 10*time.Second, "warm sessions", func() bool { return a.warmReady() && b.warmReady() })
	a.joinVoice(t)
	b.joinVoice(t)
	eventually(t, 15*time.Second, "warm peers", func() bool { return a.warmReady() && b.warmReady() })
	bothWays(t, a, b, 10)
	a.say(t, "before")
	eventually(t, 5*time.Second, "chat before", func() bool { return b.hasChat("before") })

	start := time.Now()
	hs.kill(0)
	eventually(t, 2*time.Second, "both full on the standby", func() bool {
		return a.fullOn(hs.fps[1]) && b.fullOn(hs.fps[1])
	})
	t.Logf("failover took %v", time.Since(start))
	hs.waitRole(1, server.RoleActive, 2*time.Second)
	bothWays(t, a, b, 20)
	a.say(t, "after failover")
	eventually(t, 5*time.Second, "chat after", func() bool { return b.hasChat("after failover") })
	if h := historyOf(t, hs.urls[1]); !hasText(h, "host changed to host-1", true) {
		t.Fatalf("history of general has no host-changed line: %+v", h)
	}
	for _, f := range []*follower{a, b} {
		if _, takeovers, redials, _ := f.counts(); takeovers != 1 || redials != 0 {
			t.Errorf("%s: takeovers = %d, redials = %d; want 1 and 0", f.name, takeovers, redials)
		}
	}
}

func TestClientBlipNoFailover(t *testing.T) {
	hosts := startHosts(t, 3)
	p := newWSProxy(t, hostURL(hosts[0]))
	x := followFailover(t, p.url(), "xavier", 0xC3)
	x.urlOnly = true
	y := followFailover(t, hostURL(hosts[0]), "yara", 0xD4)
	activeFP := hosts[0].Active().Fingerprint
	eventually(t, 10*time.Second, "x warm", x.warmReady)

	p.cut()
	time.Sleep(2 * time.Second)
	p.restore()

	eventually(t, 10*time.Second, "x back on the active host", func() bool {
		_, _, redials, _ := x.counts()
		return redials >= 1 && x.fullOn(activeFP)
	})
	alive, takeovers, _, _ := x.counts()
	if alive < 1 || takeovers != 0 {
		t.Errorf("alive refusals = %d, takeovers = %d; want >= 1 and 0", alive, takeovers)
	}
	if hosts[0].Role() != server.RoleActive || hosts[1].Role() != server.RoleStandby {
		t.Fatalf("roles = %s, %s; want active, standby", hosts[0].Role(), hosts[1].Role())
	}
	if hosts[2].Role() == server.RoleActive {
		t.Fatal("host 2 became active")
	}
	x.say(t, "after the blip")
	eventually(t, 5*time.Second, "chat after the blip", func() bool { return y.hasChat("after the blip") })
}

func TestStandbyReplacedThenFailover(t *testing.T) {
	hs := startHostSet(t, 3)
	a := followFailover(t, hs.urls[0], "alice", 0xA1)
	b := followFailover(t, hs.urls[0], "bob", 0xB2)
	eventually(t, 10*time.Second, "warm sessions", func() bool { return a.warmReady() && b.warmReady() })
	a.joinVoice(t)
	b.joinVoice(t)
	bothWays(t, a, b, 10)

	hs.kill(1)
	eventually(t, 10*time.Second, "host 2 is the standby, warm", func() bool {
		return a.standbyFP() == hs.fps[2] && b.standbyFP() == hs.fps[2] && a.warmReady() && b.warmReady()
	})

	hs.kill(0)
	eventually(t, 5*time.Second, "both full on host 2", func() bool {
		return a.fullOn(hs.fps[2]) && b.fullOn(hs.fps[2])
	})
	hs.waitRole(2, server.RoleActive, 2*time.Second)
	bothWays(t, a, b, 20)
}

func TestSplitBrainChatMerge(t *testing.T) {
	hs := newHostSet(t, 2, false)
	hs.start(0)
	hs.start(1)
	hs.waitRole(0, server.RoleActive, 5*time.Second)
	hs.waitRole(1, server.RoleActive, 5*time.Second)
	a := followFailover(t, hs.urls[0], "alice", 0xA1)
	b := followFailover(t, hs.urls[1], "bob", 0xB2)
	a.say(t, "line on A")
	b.say(t, "line on B")
	eventually(t, 5*time.Second, "own lines", func() bool { return a.hasChat("line on A") && b.hasChat("line on B") })

	hs.setKnown(0, hs.urls[1])
	hs.setKnown(1, hs.urls[0])
	eventually(t, 15*time.Second, "bob follows the winner", func() bool { return b.fullOn(hs.fps[0]) })
	if _, _, _, moves := b.counts(); len(moves) != 1 || moves[0].Reason != "merge" {
		t.Fatalf("bob's moves = %+v, want one merge", moves)
	}
	hs.waitRole(0, server.RoleActive, 5*time.Second)
	hs.waitRole(1, server.RoleStandby, 10*time.Second)
	eventually(t, 10*time.Second, "both lines on the winner", func() bool {
		h := historyOf(t, hs.urls[0])
		return hasText(h, "line on A", false) && hasText(h, "line on B", false)
	})
	b.say(t, "after the merge")
	eventually(t, 5*time.Second, "chat across the merge", func() bool { return a.hasChat("after the merge") })
}

func TestHandbackOnlyWhenVoiceEmpty(t *testing.T) {
	hs := newHostSet(t, 2, false)
	hs.start(1)
	hs.waitRole(1, server.RoleActive, 5*time.Second)
	c := followFailover(t, hs.urls[1], "carol", 0xC3)
	c.joinVoice(t)

	hs.setKnown(0, hs.urls[1])
	hs.start(0)
	time.Sleep(2 * time.Second) // several handback checks
	if hs.hosts[1].Role() != server.RoleActive || hs.hosts[0].Role() == server.RoleActive {
		t.Fatalf("roles = %s, %s during voice; want host 1 active", hs.hosts[0].Role(), hs.hosts[1].Role())
	}
	if _, _, _, moves := c.counts(); len(moves) != 0 {
		t.Fatalf("moves during voice: %+v", moves)
	}

	c.leaveVoice(t)
	hs.waitRole(0, server.RoleActive, 10*time.Second)
	eventually(t, 10*time.Second, "carol moved to host 0", func() bool { return c.fullOn(hs.fps[0]) })
	if _, _, _, moves := c.counts(); len(moves) != 1 || moves[0].Reason != "handback" {
		t.Fatalf("moves = %+v, want one handback", moves)
	}
	if hs.hosts[1].Role() == server.RoleActive {
		t.Fatal("host 1 is still active")
	}
	c.say(t, "home again")
	eventually(t, 5*time.Second, "line on host 0", func() bool {
		return hasText(historyOf(t, hs.urls[0]), "home again", false)
	})
}
