package ui

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/audio"
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

		AllowLoopbackICE: true, // in-process voice calls (TestJoinElsewhereLeavesOldCall)
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

// --- Voice across sessions (Task 6) ---

// fakeCloser stands in for an audio device; Close may run on any goroutine.
type fakeCloser struct{ closed atomic.Bool }

func (f *fakeCloser) Close() error { f.closed.Store(true); return nil }

// waitClosed fails unless every closer is closed within 5s (dropVoice closes
// devices on a goroutine).
func waitClosed(t *testing.T, cs ...*fakeCloser) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, c := range cs {
		for !c.closed.Load() {
			if time.Now().After(deadline) {
				t.Fatal("device not closed within 5s")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// callOn makes s the call session in ch, with fake devices.
func callOn(g *game, s *session, ch string) (src, sink *fakeCloser) {
	src, sink = &fakeCloser{}, &fakeCloser{}
	g.vc = voiceCall{start: time.Now(), src: src, sink: sink}
	g.callKey = s.key
	s.st.SetInCall(ch)
	return src, sink
}

// twoSessions connects A and B (each to its own server) and views B.
func twoSessions(t *testing.T, g *game) (srvA *server.Server, a, b *session) {
	t.Helper()
	srvA, urlA := startServer(t, "127.0.0.1:0")
	_, urlB := startServer(t, "127.0.0.1:0")
	a = g.openSession(urlA, "me")
	b = g.openSession(urlB, "me")
	pumpUntil(t, g, joinedAll(a))
	pumpUntil(t, g, joinedAll(b))
	g.view(b.key)
	return srvA, a, b
}

// withLayout gives g a theme and font so geometry and hits can be built.
func withLayout(t *testing.T, g *game) {
	t.Helper()
	tg := testGame(t)
	g.th, g.face, g.w, g.h = tg.th, tg.face, tg.w, tg.h
}

func TestCallSessionDropFreesDevices(t *testing.T) {
	for _, viewConnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "B viewed", true: "connect scene viewed"}[viewConnect], func(t *testing.T) {
			g := newTestGame(t)
			srvA, a, b := twoSessions(t, g)
			src, sink := callOn(g, a, "voice")
			b.st.Status = "quiet"
			if viewConnect {
				g.view("")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := srvA.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			pumpUntil(t, g, sessionLost(a))
			waitClosed(t, src, sink)
			if g.callKey != "" || a.st.InCall != "" {
				t.Fatalf("callKey %q, A InCall %q: call not ended", g.callKey, a.st.InCall)
			}
			if b.c == nil || b.st.Phase != state.PhaseMain || b.st.InCall != "" || b.st.Status != "quiet" {
				t.Fatalf("B touched: c=%v phase=%v InCall=%q Status=%q", b.c != nil, b.st.Phase, b.st.InCall, b.st.Status)
			}
		})
	}
}

func TestDisconnectCallSessionEndsCall(t *testing.T) {
	for _, viewed := range []bool{false, true} {
		t.Run(map[bool]string{false: "not viewed", true: "viewed"}[viewed], func(t *testing.T) {
			g := newTestGame(t)
			_, a, b := twoSessions(t, g)
			src, sink := callOn(g, a, "voice")
			if viewed {
				g.view(a.key)
			}
			g.disconnect(a.key)
			waitClosed(t, src, sink)
			if g.callKey != "" || g.callSess() != nil || g.barVisible() {
				t.Fatalf("callKey %q: call not ended", g.callKey)
			}
			if g.cur() != b {
				t.Fatalf("view = %q, want B", g.curKey)
			}
		})
	}
}

func TestDisconnectOtherSessionKeepsCall(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	src, sink := callOn(g, a, "voice")
	g.disconnect(b.key)
	if g.callKey != a.key || a.st.InCall != "voice" || src.closed.Load() || sink.closed.Load() {
		t.Fatalf("call on A disturbed: callKey %q InCall %q", g.callKey, a.st.InCall)
	}
}

func TestStaleVoiceResultDiscarded(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	src, sink := callOn(g, a, "voice")
	rs, rk := &fakeCloser{}, &fakeCloser{}
	g.vc.busy = true
	// Claims to be for A, but carries B's client: not A's connection.
	g.applyVoice(voiceResult{key: a.key, s: a, c: b.c, channel: "random-voice", src: rs, sink: rk})
	waitClosed(t, rs, rk)
	if g.callKey != a.key || a.st.InCall != "voice" || g.vc.src != src || g.vc.sink != sink {
		t.Fatalf("stale result applied: callKey %q InCall %q", g.callKey, a.st.InCall)
	}
	if src.closed.Load() || sink.closed.Load() || b.st.InCall != "" || a.st.Status != "" || b.st.Status != "" {
		t.Fatal("stale result touched the live call or a status")
	}
}

// A join in flight for A when A is disconnected: the late result is for a
// session that no longer exists, so its devices are closed and nothing
// changes (spec section 4).
func TestJoinResultAfterDisconnectDiscarded(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	g.vc = voiceCall{busy: true, joining: "voice", opKey: a.key}
	ca := a.c
	g.disconnect(a.key)
	if g.barVisible() {
		t.Fatal("bar still shows a join for a disconnected session")
	}
	rs, rk := &fakeCloser{}, &fakeCloser{}
	g.applyVoice(voiceResult{key: a.key, s: a, c: ca, channel: "voice", src: rs, sink: rk})
	waitClosed(t, rs, rk)
	if g.callKey != "" || g.vc.busy || g.vc.src != nil || b.st.InCall != "" {
		t.Fatalf("late join adopted: callKey %q", g.callKey)
	}
}

// Gap 5: the view moved to B while A's join was in flight; the result still
// lands on A.
func TestJoinResultAppliesToStartedSession(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	g.view(a.key)
	g.vc = voiceCall{busy: true, joining: "voice", opKey: a.key}
	g.view(b.key)
	rs, rk := &fakeCloser{}, &fakeCloser{}
	g.applyVoice(voiceResult{key: a.key, s: a, c: a.c, channel: "voice", src: rs, sink: rk, notice: "using defaults"})
	if g.callKey != a.key || g.callSess() != a || a.st.InCall != "voice" || g.vc.src != rs || g.vc.sink != rk {
		t.Fatalf("join not applied to A: callKey %q A InCall %q", g.callKey, a.st.InCall)
	}
	if b.st.InCall != "" || b.st.Status != "" || a.st.Status != "using defaults" {
		t.Fatalf("B InCall %q Status %q, A Status %q", b.st.InCall, b.st.Status, a.st.Status)
	}
	if rs.closed.Load() || rk.closed.Load() {
		t.Fatal("adopted devices closed")
	}
}

// failAudio is an openAudio seam that opens nothing and fails, so tests
// never touch real devices.
func failAudio(string, string) (client.AudioSource, *audio.Meter, client.AudioSink, *audio.Meters, error) {
	return nil, nil, nil, nil, errors.New("microphone unavailable: test")
}

// Gap 5 through joinCall itself: the op records the session it started on,
// so a failure reported after a view switch goes to that session.
func TestJoinCallFailureGoesToStartedSession(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	g.openAudio = failAudio
	g.view(a.key)
	g.clickVoice("voice")
	g.view(b.key)
	if !g.barVisible() {
		t.Fatal("bar hidden while A's join is in flight and B is viewed")
	}
	pumpVoiceUntil(t, g, func() bool { return !g.vc.busy })
	if a.st.Status == "" || b.st.Status != "" || g.callKey != "" {
		t.Fatalf("A Status %q, B Status %q, callKey %q", a.st.Status, b.st.Status, g.callKey)
	}
}

// pumpVoiceUntil runs the session pump and the voice update until cond holds.
func pumpVoiceUntil(t *testing.T, g *game, cond func() bool) {
	t.Helper()
	pumpUntil(t, g, func() bool { g.updateVoice(); return cond() })
}

// nopSource paces silent Opus frames like a real capture source.
type nopSource struct{}

func (nopSource) ReadOpusFrame() ([]byte, error) {
	time.Sleep(20 * time.Millisecond)
	return []byte{0xF8, 0xFF, 0xFE}, nil
}

type nopSink struct{}

func (nopSink) WriteOpusFrame(string, []byte) {}

// Gap 3: joining voice on B while in a call on A leaves A's call on A's
// client (not only its devices) before joining on B.
func TestJoinElsewhereLeavesOldCall(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	a.c.SetLoopbackICE(true)
	done := make(chan error, 1)
	go func(c *client.Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		done <- c.JoinVoice(ctx, "voice", nopSource{}, nopSink{})
	}(a.c)
	var joinErr error
	pumpUntil(t, g, func() bool {
		select {
		case joinErr = <-done:
			return true
		default:
			return false
		}
	})
	if joinErr != nil {
		t.Fatalf("A JoinVoice: %v", joinErr)
	}
	pumpUntil(t, g, func() bool { return a.c.VoiceChannel() == "voice" })
	src, sink := callOn(g, a, "voice")

	// B's devices "fail" so the test opens no real audio; A's leave has
	// already happened by then.
	g.openAudio = failAudio
	g.clickVoice("voice") // B is viewed
	if a.st.InCall != "" || g.callKey != "" {
		t.Fatalf("A still marked in call: InCall %q callKey %q", a.st.InCall, g.callKey)
	}
	pumpVoiceUntil(t, g, func() bool { return !g.vc.busy })
	waitClosed(t, src, sink)
	if v := a.c.VoiceChannel(); v != "" {
		t.Fatalf("A's client still in voice %q: LeaveVoice not called on A", v)
	}
	if b.st.Status == "" {
		t.Fatal("B's join failure not reported on B")
	}
}

// Gaps 1 and 2: with the call on A, the bar shows A's call, and mute, leave
// and the dead-call poll act on A, whether B or the connect scene is viewed.
func TestCallControlsFollowCallSession(t *testing.T) {
	for _, viewConnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "B viewed", true: "connect scene viewed"}[viewConnect], func(t *testing.T) {
			g := newTestGame(t)
			withLayout(t, g)
			g.muteCh = make(chan muteReq, 1)
			_, a, b := twoSessions(t, g)
			g.servers = []state.ServerEntry{{URL: a.key, Label: "primos"}, {URL: b.key, Label: "other"}}
			callOn(g, a, "voice")
			if viewConnect {
				g.view("")
			}
			if !g.barVisible() {
				t.Fatal("bar hidden")
			}
			if got := g.callTitle(); got != "voice @ primos" {
				t.Fatalf("title %q, want %q", got, "voice @ primos")
			}
			g.buildHits()
			l := g.barLayout(g.geom())
			if h, ok := g.hits.At(l.muteX0+1, l.y+1); !ok || h.Kind != state.HitBarMute {
				t.Fatalf("mute hit = %+v %v", h, ok)
			}
			if h, ok := g.hits.At(l.leaveX0+1, l.y+1); !ok || h.Kind != state.HitBarLeave {
				t.Fatalf("leave hit = %+v %v", h, ok)
			}

			g.toggleMute() // Ctrl+M and the bar's mute button
			select {
			case r := <-g.muteCh:
				if r.c != a.c || !r.muted {
					t.Fatalf("mute went to the wrong client (A=%v) or value %v", r.c == a.c, r.muted)
				}
			default:
				t.Fatal("toggleMute queued nothing")
			}

			// Dead-call poll: A's client is not really in voice, so the poll
			// must notice it although A is not viewed.
			g.updateVoice()
			if g.callKey != "" || a.st.InCall != "" || g.barVisible() {
				t.Fatalf("poll did not end A's dead call: callKey %q", g.callKey)
			}
			if a.st.Status != "voice call ended" || b.st.Status != "" {
				t.Fatalf("A Status %q, B Status %q", a.st.Status, b.st.Status)
			}
		})
	}
}

