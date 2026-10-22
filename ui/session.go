package ui

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

// session is one server connection with its own state and chat view. The
// game keeps one per server in g.sess; switching rail tiles only changes
// which one is viewed.
type session struct {
	key, url, name string         // saved-list key (localKey for -local), real dial URL, display name
	c              *client.Client // nil while dialing or disconnected
	st             *state.State
	ms             mainScene
	dialing        bool
	everUp         bool      // connected at least once
	attempt        int       // reconnect attempts since last success (Task 5)
	retryAt        time.Time // zero = no retry scheduled (Task 5)
	failed         bool      // the last dial failed; cleared by a retry or a success
	dialGen        int       // generation of the latest dial; older results are stale
	lastErr        string    // that dial's error, for the tooltip and the failed pane
	activeOnly     bool      // the next dial tries only the active host (after active_alive)

	// Host failover (hosts.go). The host serving the session is st.HostedBy.
	warm        *client.Client // warm session to the standby; nil = none
	standby     proto.HostInfo // the standby as last announced; zero = none
	hint        []string       // the active host's addresses as last announced
	switching   bool           // a failover or move is in flight: c is dead or leaving
	warming     bool           // a warm dial is in flight
	warmGen     int            // generation of the latest warm dial; older results are stale
	warmFails   int            // warm dial or session failures in a row, for the backoff
	warmRetryAt time.Time      // no warm dial before this
	peerTried   bool           // the warm voice peer was opened for this warm session
}

func newSession(key, url, name string) *session {
	return &session{key: key, url: url, name: name, st: state.New(), ms: newMainScene()}
}

// dialResult is the outcome of a background dial. s identifies the session
// instance that started it: a result is only adopted if g.sess[key] is still
// that instance, so a disconnect+reopen of the same key cannot adopt it.
type dialResult struct {
	key string
	s   *session
	gen int // the dial generation that produced it
	c   *client.Client
	err error
}

// joinResult is the outcome of a background Join.
type joinResult struct {
	key, channel string
	c            *client.Client // the client that joined; nil skips the staleness check
	err          error
}

// sendResult is the outcome of a background chat send.
type sendResult struct {
	key string
	err error
	// older is set for a history_before request: the channel whose Loading
	// mark an error must clear.
	older string
}

// cur returns the viewed session, or g.none (a PhaseConnect placeholder that
// backs the connect scene). Never nil.
func (g *game) cur() *session {
	if s, ok := g.sess[g.curKey]; ok {
		return s
	}
	return g.noneSession()
}

