package server

import (
	"context"
	"sync"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// Orchestrator timing. Variables so tests can shorten them.
var (
	// coldStartWindow is how long a starting host looks for an active host.
	coldStartWindow = 3 * time.Second
	// probeEvery is how often an active host looks for another active host.
	probeEvery = 30 * time.Second
	// handbackIdle is how long voice must be empty before an active host hands
	// its role back to a higher-priority host linked to it.
	handbackIdle = 2 * time.Minute
)

const (
	dialTimeout   = 5 * time.Second
	maxKnownHosts = 16
	// handoverWait is how long the old active host waits for the receiver of
	// a handover to report itself active.
	handoverWait = 3 * time.Second
)

// Role names reported by Host.Role.
const (
	RoleActive  = "active"
	RoleStandby = "standby"
	RoleIdle    = "idle"
)

// HostConfig configures a Host.
type HostConfig struct {
	Server Config
	// Known returns the URLs of the other hosts we know, except our own
	// advertised ones.
	Known func() []string
	// SaveKnown is given the known host URLs whenever the set grows.
	SaveKnown func([]string)
	// Notify gets short status lines: "hosting: active", "hosting: standby for
	// <name>", "hosting: idle".
	Notify func(string)
}

// Host owns a Server's role: it starts idle, finds the active host or becomes
// it, follows it as standby or idle, takes over when it disappears, settles a
// split brain, and hands the role back to a higher-priority host.
type Host struct {
	hc  HostConfig
	srv *Server

	ctx      context.Context // canceled by Shutdown
	cancel   context.CancelFunc
	done     chan struct{} // closed when the loop has exited
	promoted chan struct{} // signaled by the server's promote hook (cap 1)
	started  bool
	runs     sync.WaitGroup // link Run goroutines

	// Owned by the loop goroutine.
	seen    []string // host URLs learned from hosts frames and probe replies
	hadLink bool     // this host has followed an active host

	mu       sync.Mutex
	role     string
	roleName string // the active host's name while standby
	link     *Link
	active   proto.HostInfo // the active host as last learned, while not active
}

// NewHost returns a Host for hc. The server starts in ModeIdle whatever
// hc.Server.Mode says.
func NewHost(hc HostConfig) *Host {
	hc.Server.Mode = ModeIdle
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{
		hc:       hc,
		srv:      New(hc.Server),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		promoted: make(chan struct{}, 1),
		role:     RoleIdle,
	}
}

// Server returns the host's server.
func (h *Host) Server() *Server { return h.srv }

// Role is "active", "standby" or "idle".
func (h *Host) Role() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.role
}

// Active describes the active host: this one when active, else the host we
// follow.
func (h *Host) Active() proto.HostInfo {
	h.mu.Lock()
	role, l, info := h.role, h.link, h.active
	h.mu.Unlock()
	if role == RoleActive {
		return h.srv.selfInfo()
	}
	if l != nil {
		if hs, ok := l.Hosts(); ok {
			return hs.Active
		}
	}
	return info
}

// Start starts the server in ModeIdle and runs the orchestrator.
func (h *Host) Start() error {
	if err := h.srv.Start(); err != nil {
		return err
	}
	h.srv.SetPromoteHook(func() { signal(h.promoted) })
	h.started = true
	go h.loop()
	return nil
}

// Shutdown stops the orchestrator and the server.
func (h *Host) Shutdown(ctx context.Context) error {
	h.cancel()
	if h.started {
		select {
		case <-h.done:
		case <-ctx.Done():
		}
	}
	h.mu.Lock()
	l := h.link
	h.mu.Unlock()
	if l != nil {
		l.Close()
	}
	h.runs.Wait()
	return h.srv.Shutdown(ctx)
}

func (h *Host) stopped() bool { return h.ctx.Err() != nil }

// loop is the state machine. It runs on one goroutine, which makes every mode
// change; a nil link means the host is active (or stopping).
func (h *Host) loop() {
	defer close(h.done)
	l := h.rejoin(nil)
	for !h.stopped() {
		if l != nil {
			l = h.runLinked(l)
		} else {
			l = h.runActive()
		}
	}
	if l != nil {
		l.Close()
	}
}

// setRole records the role and tells Notify when it changed.
func (h *Host) setRole(role, name string) {
	h.mu.Lock()
	changed := h.role != role || h.roleName != name
	h.role, h.roleName = role, name
	h.mu.Unlock()
	if !changed || h.hc.Notify == nil {
		return
	}
	switch role {
	case RoleActive:
		h.hc.Notify("hosting: active")
	case RoleStandby:
		h.hc.Notify("hosting: standby for " + name)
	default:
		h.hc.Notify("hosting: idle")
	}
}

