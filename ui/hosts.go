package ui

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

// A session may hold a second, warm connection to the standby host. When the
// active host dies, the warm one takes over and the session swaps to it: the
// call moves across and the user sees no reconnect screen.

// hostKind says which background operation a hostResult reports.
type hostKind int

const (
	hostWarm     hostKind = iota // a warm dial to the standby
	hostTakeover                 // Takeover on the warm session
	hostMove                     // a full dial to the host a move names
	hostAdopt                    // the call moved onto the new client
	hostAlive                    // the standby says the active host is alive: redial it now
)

// takeoverRetryFor is how long a refused Takeover is repeated. A standby
// judges the active host dead only after its heartbeat grace, which a dropped
// connection can beat; the active host is redialed meanwhile. A variable so
// tests can shorten it.
var takeoverRetryFor = 3 * time.Second

const (
	dialAnyTimeout     = 20 * time.Second // all the dials of one dialAny
	dialOneTimeout     = 8 * time.Second  // one dial of dialAny
	takeoverTimeout    = 8 * time.Second  // a Takeover with its retries
	takeoverRetryEvery = 200 * time.Millisecond
)

// hostResult is the outcome of a background host operation. s identifies the
// session instance it was started for, old the client it replaces.
type hostResult struct {
	kind      hostKind
	key       string
	s         *session
	gen       int            // hostWarm: the session's warmGen at the start
	old       *client.Client // the client the op was started for
	c         *client.Client // dialed or promoted; the adopting client for hostAdopt
	err       error
	mediaLost bool // AdoptCall failed and the fallback join could not retain publishers
}

// hostChan is the channel of hostResults, made on first use.
func (g *game) hostChan() chan hostResult {
	if g.hostCh == nil {
		g.hostCh = make(chan hostResult, 8)
	}
	return g.hostCh
}

// dialCreds is what a dial of s sends: the password (hosted servers only), the
// cached key, the pinned server ID and the lowest chain head it accepts.
func (g *game) dialCreds(s *session) (pass string, o client.DialOpts) {
	if e, ok := g.entryFor(s.key); ok {
		o.Pin = e.ServerID
		o.MinChainSeq = e.ChainSeq
		if k, err := hex.DecodeString(e.PasswordKey); err == nil && len(k) == 32 {
			o.PasswordKey = k // a corrupt cache is ignored; the password is used
		}
	}
	if s.key == hostedKey && g.hosted != nil {
		pass = g.hosted.cfg.Password
		o.PasswordKey = g.hosted.cfg.PasswordKey
		o.Pin = g.hosted.id
		o.MinChainSeq = 0 // the local host's own chain; dialAny raises it for a friend host
	}
	return pass, o
}

// dialTargets are the URLs to try for s, in order: the active host as last
// told, the saved URL, then the known hosts.
func (g *game) dialTargets(s *session) []string {
	activeOnly := s.activeOnly
	s.activeOnly = false
	if s.key == hostedKey {
		return []string{s.url}
	}
	var out []string
	add := func(us ...string) {
		for _, u := range us {
			if u != "" && !slices.Contains(out, u) {
				out = append(out, u)
			}
		}
	}
	add(s.hint...)
	add(s.url)
	if activeOnly { // the redial after active_alive must not reach the standby
		return out
	}
	if e, ok := g.entryFor(s.key); ok {
		add(e.Hosts...)
	}
	return out
}

// dialAny dials urls in order until one answers. A standby's not_active
// refusal puts the active host it names first in line, with no delay. On
// failure it returns the refusal a host answered with (a standby's not_active
// only if nothing better), else the last network error. Safe off the game
// goroutine.
func (g *game) dialAny(urls []string, name, pass string, o client.DialOpts) (*client.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialAnyTimeout)
	defer cancel()
	queue := slices.Clone(urls)
	tried, followed := map[string]bool{}, map[string]bool{}
	var refusal, last error
	for len(queue) > 0 && ctx.Err() == nil {
		u := queue[0]
		queue = queue[1:]
		if tried[u] {
			continue
		}
		tried[u] = true
		do := o
		if followed[u] && g.hosted != nil && o.Pin == g.hosted.id { // a friend host, not the local one
			do.MinChainSeq = max(do.MinChainSeq, g.hosted.chain.Head().Seq)
		}
		dctx, dcancel := context.WithTimeout(ctx, dialOneTimeout)
		c, err := client.DialWith(dctx, u, g.id, name, pass, do)
		dcancel()
		if err == nil {
			if g.onDialed != nil {
				g.onDialed(c)
			}
			return c, nil
		}
		last = err
		var se *client.ServerError
		if !errors.As(err, &se) {
			continue
		}
		var prev *client.ServerError
		if refusal == nil || (errors.As(refusal, &prev) && prev.Code == proto.CodeNotActive && se.Code != proto.CodeNotActive) {
			refusal = err
		}
		if se.Code == proto.CodeNotActive && se.Hosts != nil {
			for _, a := range se.Hosts.Active.Addrs {
				followed[a] = true
			}
			queue = append(slices.Clone(se.Hosts.Active.Addrs), queue...)
		}
	}
	if refusal != nil {
		return nil, refusal
	}
	return nil, last
}

