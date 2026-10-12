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

type dialResult struct {
	key string
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
	g.flushName()
	g.rememberName(s.name)
	g.logf("dialing %s as %q", s.url, s.name)
	go func(key, url, name string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := client.Dial(ctx, url, g.id, name, "")
		g.dialCh <- dialResult{key, c, err}
	}(s.key, s.url, s.name)
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
	if s == nil || !s.dialing || s.c != nil { // stale: the session is gone or moved on
		if r.c != nil {
			_ = r.c.Close()
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
		return
	}
	g.logf("connected to %s, channels %v", s.url, r.c.Channels())
	s.c = r.c
	s.st.SetConnected(r.c.Channels(), r.c.Fingerprint())
	s.ms = newMainScene()
	s.everUp = true
	s.attempt = 0
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
				if g.cur() == s {
					g.dropVoice() // Task 6 makes this "the call session"
				}
				s.c = nil
				return
			}
			s.st.Apply(ev, true)
		default:
			return
		}
	}
}
