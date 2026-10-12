package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// startServer runs an in-process server configured like -local and returns
// it with its ws:// URL. It is shut down when the test ends.
func startServer(t *testing.T, addr string) (*server.Server, string) {
	t.Helper()
	srv := server.New(server.Config{
		Addr:          addr,
		Discoverable:  false,
		ServerName:    "test",
		Channels:      []string{"general", "random"},
		VoiceChannels: []string{"voice"},
		Roles:         roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, "ws://" + srv.Addr() + "/"
}

// newTestGame builds a windowless game with the network channels Run makes.
// Every session's client is closed when the test ends.
func newTestGame(t *testing.T) *game {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	g := &game{
		opts: Options{Name: "tester"}, id: id, fp: identity.Fingerprint(id.PublicKey()),
		serversPath: filepath.Join(t.TempDir(), "servers.toml"),
		sess:        map[string]*session{}, none: newSession("", "", ""),
		dialCh: make(chan dialResult, 8), joinCh: make(chan joinResult, 8), sendCh: make(chan sendResult, 8), voiceCh: make(chan voiceResult, 4),
	}
	t.Cleanup(func() {
		for _, s := range g.sess {
			if s.c != nil {
				_ = s.c.Close()
			}
		}
	})
	return g
}

// pumpUntil calls pumpSessions every 10ms until cond holds; fails after 5s.
func pumpUntil(t *testing.T, g *game, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		g.pumpSessions()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func connected(s *session) func() bool {
	return func() bool { return s.c != nil && s.st.Phase == state.PhaseMain }
}

// joinedAll reports whether s holds the presence of every text channel, i.e.
// the server has acknowledged each join.
func joinedAll(s *session) func() bool {
	return func() bool {
		if !connected(s)() {
			return false
		}
		for _, ch := range s.st.Channels {
			if len(s.st.Members[ch]) == 0 {
				return false
			}
		}
		return true
	}
}

func TestTwoSessionsStayConnected(t *testing.T) {
	g := newTestGame(t)
	_, urlA := startServer(t, "127.0.0.1:0")
	_, urlB := startServer(t, "127.0.0.1:0")

	a := g.openSession(urlA, "me")
	g.view(a.key)
	pumpUntil(t, g, connected(a))
	b := g.openSession(urlB, "me")
	g.view(b.key)
	pumpUntil(t, g, connected(b))

	if g.cur() != b {
		t.Fatal("B not viewed")
	}
	if a.c == nil || a.st.Phase != state.PhaseMain {
		t.Fatalf("A dropped: c=%v phase=%v", a.c, a.st.Phase)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.c.SendChat(ctx, "general", "still here"); err != nil {
		t.Fatalf("send on A: %v", err)
	}
}

func TestJoinAllTextChannels(t *testing.T) {
	g := newTestGame(t)
	_, url := startServer(t, "127.0.0.1:0")
	s := g.openSession(url, "me")
	g.view(s.key)
	pumpUntil(t, g, joinedAll(s))
	for _, ch := range []string{"general", "random"} {
		if !s.ms.joined[ch] {
			t.Errorf("%s not joined: %v", ch, s.ms.joined)
		}
	}
}

func TestBackgroundChatMarksUnread(t *testing.T) {
	g := newTestGame(t)
	_, urlA := startServer(t, "127.0.0.1:0")
	_, urlB := startServer(t, "127.0.0.1:0")
	a := g.openSession(urlA, "me")
	b := g.openSession(urlB, "me")
	g.view(a.key)
	pumpUntil(t, g, joinedAll(a))
	pumpUntil(t, g, joinedAll(b))

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	other, err := client.Dial(ctx, urlB, id, "other", "")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Join(ctx, "general"); err != nil {
		t.Fatal(err)
	}
	if err := other.SendChat(ctx, "general", "hello B"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(t, g, b.st.AnyUnread)
	if !b.st.Unread["general"] {
		t.Errorf("B unread = %v, want general", b.st.Unread)
	}
	if a.st.AnyUnread() {
		t.Errorf("A unread = %v, want none", a.st.Unread)
	}
}

func TestRailClickExistingSessionDoesNotRedial(t *testing.T) {
	g := newTestGame(t)
	_, urlA := startServer(t, "127.0.0.1:0")
	_, urlB := startServer(t, "127.0.0.1:0")
	a := g.openSession(urlA, "me")
	g.view(a.key)
	pumpUntil(t, g, connected(a))
	b := g.openSession(urlB, "me")
	g.view(b.key)
	pumpUntil(t, g, connected(b))

	var entry state.ServerEntry
	for _, e := range g.servers {
		if e.URL == a.key {
			entry = e
		}
	}
	if entry.URL == "" {
		t.Fatalf("A not saved: %+v", g.servers)
	}
	ca := a.c
	g.switchServer(entry)
	if g.curKey != a.key || g.cur() != a {
		t.Fatalf("curKey = %q, want %q", g.curKey, a.key)
	}
	if a.c != ca || a.dialing || len(g.sess) != 2 {
		t.Fatalf("A redialed: same client %v dialing %v sessions %d", a.c == ca, a.dialing, len(g.sess))
	}
}

func TestOpenSessionLocalURLMapsToLocalKey(t *testing.T) {
	g := newTestGame(t)
	_, url := startServer(t, "127.0.0.1:0")
	g.localURL = url
	s := g.openSession(g.localURL, "me")
	if s == nil || s.key != localKey || s.url != url {
		t.Fatalf("session = %+v, want key %q url %q", s, localKey, url)
	}
	g.view(s.key)
	pumpUntil(t, g, connected(s))
	g.switchServer(state.ServerEntry{URL: localKey})
	if len(g.sess) != 1 || g.cur() != s {
		t.Fatalf("rail entry made a second session: %d sessions", len(g.sess))
	}
	if again := g.openSession(localKey, "me"); again != s {
		t.Fatal("openSession(localKey) did not reuse the session")
	}
}

func TestFirstDialFailureDropsSession(t *testing.T) {
	g := newTestGame(t)
	const url = "ws://127.0.0.1:1/"
	s := g.openSession(url, "me")
	if s == nil {
		t.Fatal("no session")
	}
	g.view(s.key)
	pumpUntil(t, g, func() bool { return !s.dialing })
	if _, ok := g.sess[url]; ok {
		t.Fatal("failed first dial left a session")
	}
	if g.curKey != "" {
		t.Fatalf("curKey = %q, want connect scene", g.curKey)
	}
	if g.none.st.ConnectErr == "" {
		t.Fatal("want ConnectErr on the connect scene")
	}
}

// sessionLost reports whether s is disconnected with no client.
func sessionLost(s *session) func() bool {
	return func() bool { return s.c == nil && !s.dialing && s.st.Phase == state.PhaseDisconnected }
}

func TestDisconnectViewedFallsBack(t *testing.T) {
	g := newTestGame(t)
	_, urlA := startServer(t, "127.0.0.1:0")
	_, urlB := startServer(t, "127.0.0.1:0")
	a := g.openSession(urlA, "me")
	g.view(a.key)
	pumpUntil(t, g, connected(a))
	b := g.openSession(urlB, "me")
	g.view(b.key)
	pumpUntil(t, g, connected(b))
	g.view(a.key)

	ca := a.c
	g.disconnect(a.key)
	if _, ok := g.sess[a.key]; ok {
		t.Fatal("A session still present")
	}
	closedCh := make(chan struct{})
	go func() {
		for range ca.Events() { // returns once the stream is closed
		}
		close(closedCh)
	}()
	select {
	case <-closedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("A's client not closed")
	}
	if g.curKey != b.key {
		t.Fatalf("curKey = %q, want %q", g.curKey, b.key)
	}
	if !b.st.Viewed {
		t.Error("B not marked viewed")
	}
	g.disconnect(b.key)
	if g.curKey != "" || len(g.sess) != 0 {
		t.Fatalf("curKey = %q sessions %d, want connect scene and none", g.curKey, len(g.sess))
	}
	g.disconnect("nope") // unknown key is a no-op
}

func TestDisconnectDuringDialClosesLateClient(t *testing.T) {
	g := newTestGame(t)
	var closed []*client.Client
	g.closeStale = func(c *client.Client) { closed = append(closed, c); _ = c.Close() }
	_, url := startServer(t, "127.0.0.1:0")

	s := g.openSession(url, "me")
	g.view(s.key)
	g.disconnect(s.key)
	pumpUntil(t, g, func() bool { return len(closed) == 1 })
	if len(g.sess) != 0 || g.curKey != "" {
		t.Fatalf("late dial resurrected a session: %d sessions, curKey %q", len(g.sess), g.curKey)
	}
}

func TestDisconnectThenReopenIgnoresOldDial(t *testing.T) {
	g := newTestGame(t)
	var closed []*client.Client
	g.closeStale = func(c *client.Client) { closed = append(closed, c); _ = c.Close() }
	_, url := startServer(t, "127.0.0.1:0")

	old := g.openSession(url, "me")
	g.disconnect(old.key)
	fresh := g.openSession(url, "me")
	if fresh == old {
		t.Fatal("reopen reused the disconnected session")
	}
	g.view(fresh.key)

	// Collect both dial results, then apply the old one first: a key-only
	// guard would let the new session adopt it.
	var oldRes, newRes dialResult
	for i := 0; i < 2; i++ {
		select {
		case r := <-g.dialCh:
			if r.s == old {
				oldRes = r
			} else {
				newRes = r
			}
		case <-time.After(5 * time.Second):
			t.Fatal("dial result missing")
		}
	}
	if oldRes.c == nil || newRes.c == nil {
		t.Fatalf("dials failed: %v / %v", oldRes.err, newRes.err)
	}
	g.applyDial(oldRes)
	if fresh.c != nil || !fresh.dialing {
		t.Fatal("new session adopted the old dial's client")
	}
	if len(closed) != 1 || closed[0] != oldRes.c {
		t.Fatalf("old client not closed: %v", closed)
	}
	g.applyDial(newRes)
	if g.sess[fresh.key] != fresh || fresh.c != newRes.c {
		t.Fatal("new session not adopted")
	}
	if old.c != nil {
		t.Fatal("old session adopted a client")
	}
}

func TestReconnectAfterServerRestart(t *testing.T) {
	g := newTestGame(t)
	srv, url := startServer(t, "127.0.0.1:0")
	addr := srv.Addr()
	s := g.openSession(url, "me")
	g.view(s.key)
	pumpUntil(t, g, joinedAll(s))
	s.st.Messages["general"] = append(s.st.Messages["general"], state.Message{FromName: "x", Text: "before"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	lostAt := time.Now()
	pumpUntil(t, g, sessionLost(s))
	if d := s.retryAt.Sub(lostAt); d < 500*time.Millisecond || d > 2*time.Second {
		t.Fatalf("retryAt %v ahead, want ~1s", d)
	}
	if s.attempt != 1 {
		t.Fatalf("attempt = %d, want 1", s.attempt)
	}

	startServer(t, addr)
	// A far-future now skips the real backoff wait.
	pumpUntil(t, g, func() bool {
		g.tickReconnect(time.Now().Add(time.Hour))
		return joinedAll(s)()
	})
	if s.st.Phase != state.PhaseMain || s.attempt != 0 || !s.retryAt.IsZero() {
		t.Fatalf("phase %v attempt %d retryAt %v", s.st.Phase, s.attempt, s.retryAt)
	}
	for _, ch := range []string{"general", "random"} {
		if !s.ms.joined[ch] {
			t.Errorf("%s not re-joined: %v", ch, s.ms.joined)
		}
	}
	if ms := s.st.Messages["general"]; len(ms) == 0 || ms[0].Text != "before" {
		t.Fatalf("scrollback lost: %+v", ms)
	}
}

func TestTickReconnectWaitsForRetryAt(t *testing.T) {
	g := newTestGame(t)
	s := newSession("k", "ws://127.0.0.1:1/", "me")
	s.everUp = true
	s.st.Phase = state.PhaseDisconnected
	s.retryAt = time.Now().Add(time.Hour)
	g.sess["k"] = s
	g.tickReconnect(time.Now())
	if s.dialing {
		t.Fatal("dialed before retryAt")
	}
	g.tickReconnect(s.retryAt)
	if !s.dialing {
		t.Fatal("did not dial at retryAt")
	}
}

func TestEverUpDialFailureSchedulesRetry(t *testing.T) {
	g := newTestGame(t)
	s := newSession("k", "ws://127.0.0.1:1/", "me")
	s.everUp = true
	s.attempt = 2
	s.st.Phase = state.PhaseDisconnected
	g.sess["k"] = s
	before := time.Now()
	g.dial(s)
	pumpUntil(t, g, func() bool { return !s.dialing })
	if g.sess["k"] != s {
		t.Fatal("everUp session dropped on failed dial")
	}
	if s.attempt != 3 {
		t.Fatalf("attempt = %d, want 3", s.attempt)
	}
	if want := before.Add(4 * time.Second); s.retryAt.Before(want) {
		t.Fatalf("retryAt %v, want >= %v (4s backoff for attempt 2)", s.retryAt, want)
	}
	if s.st.ConnectErr == "" {
		t.Error("want ConnectErr")
	}
}

func TestRetryNowResetsBackoff(t *testing.T) {
	g := newTestGame(t)
	s := newSession("k", "ws://127.0.0.1:1/", "me")
	s.everUp = true
	s.attempt = 5
	s.retryAt = time.Now().Add(time.Hour)
	s.st.Phase = state.PhaseDisconnected
	g.sess["k"] = s
	g.retryNow("k")
	if s.attempt != 0 || !s.dialing {
		t.Fatalf("attempt %d dialing %v, want 0 and true", s.attempt, s.dialing)
	}
}

func TestBackgroundFailureNoStatusNoise(t *testing.T) {
	g := newTestGame(t)
	_, urlA := startServer(t, "127.0.0.1:0")
	srvB, urlB := startServer(t, "127.0.0.1:0")
	a := g.openSession(urlA, "me")
	b := g.openSession(urlB, "me")
	g.view(a.key)
	pumpUntil(t, g, connected(a))
	pumpUntil(t, g, connected(b))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srvB.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	pumpUntil(t, g, sessionLost(b))
	a.st.Status = "quiet"
	g.tickReconnect(time.Now().Add(time.Hour))
	if !b.dialing {
		t.Fatal("B not retried")
	}
	pumpUntil(t, g, func() bool { return !b.dialing })
	if b.attempt != 2 {
		t.Fatalf("B attempt = %d, want 2 (failed retry)", b.attempt)
	}
	if a.st.Status != "quiet" || g.none.st.ConnectErr != "" {
		t.Fatalf("viewed status %q, none.ConnectErr %q: background failure leaked", a.st.Status, g.none.st.ConnectErr)
	}
}