// persist writes the saved server list; a failure goes to the notice line.
func (g *game) persist() {
	if g.serversPath == "" {
		return
	}
	if err := state.SaveServers(g.serversPath, g.servers); err != nil {
		g.notice = fmt.Sprintf("save servers: %v", err)
	}
}

// noteChainSeq records the chain head s's client has seen, so a later dial
// refuses a host with an older chain. It reports whether the saved seq rose;
// the caller saves.
func (g *game) noteChainSeq(s *session) bool {
	if s.c == nil || s.key == hostedKey {
		return false
	}
	e, ok := g.entryFor(s.key)
	if !ok {
		return false
	}
	seq := s.c.ChainSeq()
	if seq <= e.ChainSeq {
		return false
	}
	g.servers = state.BumpChainSeq(g.servers, s.key, seq)
	return true
}

// onHosts handles a hosts frame: it remembers the host addresses, names the
// host serving s, and follows a change of standby with a new warm session.
func (g *game) onHosts(s *session, h *proto.Hosts) {
	s.hint = slices.Clone(h.Active.Addrs)
	defer g.syncFriendHosting(s)
	if s.key != hostedKey {
		urls := append(slices.Clone(h.Active.Addrs), func() []string {
			if h.Standby != nil {
				return h.Standby.Addrs
			}
			return nil
		}()...)
		if e, ok := g.entryFor(s.key); ok {
			g.servers = state.SetHosts(g.servers, s.key, urls)
			if ne, _ := g.entryFor(s.key); !slices.Equal(e.Hosts, ne.Hosts) {
				g.persist()
			}
		}
	}
	var sb proto.HostInfo
	if h.Standby != nil {
		sb = *h.Standby
	}
	if sb.Fingerprint != s.standby.Fingerprint || !slices.Equal(sb.Addrs, s.standby.Addrs) {
		g.dropWarm(s)
		s.standby = sb
		s.warmFails = 0
		s.warmRetryAt = time.Time{}
	}
}

// dropWarm closes the warm session of s and abandons a warm dial in flight.
func (g *game) dropWarm(s *session) {
	if s.warm != nil {
		_ = s.warm.Close()
		s.warm = nil
	}
	s.warmGen++
	s.warming = false
}

// tickHosts runs the per-session host upkeep: it watches the warm sessions and
// starts the warm dials and warm peers that are due.
func (g *game) tickHosts(now time.Time) {
	for _, k := range g.sessionKeys() {
		s := g.sess[k]
		g.drainWarm(s)
		g.tickWarm(s, now)
	}
}

// drainWarm reads the warm session's events. Silence or a drop of the standby
// ends it; the next tick dials it again, after a backoff. A warm session that
// turns full was promoted by someone else: it is not ours to keep.
func (g *game) drainWarm(s *session) {
	if s.warm == nil || s.switching {
		return
	}
	for i := 0; i < maxEventsPerTick; i++ {
		select {
		case ev, ok := <-s.warm.Events():
			if !ok || ev.ActiveLost {
				g.logf("warm session to %s lost", s.standby.Name)
				g.dropWarm(s)
				s.warmFails++
				s.warmRetryAt = time.Now().Add(state.JitteredBackoff(s.warmFails, nil))
				return
			}
			if !s.warm.Warm() { // promoted by someone else: it is the active host now
				if s.c != nil {
					g.failover(s) // Takeover on a full client returns at once; install moves the call
					return
				}
				g.dropWarm(s)
				s.warmFails++
				s.warmRetryAt = time.Now().Add(state.JitteredBackoff(s.warmFails, nil))
				return
			}
		default:
			return
		}
	}
}

