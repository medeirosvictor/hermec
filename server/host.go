package server

import (
	"context"
	"sync"
	"time"

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

	// maxHandbackCheck caps how often an active host looks for a handback.
	maxHandbackCheck = 5 * time.Second
	// handoverProbeTimeout and handoverPollEvery pace waitActive's probes of
	// the host a role was handed to.
	handoverProbeTimeout = time.Second
	handoverPollEvery    = 50 * time.Millisecond
	// relinkTries and relinkEvery bound relink's retries of the new active host.
	relinkTries = 10
	relinkEvery = 100 * time.Millisecond
	// probeMaxTimeout caps the time an active host spends probing for a rival.
	probeMaxTimeout = 5 * time.Second
)

var (
	// coldProbeEvery is how often a cold start probes again while no active
	// host has answered.
	coldProbeEvery = 200 * time.Millisecond
	// handoverWait is how long the old active host waits for the receiver of
	// a handover to report itself active.
	handoverWait = 3 * time.Second

	// Test seams, called at fixed points of a merge.
	beforeLoop         func(*Host)
	beforeYieldRecheck func(*Host)
	beforeMergeHistory func(*Host)
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
	// OnPasswordKey, when set, is called with the password key the active host
	// supplied over the link whenever it differs from the one held.
	OnPasswordKey func([]byte)
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
	h := &Host{
		hc:       hc,
		srv:      New(hc.Server),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		promoted: make(chan struct{}, 1),
		role:     RoleIdle,
	}
	if hc.OnPasswordKey != nil {
		h.srv.SetPasswordKeyHook(hc.OnPasswordKey)
	}
	return h
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
	if beforeLoop != nil {
		beforeLoop(h)
	}
	l := h.rejoin(nil, coldStartWindow)
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
	for _, a := range h.srv.advertise() {
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
	for _, a := range h.srv.advertise() {
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
func (h *Host) coldStart(extra []string, window time.Duration) *Link {
	if len(h.targets(extra)) == 0 {
		return nil
	}
	mine := h.srv.selfInfo().Priority
	start := time.Now()
	end, hardEnd := start.Add(window), start.Add(3*window)
	for {
		// Probe again and again: a host that answers inactive may be about to
		// become active, and a refused one may be starting.
		var link *Link
		better := false // a reachable, inactive host outranks us
		ctx, cancel := context.WithTimeout(h.ctx, window)
		h.probeEach(ctx, h.targets(extra), func(url string, r proto.LinkProbeReply) bool {
			if !h.sameServer(r) {
				better = better || (r.ServerID == h.srv.chain.ID().String() && r.Priority > 0 && r.Priority < mine)
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
		cancel()
		if link != nil || h.stopped() {
			return link
		}
		// Past the window, only a better-ranked host that is still coming up
		// keeps us waiting: it will become active, so we must not.
		if now := time.Now(); !now.Before(end) && !(better && now.Before(hardEnd)) {
			return nil
		}
		if !h.pause(coldProbeEvery) {
			return nil
		}
	}
}

// pause sleeps for d and reports false if the host is stopping.
func (h *Host) pause(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-h.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// rejoin is the cold start: it links to an active host if one answers, else
// makes this host active. It returns the link, or nil when the host is active
// (or stopping).
func (h *Host) rejoin(extra []string, window time.Duration) *Link {
	l := h.coldStart(extra, window)
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
	// A takeover may have promoted the host meanwhile; that stands.
	if l.Standby() {
		if h.srv.setFollowerMode(ModeStandby) {
			h.setRole(RoleStandby, name)
		}
	} else if h.srv.setFollowerMode(ModeIdle) {
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
			if h.srv.Mode() != ModeActive {
				continue // a stale signal
			}
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
	// Only the standby may promote itself at once. Any other host gives it time
	// to do so, and to be found by a cold start that outlasts it.
	window := coldStartWindow
	if !l.Standby() {
		window = 2 * coldStartWindow
	}
	grace := time.NewTimer(heartbeatGrace)
	defer grace.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return nil
		case <-h.promoted:
			if h.srv.Mode() == ModeActive {
				h.becomeActive(false)
				return nil
			}
			continue // a stale signal
		case <-grace.C:
		}
		break
	}
	return h.rejoin(extra, window)
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
	// Look often relative to handbackIdle so the idle time is honored closely.
	check := time.NewTicker(min(handbackIdle/4, maxHandbackCheck))
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
				emptySince = time.Now() // try again after another idle period
			}
		}
	}
}

// waitActive polls the host at addrs until it reports active, up to
// handoverWait.
func (h *Host) waitActive(addrs []string) bool {
	deadline := time.Now().Add(handoverWait)
	for time.Now().Before(deadline) && !h.stopped() {
		for _, u := range addrs {
			ctx, cancel := context.WithTimeout(h.ctx, handoverProbeTimeout)
			r, err := ProbeHost(ctx, u, h.srv)
			cancel()
			if err == nil && h.sameServer(r) {
				return true
			}
		}
		h.pause(handoverPollEvery)
	}
	return false
}