// targets are the hosts to look at: known, learned, and extra, without our own
// addresses.
func (h *Host) targets(extra []string) []string {
	own := make(map[string]bool)
	for _, a := range h.srv.cfg.Advertise {
		own[a] = true
	}
	var known []string
	if h.hc.Known != nil {
		known = h.hc.Known()
	}
	var out []string
	for _, list := range [][]string{known, h.seen, extra} {
		for _, u := range list {
			if u != "" && !own[u] {
				own[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

// learn remembers host addresses and passes the set to SaveKnown when it grew.
func (h *Host) learn(addrs ...string) {
	own := make(map[string]bool)
	for _, a := range h.srv.cfg.Advertise {
		own[a] = true
	}
	for _, a := range h.seen {
		own[a] = true
	}
	if h.hc.Known != nil {
		for _, a := range h.hc.Known() {
			own[a] = true
		}
	}
	grew := false
	for _, a := range addrs {
		if a != "" && !own[a] && len(h.seen) < maxKnownHosts {
			own[a] = true
			h.seen = append(h.seen, a)
			grew = true
		}
	}
	if grew && h.hc.SaveKnown != nil {
		h.hc.SaveKnown(append([]string(nil), h.seen...))
	}
}

func (h *Host) learnHosts(hs proto.Hosts) {
	h.learn(hs.Active.Addrs...)
	if hs.Standby != nil {
		h.learn(hs.Standby.Addrs...)
	}
}

// probeEach probes urls in parallel and calls fn with each reply as it comes,
// until fn returns true or ctx ends.
func (h *Host) probeEach(ctx context.Context, urls []string, fn func(url string, r proto.LinkProbeReply) bool) {
	type res struct {
		url string
		r   proto.LinkProbeReply
		err error
	}
	ch := make(chan res, len(urls))
	for _, u := range urls {
		go func() {
			r, err := ProbeHost(ctx, u, h.srv)
			ch <- res{u, r, err}
		}()
	}
	for range urls {
		select {
		case x := <-ch:
			if x.err == nil {
				h.learn(x.r.Self.Addrs...)
				if fn(x.url, x.r) {
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// sameServer reports whether a probe reply is from an active host of our server.
func (h *Host) sameServer(r proto.LinkProbeReply) bool {
	return r.Active && r.ServerID == h.srv.chain.ID().String()
}

// coldStart looks for an active host among the known ones (and extra) for up
// to coldStartWindow and links to the first it finds.
func (h *Host) coldStart(extra []string) *Link {
	urls := h.targets(extra)
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(h.ctx, coldStartWindow)
	defer cancel()
	var link *Link
	h.probeEach(ctx, urls, func(url string, r proto.LinkProbeReply) bool {
		if !h.sameServer(r) {
			return false
		}
		dctx, dcancel := context.WithTimeout(h.ctx, dialTimeout)
		l, err := DialLink(dctx, url, h.srv)
		dcancel()
		if err != nil {
			return false
		}
		link = l
		h.mu.Lock()
		h.active = r.Self
		h.mu.Unlock()
		return true
	})
	return link
}

// rejoin is the cold start: it links to an active host if one answers, else
// makes this host active. It returns the link, or nil when the host is active
// (or stopping).
func (h *Host) rejoin(extra []string) *Link {
	l := h.coldStart(extra)
	if h.stopped() {
		if l != nil {
			l.Close()
		}
		return nil
	}
	if h.srv.Mode() == ModeActive {
		// A client's takeover promoted us meanwhile.
		if l != nil {
			l.Close()
		}
		h.becomeActive(false)
		return nil
	}
	if l != nil {
		h.install(l)
		return l
	}
	h.becomeActive(h.hadLink)
	return nil
}

// install starts l and makes it the host's link.
func (h *Host) install(l *Link) {
	h.hadLink = true
	select { // a promotion signal from before this link is stale
	case <-h.promoted:
	default:
	}
	h.mu.Lock()
	h.link = l
	h.mu.Unlock()
	h.srv.SetLinkView(l.ActiveAlive, l.Standby)
	h.runs.Add(1)
	go func() {
		defer h.runs.Done()
		l.Run()
	}()
	h.applyRole(l)
}

// applyRole sets the server mode and role from what the active host says of us.
func (h *Host) applyRole(l *Link) {
	if hs, ok := l.Hosts(); ok {
		h.learnHosts(hs)
	}
	h.mu.Lock()
	name := h.active.Name
	h.mu.Unlock()
	if hs, ok := l.Hosts(); ok {
		name = hs.Active.Name
	}
	if l.Standby() {
		h.srv.SetMode(ModeStandby)
		h.setRole(RoleStandby, name)
	} else {
		h.srv.SetMode(ModeIdle)
		h.setRole(RoleIdle, "")
	}
}

// becomeActive makes the host serve clients. After following an active host
// (failover) the warm sessions go live and everyone is told; a first start has
// nothing to announce.
func (h *Host) becomeActive(failover bool) {
	h.mu.Lock()
	h.link = nil
	h.mu.Unlock()
	if failover {
		h.srv.Promote()
	} else {
		h.srv.SetMode(ModeActive)
		h.srv.PromoteWarm()
	}
	h.setRole(RoleActive, "")
}

// runLinked follows the active host over l until it is lost, then returns the
// next link, or nil once the host is active.
func (h *Host) runLinked(l *Link) *Link {
	beat := time.NewTicker(heartbeatInterval)
	defer beat.Stop()
	for {
		select {
		case <-h.ctx.Done():
			l.Close()
			return nil
		case <-l.RoleChanged():
			h.applyRole(l)
		case <-h.promoted:
			// A client's takeover promoted us.
			l.Close()
			h.becomeActive(false)
			return nil
		case <-l.Handover():
			l.Close()
			h.srv.Promote()
			h.becomeActive(false)
			return nil
		case <-beat.C:
			if !l.ActiveAlive(time.Now()) {
				l.Close()
			}
		case <-l.Done():
			return h.failover(l)
		}
	}
}

// failover runs when the link to the active host ended: it gives clients
// heartbeatGrace to send takeovers, then looks for an active host again and
// becomes active itself if there is none.
func (h *Host) failover(l *Link) *Link {
	var extra []string
	if hs, ok := l.Hosts(); ok {
		h.learnHosts(hs)
		extra = append(extra, hs.Active.Addrs...)
		if hs.Standby != nil {
			extra = append(extra, hs.Standby.Addrs...)
		}
	}
	h.mu.Lock()
	h.link = nil
	h.mu.Unlock()
	select {
	case <-h.ctx.Done():
		return nil
	case <-h.promoted:
		h.becomeActive(false)
		return nil
	case <-time.After(heartbeatGrace):
	}
	return h.rejoin(extra)
}

// runActive serves as the active host until it is demoted, then returns the
// link to the new active host, or nil if stopping or active again.
func (h *Host) runActive() *Link {
	select { // a signal from a takeover that already happened is stale
	case <-h.promoted:
	default:
	}
	probe := time.NewTicker(probeEvery)
	defer probe.Stop()
	check := time.NewTicker(handbackCheck())
	defer check.Stop()
	var emptySince time.Time
	for {
		select {
		case <-h.ctx.Done():
			return nil
		case <-h.promoted:
		case <-probe.C:
			if l, demoted := h.probeAndYield(); demoted {
				return l
			}
		case <-check.C:
			for _, p := range h.srv.Links() {
				h.learn(p.Addrs...)
			}
			voice, _ := h.srv.load()
			now := time.Now()
			if voice > 0 {
				emptySince = time.Time{}
				continue
			}
			if emptySince.IsZero() {
				emptySince = now
			}
			if now.Sub(emptySince) < handbackIdle {
				continue
			}
			if p := h.handbackCandidate(); p != nil {
				if l, ok := h.handback(p); ok {
					return l
				}
				emptySince = now // try again after another idle period
			}
		}
	}
}

// handbackCheck is how often an active host looks for a handback; short
// relative to handbackIdle so the idle time is honored closely.
func handbackCheck() time.Duration {
	d := handbackIdle / 4
	if d < 20*time.Millisecond {
		d = 20 * time.Millisecond
	}
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// loses is the yield rule: fewer voice members yields; on a tie fewer clients
// yields; on a tie the larger priority number yields.
func loses(voice, clients, prio int, r proto.LinkProbeReply) bool {
	if voice != r.VoiceMembers {
		return voice < r.VoiceMembers
	}
	if clients != r.Clients {
		return clients < r.Clients
	}
	return prio > r.Priority
}

// probeAndYield looks for another active host. If there is one we lose to, it
// merges into it; demoted reports that the host left the active role, and l is
// then the link to the winner (nil if none could be made and the host became
// active again).
func (h *Host) probeAndYield() (l *Link, demoted bool) {
	urls := h.targets(nil)
	if len(urls) == 0 {
		return nil, false
	}
	timeout := probeEvery
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(h.ctx, timeout)
	defer cancel()
	var winURL string
	var win proto.LinkProbeReply
	h.probeEach(ctx, urls, func(url string, r proto.LinkProbeReply) bool {
		if !h.sameServer(r) {
			return false
		}
		voice, clients := h.srv.load()
		if loses(voice, clients, h.srv.selfInfo().Priority, r) {
			winURL, win = url, r
			return true
		}
		return false
	})
	if winURL == "" {
		return nil, false
	}
	return h.yield(winURL, win)
}

// yield merges this host into the winner of a split brain: it sends its chat
// history, tells its clients to move, tells those who lost a chain change, takes
// the winner's chain and links to it.
func (h *Host) yield(url string, win proto.LinkProbeReply) (*Link, bool) {
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	defer cancel()
	ws, theirs, err := dialMerge(ctx, url, h.srv)
	if err != nil {
		h.srv.logf("server: merge into %s: %v", url, err)
		return nil, false
	}
	defer ws.Close()
	mine := h.srv.chain.Entries()
	full := append(mine[:1:1], theirs...)
	if _, err := chain.FromEntries(full); err != nil {
		h.srv.logf("server: merge into %s: its chain does not verify: %v", url, err)
		return nil, false
	}
	// From here the host is no longer active: no chat or chain change lands
	// after the history below was read.
	h.srv.goIdle()
	if err := h.srv.sendMergeHistory(ws); err != nil {
		h.srv.logf("server: merge into %s: %v", url, err)
	}
	h.srv.Demote(win.Self, "merge", h.srv.lostAuthors(full))
	if err := h.srv.replaceChain(full); err != nil {
		h.srv.logf("server: merge into %s: taking its chain: %v", url, err)
	}
	h.learn(win.Self.Addrs...)
	h.setRole(RoleIdle, "")
	return h.relink(win.Self, []string{url}, true), true
}

// relink links to the host that is now active, retrying briefly; with none, it
// cold-starts (which makes this host active when nobody answers).
func (h *Host) relink(to proto.HostInfo, urls []string, full bool) *Link {
	urls = append(append([]string(nil), urls...), to.Addrs...)
	for attempt := 0; attempt < 10 && !h.stopped(); attempt++ {
		for _, u := range urls {
			dctx, cancel := context.WithTimeout(h.ctx, dialTimeout)
			l, err := dialLinkOpts(dctx, u, h.srv, full)
			cancel()
			if err == nil {
				h.mu.Lock()
				h.active = to
				h.mu.Unlock()
				h.install(l)
				return l
			}
		}
		select {
		case <-h.ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return h.rejoin(urls)
}

// handbackCandidate is the linked host with the best priority among those
// that outrank this host, or nil.
func (h *Host) handbackCandidate() *LinkPeer {
	mine := h.srv.selfInfo().Priority
	for _, p := range h.srv.Links() { // best priority first
		if p.Priority > 0 && p.Priority < mine && len(p.Addrs) > 0 {
			return &p
		}
	}
	return nil
}

// handback hands the active role to p: it sends handover, waits for p to
// report itself active, then moves clients to it and links to it as idle or
// standby. It reports false, leaving the host active, if p never became active.
func (h *Host) handback(p *LinkPeer) (*Link, bool) {
	if !h.srv.sendHandover(p.Fingerprint) {
		return nil, false
	}
	if !h.waitActive(p.Addrs) {
		return nil, false
	}
	to := proto.HostInfo{Fingerprint: p.Fingerprint, Name: p.Name, Addrs: p.Addrs, Priority: p.Priority}
	h.srv.Demote(to, "handback", nil)
	h.setRole(RoleIdle, "")
	return h.relink(to, nil, false), true
}

// waitActive polls the host at addrs until it reports active, up to
// handoverWait.
func (h *Host) waitActive(addrs []string) bool {
	deadline := time.Now().Add(handoverWait)
	for time.Now().Before(deadline) && !h.stopped() {
		for _, u := range addrs {
			ctx, cancel := context.WithTimeout(h.ctx, time.Second)
			r, err := ProbeHost(ctx, u, h.srv)
			cancel()
			if err == nil && h.sameServer(r) {
				return true
			}
		}
		select {
		case <-h.ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}
