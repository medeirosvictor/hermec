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