func TestLeaveCallActsOnCallSession(t *testing.T) {
	g := newTestGame(t)
	_, a, b := twoSessions(t, g)
	src, sink := callOn(g, a, "voice")
	g.leaveCall() // the bar's leave button, B viewed
	if g.callKey != "" || a.st.InCall != "" || !g.vc.busy {
		t.Fatalf("leave not started on A: callKey %q InCall %q busy %v", g.callKey, a.st.InCall, g.vc.busy)
	}
	pumpVoiceUntil(t, g, func() bool { return !g.vc.busy })
	waitClosed(t, src, sink)
	if a.st.Status != "" || b.st.Status != "" {
		t.Fatalf("A Status %q, B Status %q", a.st.Status, b.st.Status)
	}
}

func TestBackgroundDialFailureDoesNotTouchConnectScene(t *testing.T) {
	g := newTestGame(t)
	_, urlA := startServer(t, "127.0.0.1:0")
	a := g.openSession(urlA, "me")
	g.view(a.key)
	pumpUntil(t, g, connected(a))
	b := g.openSession("ws://127.0.0.1:1/", "me") // not viewed
	g.noneSession().st.ConnectErr = "typed-by-user"
	pumpUntil(t, g, func() bool { return !b.dialing })
	if _, ok := g.sess[b.key]; ok {
		t.Fatal("failed first dial left a session")
	}
	if g.curKey != a.key {
		t.Fatalf("curKey = %q, want A still viewed", g.curKey)
	}
	if got := g.none.st.ConnectErr; got != "typed-by-user" {
		t.Fatalf("connect scene error = %q, want it untouched", got)
	}
}

func TestSettingsCountsAsNotViewed(t *testing.T) {
	g := newTestGame(t)
	s := newSession("k", "k", "me")
	s.st.Channels = []string{"general"}
	g.sess["k"] = s
	g.view("k")
	if !s.st.Viewed {
		t.Fatal("setup: not viewed")
	}
	g.toggleSettings()
	if s.st.Viewed {
		t.Fatal("viewed session still counts as viewed under Settings")
	}
	s.st.Unread["general"] = true // arrived while Settings covered the chat
	g.toggleSettings()
	if !s.st.Viewed || s.st.Unread["general"] {
		t.Fatalf("after close: viewed=%v unread=%v, want viewed and read", s.st.Viewed, s.st.Unread)
	}
}