// tickWarm dials the standby when s has none warm, and opens the warm voice
// peer once a call is on s.
func (g *game) tickWarm(s *session, now time.Time) {
	if s.c == nil || s.switching {
		return
	}
	if s.warm == nil {
		if !s.warming && s.standby.Fingerprint != "" && len(s.standby.Addrs) > 0 && !now.Before(s.warmRetryAt) {
			g.dialWarm(s)
		}
		return
	}
	if !s.peerTried && s.warm.Warm() && g.callKey == s.key && s.st.InCall != "" {
		s.peerTried = true
		w := s.warm
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := w.OpenWarmPeer(ctx); err != nil {
				g.logf("warm voice peer: %v", err) // AdoptCall falls back to a plain join
			}
		}()
	}
}

// dialWarm dials the standby in the background and tells the active host
// whether it was reachable.
func (g *game) dialWarm(s *session) {
	s.warming = true
	s.warmGen++
	pass, o := g.dialCreds(s)
	o.Warm = true
	stand, act, gen, name, hc := s.standby, s.c, s.warmGen, s.name, g.hostChan()
	go func(s *session, key string) { // reads no session field
		c, err := g.dialAny(stand.Addrs, name, pass, o)
		_ = act.ReportReach(stand.Fingerprint, err == nil)
		hc <- hostResult{kind: hostWarm, key: key, s: s, gen: gen, old: act, c: c, err: err}
	}(s, s.key)
}

// applyHost applies a finished host operation.
func (g *game) applyHost(r hostResult) {
	s := g.sess[r.key]
	live := s != nil && s == r.s
	switch r.kind {
	case hostAdopt:
		g.applyAdopt(r, s, live)
	case hostWarm:
		g.applyWarm(r, s, live)
	case hostTakeover:
		if !live || !s.switching || s.c != r.old || s.warm != r.c {
			if r.c != nil {
				g.closeClient(r.c)
			}
			return
		}
		s.switching = false
		s.warm = nil
		if r.err == nil {
			g.logf("took over on %s", s.standby.Name)
			s.standby = proto.HostInfo{}
			s.peerTried = false
			g.install(s, r.c, r.old)
			return
		}
		g.logf("takeover on %s failed: %v", s.url, r.err)
		_ = r.c.Close()
		_ = r.old.Close()
		g.connLost(s)
	case hostAlive:
		if live && s.switching && s.c == r.old && !s.dialing {
			g.logf("active host of %s may be alive; redialing", s.url)
			s.attempt = 0
			s.activeOnly = true
			g.dial(s) // applyDial ends the takeover if this wins
		}
	case hostMove:
		if !live || !s.switching || s.c != r.old {
			if r.c != nil {
				g.closeClient(r.c)
			}
			return
		}
		s.switching = false
		if r.err != nil {
			g.logf("move on %s failed: %v", s.url, r.err)
			_ = r.old.Close()
			g.connLost(s)
			return
		}
		g.logf("moved to the host %s", r.c.ServerName())
		s.standby = proto.HostInfo{}
		g.install(s, r.c, r.old)
	}
}

func (g *game) applyWarm(r hostResult, s *session, live bool) {
	if !live || r.gen != s.warmGen {
		if r.c != nil {
			g.closeClient(r.c)
		}
		return
	}
	s.warming = false
	switch {
	case r.err != nil:
		g.logf("warm dial to %s failed: %v", s.standby.Name, r.err)
		s.warmFails++
		s.warmRetryAt = time.Now().Add(state.JitteredBackoff(s.warmFails, nil))
	case s.c != r.old || s.switching:
		g.closeClient(r.c)
	default:
		s.warm = r.c
		s.warmFails = 0
		s.peerTried = false
		g.logf("warm session to %s", s.standby.Name)
	}
}

// applyAdopt ends the busy mark of a call move and reports its failure.
func (g *game) applyAdopt(r hostResult, s *session, live bool) {
	g.vc.busy = false
	g.vc.opKey = ""
	if !live || s.c != r.c {
		return
	}
	if r.err != nil {
		g.logf("call did not survive the host change: %v", r.err)
		g.dropVoice()
		s.st.Status = "voice call ended"
		return
	}
	if r.mediaLost {
		g.stopCamera()
		g.stopShare()
		s.st.Status = "camera or screen failed to resume after host failover"
	} else {
		if g.camera.on {
			g.camera.c = r.c
		}
		if g.share.on {
			g.share.c = r.c
		}
	}
	g.logf("call moved to %s", s.st.HostedBy)
}

