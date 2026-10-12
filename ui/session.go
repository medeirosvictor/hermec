package ui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/medeirosvictor/hermec/client"
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
	c   *client.Client
	err error
}

// joinResult is the outcome of a background Join.
type joinResult struct {
	key, channel string
	err          error
}

// sendResult is the outcome of a background chat send.
type sendResult struct {
	key string
	err error
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

// openSession returns the session for url, creating it if needed, and dials
// it unless it is connected or dialing. It returns nil when url is the saved
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
	if g.sess == nil {
		g.sess = map[string]*session{}
	}
	s := g.sess[key]
	if s == nil {
		s = newSession(key, real, name)
		g.sess[key] = s
	}
	if s.c == nil && !s.dialing {
		s.name = name
		g.flushName()
		g.rememberName(name)
		g.dial(s)
	}
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
	s.st.ConnectErr = ""
	g.logf("dialing %s as %q", s.url, s.name)
	go func(s *session, key, url, name string) { // reads no session field
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := client.Dial(ctx, url, g.id, name, "")
		g.dialCh <- dialResult{key: key, s: s, c: c, err: err}
	}(s, s.key, s.url, s.name)
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
	s.retryAt = now.Add(state.NextBackoff(s.attempt))
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

// retryNow redials the disconnected session at key immediately and resets
// its backoff.
func (g *game) retryNow(key string) {
	s := g.sess[key]
	if s == nil || s.dialing || s.c != nil || s.url == "" {
		return
	}
	s.attempt = 0
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
	if s.c != nil {
		_ = s.c.Close()
		s.c = nil
	}
	s.st.SetViewed(false)
	delete(g.sess, key) // a pending dial result no longer matches any session
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
	g.cur().st.SetViewed(key != "")
}

// pumpSessions applies finished dials, joins and sends, then drains every
// session's events (sorted by key, capped per session so none starves).
func (g *game) pumpSessions() {
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
}

func (g *game) applyDial(r dialResult) {
	s := g.sess[r.key]
	if s == nil || s != r.s || !s.dialing || s.c != nil { // stale: the session is gone, replaced or moved on
		if r.c != nil {
			g.closeClient(r.c)
		}
		return
	}
	s.dialing = false
	if r.err != nil {
		g.logf("dial %s failed: %v", s.url, r.err)
		if !s.everUp {
			delete(g.sess, s.key)
			if g.curKey == s.key {
				g.view("")
			}
			g.noneSession().st.ConnectErr = r.err.Error()
			return
		}
		s.st.ConnectErr = r.err.Error() // stays PhaseDisconnected
		s.scheduleRetry(time.Now())
		return
	}
	g.logf("connected to %s, channels %v", s.url, r.c.Channels())
	s.c = r.c
	s.st.SetConnected(r.c.Channels(), r.c.Fingerprint())
	s.ms = newMainScene()
	s.everUp = true
	s.attempt = 0
	s.retryAt = time.Time{}
	g.onConnected(s)
}

func (g *game) applyJoin(r joinResult) {
	s := g.sess[r.key]
	if s == nil {
		return
	}
	if r.err != nil {
		delete(s.ms.joined, r.channel) // allow retry on next switch
		s.st.Status = fmt.Sprintf("join %s failed: %v", r.channel, r.err)
		return
	}
	g.logf("joined %s on %s", r.channel, s.url)
}

// drainEvents applies up to maxEventsPerTick events of s. A closed stream
// marks s disconnected.
func (g *game) drainEvents(s *session) {
	if s.c == nil {
		return
	}
	for i := 0; i < maxEventsPerTick; i++ {
		select {
		case ev, ok := <-s.c.Events():
			if !ok {
				g.logf("connection to %s lost", s.url)
				s.st.Apply(ev, false)
				g.endVoiceFor(s.key) // only s's call; other sessions' voice is untouched
				s.c = nil
				s.scheduleRetry(time.Now())
				return
			}
			s.st.Apply(ev, true)
		default:
			return
		}
	}
}