// sessionKeys returns the session keys in sorted order.
func (g *game) sessionKeys() []string {
	ks := make([]string, 0, len(g.sess))
	for k := range g.sess {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// openSession returns the session for url. A new one is dialed and, when url
// is not saved, gets a pending rail entry at once; an existing one is returned
// as is (a retry goes through retryNow). It returns nil when url is the saved
// local server and this run has none; the error goes to the connect scene.
func (g *game) openSession(url, name string) *session {
	none := g.noneSession()
	none.st.ConnectErr = ""
	real := g.realURL(url)
	if real == localKey {
		none.st.ConnectErr = "local server not running (start with -local)"
		return nil
	}
	key := g.keyFor(real)
	if key == hostedKey && g.hosted == nil {
		none.st.ConnectErr = "not hosting a server"
		return nil
	}
	if g.sess == nil {
		g.sess = map[string]*session{}
	}
	if s := g.sess[key]; s != nil {
		if s.c == nil && !s.dialing { // an idle one still takes the name typed now
			s.name = name
			g.flushName()
			g.rememberName(name)
		}
		return s
	}
	s := newSession(key, real, name)
	g.sess[key] = s
	if _, ok := g.entryFor(key); !ok { // the tile shows now; the file waits for a first success
		g.servers = append([]state.ServerEntry{{URL: key, Label: g.labelFor(key, real), Pending: true}}, g.servers...)
	}
	g.flushName()
	g.rememberName(name)
	if key == hostedKey && g.hosted.srv == nil { // not running: failed from the start, nothing to dial
		s.failed, s.lastErr = true, g.hosted.err
		return s
	}
	g.dial(s)
	return s
}

// noneSession is g.none, created on first use.
func (g *game) noneSession() *session {
	if g.none == nil {
		g.none = newSession("", "", "")
	}
	return g.none
}

// dial dials s in a goroutine; the result arrives on dialCh.
func (g *game) dial(s *session) {
	s.dialing = true
	s.dialGen++
	s.st.ConnectErr = ""
	g.logf("dialing %s as %q", s.url, s.name)
	pass, o := g.dialCreds(s)
	urls := g.dialTargets(s)
	go func(s *session, key, name, pass string, o client.DialOpts, gen int) { // reads no session field
		c, err := g.dialAny(urls, name, pass, o)
		g.dialCh <- dialResult{key: key, s: s, gen: gen, c: c, err: err}
	}(s, s.key, s.name, pass, o, s.dialGen)
}

// closeClient closes a client no session owns (a stale dial result).
func (g *game) closeClient(c *client.Client) {
	if g.closeStale != nil {
		g.closeStale(c)
		return
	}
	_ = c.Close()
}

// scheduleRetry arms the next background reconnect of s.
func (s *session) scheduleRetry(now time.Time) {
	s.retryAt = now.Add(state.JitteredBackoff(s.attempt, nil))
	s.attempt++
}

// tickReconnect dials every disconnected session whose retry time has come.
func (g *game) tickReconnect(now time.Time) {
	for _, k := range g.sessionKeys() {
		s := g.sess[k]
		if !s.dialing && s.c == nil && !s.retryAt.IsZero() && !now.Before(s.retryAt) {
			g.dial(s)
		}
	}
}

// retryNow redials the disconnected session at key immediately, resets its
// backoff and clears its failed mark.
func (g *game) retryNow(key string) {
	s := g.sess[key]
	if s == nil && key == hostedKey {
		g.startHostedNow()
		return
	}
	if s == nil || s.dialing || s.c != nil || s.url == "" {
		return
	}
	if key == hostedKey && g.hosted != nil && g.hosted.srv == nil { // stopped: start it first
		if g.startHosted() != nil {
			s.failed, s.lastErr = true, g.hosted.err
			return
		}
		s.url = g.hosted.url
	}
	s.attempt = 0
	s.failed = false
	g.dial(s)
}

// disconnect closes and forgets the session at key, ending its call whether
// or not it is viewed. If it was viewed, the view falls back to the top-most
// rail server that still has a session, or the connect scene.
func (g *game) disconnect(key string) {
	s := g.sess[key]
	if s == nil {
		return
	}
	viewed := g.curKey == key
	g.endVoiceFor(key) // closing the client ends its call server-side
	g.dropWarm(s)
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
	s.st.SetViewed(false)
	delete(g.sess, key) // a pending dial result no longer matches any session
	for _, e := range g.servers {
		if e.URL == key && e.Pending { // never saved: the tile goes with its session
			g.servers = state.Remove(g.servers, key)
			break
		}
	}
	if !viewed {
		return
	}
	next := ""
	for _, e := range g.servers {
		if _, ok := g.sess[e.URL]; ok {
			next = e.URL
			break
		}
	}
	g.curKey = ""
	g.view(next)
}

// view shows the session at key; "" (or a key with no session) shows the
// connect scene.
func (g *game) view(key string) {
	g.cur().st.SetViewed(false)
	if _, ok := g.sess[key]; !ok {
		key = ""
	}
	g.curKey = key
	if key != "" {
		g.add = addChoose // the next visit to the connect scene starts at the choices
	}
	g.cur().st.SetViewed(key != "")
}

// pumpSessions applies finished dials, joins and sends, then drains every
// session's events (sorted by key, capped per session so none starves).
func (g *game) pumpSessions() {
	for {
		select {
		case r := <-g.hostCh: // nil until a host operation starts
			g.applyHost(r)
			continue
		default:
		}
		break
	}
	for {
		select {
		case r := <-g.dialCh:
			g.applyDial(r)
			continue
		default:
		}
		break
	}
	for {
		select {
		case r := <-g.joinCh:
			g.applyJoin(r)
			continue
		default:
		}
		break
	}
	for {
		select {
		case r := <-g.sendCh:
			if s := g.sess[r.key]; s != nil && r.err != nil {
				if r.older != "" {
					delete(s.st.Loading, r.older)
					continue
				}
				s.st.Status = fmt.Sprintf("send failed: %v", r.err)
			}
			continue
		default:
		}
		break
	}
	for _, k := range g.sessionKeys() {
		g.drainEvents(g.sess[k])
	}
	g.tickHosts(time.Now())
}

// noRetryCode reports whether a server error code means retrying cannot help.
func noRetryCode(code string) bool {
	switch code {
	case proto.CodeAuthFailed, proto.CodeServerIDMismatch, proto.CodeHostNotAuthorized,
		proto.CodeBadChain, proto.CodeServerOutdated, proto.CodeUpdateRequired, proto.CodeBanned:
		return true
	}
	return false
}

// dropCachedKey forgets the cached password key of s after the server refused
// it, so a changed server password can be used on the next dial.
func (g *game) dropCachedKey(s *session, err error) {
	var se *client.ServerError
	if s.key == hostedKey || !errors.As(err, &se) || se.Code != proto.CodeAuthFailed {
		return
	}
	for i := range g.servers {
		if g.servers[i].URL == s.key && g.servers[i].PasswordKey != "" {
			g.servers[i].PasswordKey = ""
			if err := state.SaveServers(g.serversPath, g.servers); err != nil {
				g.notice = fmt.Sprintf("save servers: %v", err)
			}
		}
	}
}

func (g *game) applyDial(r dialResult) {
	s := g.sess[r.key]
	if s == nil || s != r.s || r.gen != s.dialGen || !s.dialing || (s.c != nil && !s.switching) { // stale: the session is gone, replaced, redialed or moved on
		if r.c != nil {
			g.closeClient(r.c)
		}
		return
	}
	s.dialing = false
	if r.err != nil {
		g.logf("dial %s failed: %v", s.url, r.err)
		g.dropCachedKey(s, r.err)
		if s.switching { // the standby's takeover is still pending and decides
			return
		}
		s.failed, s.lastErr = true, state.DialErrorText(r.err)
		if !s.everUp {
			return // keeps its tile and session; the user retries with the badge
		}
		s.st.ConnectErr = s.lastErr // stays PhaseDisconnected
		var se *client.ServerError
		if errors.As(r.err, &se) && noRetryCode(se.Code) {
			s.retryAt = time.Time{} // retrying cannot help; the tile's ↻ still can
			return
		}
		s.scheduleRetry(time.Now())
		return
	}
	g.logf("connected to %s, channels %v", s.url, r.c.Channels())
	carried := ""
	if s.switching { // the active host answered before the standby could take over
		s.switching = false
		g.dropWarm(s)
		if old := s.c; old != nil {
			// Keep the call: it rejoins on the host that answered, same mute.
			g.endOldMedia(s, old)
			s.c = r.c
			ch := s.st.InCall
			if g.carryCall(s, r.c, old) {
				carried = ch
			} else {
				_ = old.Close()
			}
		}
	}
	s.c = r.c
	s.c.SetVideoSink(&g.vsink)
	s.st.SetConnected(r.c.Channels(), r.c.Fingerprint())
	s.ms = newMainScene()
	s.everUp = true
	s.failed, s.lastErr = false, ""
	s.attempt = 0
	s.retryAt = time.Time{}
	g.noteChainSeq(s) // onConnected saves
	g.onConnected(s)
	if carried != "" {
		s.st.SetInCall(carried) // SetConnected cleared it; the call moves with the dial
	}
}

func (g *game) applyJoin(r joinResult) {
	s := g.sess[r.key]
	if s == nil || (r.c != nil && r.c != s.c) {
		return // gone, or answered for a connection since replaced
	}
	if r.err != nil {
		delete(s.ms.joined, r.channel) // allow retry on next switch
		delete(s.ms.confirmed, r.channel)
		s.st.Status = fmt.Sprintf("join %s failed: %v", r.channel, r.err)
		return
	}
	s.ms.confirmed[r.channel] = true
	g.logf("joined %s on %s", r.channel, s.url)
}

// noteEvent reacts to client events beyond what state.Apply records. A host
// that cannot carry screen share has already ended our share in the client, so
// the button must go back to "share".
func (g *game) noteEvent(s *session, ev client.Event) {
	if errors.Is(ev.Err, client.ErrShareUnsupported) && g.share.on && g.share.c == s.c {
		g.stopShare()
	}
	// A refusal that reaches us for the watch still in force (it arrived after
	// Watch gave up waiting) ends the view; a timely refusal is handled by the
	// media worker, which sees it as Watch's error.
	var se *client.ServerError
	if errors.As(ev.Err, &se) && se.Code == proto.CodeNotSharing && se.Gen != 0 &&
		s.c != nil && se.Gen == s.c.WatchGen() && g.watch.fp != "" && g.callSess() == s {
		g.stopWatching()
	}
}

// drainEvents applies up to maxEventsPerTick events of s. A closed stream
// marks s disconnected.
func (g *game) drainEvents(s *session) {
	if s.c == nil || s.switching { // a failover or move in flight owns the dead client
		return
	}
	for i := 0; i < maxEventsPerTick; i++ {
		select {
		case ev, ok := <-s.c.Events():
			if !ok || (ev.ActiveLost && s.warm != nil) {
				if s.warm != nil { // the standby takes over: no reconnect screen
					g.failover(s)
					return
				}
				g.connLost(s)
				return
			}
			s.st.Apply(ev, true)
			g.noteEvent(s, ev)
			switch {
			case ev.Hosts != nil:
				g.onHosts(s, ev.Hosts)
			case ev.Move != nil:
				g.onMove(s, ev.Move)
				if s.switching {
					return
				}
			case ev.Chain != nil:
				g.bumpChainSeq(s)
			}
		default:
			return
		}
	}
}