// failover asks the warm session to take over, off the game goroutine. The
// result arrives as hostTakeover; until then the session keeps its dead client
// and shows no change.
func (g *game) failover(s *session) {
	g.logf("host of %s lost; taking over on the standby", s.url)
	g.interruptChats(s, s.c, "host lost before confirmation")
	s.switching = true
	old, w, hc := s.c, s.warm, g.hostChan()
	retryFor := takeoverRetryFor // read once, here
	go func(s *session, key string) {
		ctx, cancel := context.WithTimeout(context.Background(), takeoverTimeout)
		defer cancel()
		deadline := time.Now().Add(retryFor)
		told := false
		for {
			err := w.Takeover(ctx)
			var se *client.ServerError
			if !errors.As(err, &se) || se.Code != proto.CodeActiveAlive || time.Now().After(deadline) {
				hc <- hostResult{kind: hostTakeover, key: key, s: s, old: old, c: w, err: err}
				return
			}
			if !told { // the active host may only have dropped us: try it at once
				told = true
				hc <- hostResult{kind: hostAlive, key: key, s: s, old: old}
			}
			select {
			case <-time.After(takeoverRetryEvery):
			case <-ctx.Done():
			}
		}
	}(s, s.key)
}

// onMove follows a host's move to the host it names with a new full client.
func (g *game) onMove(s *session, m *proto.Move) {
	if s.switching || len(m.To.Addrs) == 0 {
		return
	}
	g.logf("host asks %s to move to %s", s.url, m.To.Name)
	g.interruptChats(s, s.c, "host move before confirmation")
	s.switching = true
	g.dropWarm(s)
	pass, o := g.dialCreds(s)
	old, name, hc := s.c, s.name, g.hostChan()
	go func(s *session, key string) {
		c, err := g.dialAny(m.To.Addrs, name, pass, o)
		hc <- hostResult{kind: hostMove, key: key, s: s, old: old, c: c, err: err}
	}(s, s.key)
}

// connLost marks s disconnected after its connection closed and schedules the
// reconnect.
func (g *game) connLost(s *session) {
	g.logf("connection to %s lost", s.url)
	g.dropWarm(s)
	s.st.Apply(client.Event{}, false)
	g.interruptChats(s, s.c, "connection lost before confirmation")
	g.endVoiceFor(s.key) // only s's call; other sessions' voice is untouched
	s.c = nil
	s.scheduleRetry(time.Now())
}

// install makes nu the client of s in place of old, which is closed: the text
// channels are joined again and the call, if there is one, moves across.
func (g *game) install(s *session, nu, old *client.Client) {
	s.c = nu
	s.dialing = false
	s.dialGen++ // a redial of the active host still in flight is now stale
	nu.SetVideoSink(&g.vsink)
	nu.SetMediaSink(&g.mframes)
	s.ms.joined, s.ms.confirmed = map[string]bool{}, map[string]bool{}
	s.st.Phase = state.PhaseMain
	s.st.ConnectErr = ""
	s.failed, s.lastErr = false, ""
	s.attempt = 0
	s.retryAt = time.Time{}
	s.st.HostedBy = "" // the new host's hosts frame names it
	s.restoreSendNotice()
	if !g.carryCall(s, nu, old) {
		_ = old.Close()
	}
	ch := ""
	if n := len(s.st.Channels); n > 0 && s.st.Active >= 0 && s.st.Active < n {
		ch = s.st.Channels[s.st.Active]
		g.joinChannel(s, ch) // the shown channel first
		for _, c := range s.st.Channels {
			g.joinChannel(s, c)
		}
	}
	g.noteChainSeq(s)
	g.saveServer(s, ch) // one write for the seq and the touch
}

// carryCall moves the call of s from old to nu in the background and closes
// old after. It reports false, doing nothing, when s has no call to move.
func (g *game) carryCall(s *session, nu, old *client.Client) bool {
	if g.vc.busy && g.vc.opKey == s.key { // an op on the old client: its result will be stale
		g.endVoiceFor(s.key)
		return false
	}
	if g.callKey != s.key || s.st.InCall == "" {
		return false
	}
	ch, muted := s.st.InCall, g.vc.muted
	src, _ := g.vc.src.(client.AudioSource)
	sink, _ := g.vc.sink.(client.AudioSink)
	g.vc.busy, g.vc.opKey = true, s.key
	hc := g.hostChan()
	go func(s *session, key string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := nu.AdoptCall(ctx, old)
		mediaLost := err != nil
		if mediaLost && src != nil && sink != nil { // an ordinary join instead
			g.logf("adopt call: %v; joining %s again", err, ch)
			if err = nu.JoinVoice(ctx, ch, src, sink); err == nil && muted {
				_ = nu.SetMuted(true)
			}
		}
		_ = old.Close()
		hc <- hostResult{kind: hostAdopt, key: key, s: s, c: nu, err: err, mediaLost: mediaLost}
	}(s, s.key)
	return true
}
