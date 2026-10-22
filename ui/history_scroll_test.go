package ui

import (
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/ui/state"
)

// olderTestGame is a windowless game whose viewed session shows "general"
// with msgs and an older page available. Requests are counted, not sent.
func olderTestGame(t *testing.T, msgs []state.Message) (*game, *session, *int32) {
	t.Helper()
	g := newTestGame(t)
	s := newSession("k", "ws://x/", "tester")
	s.st.Channels = []string{"general"}
	s.st.Messages["general"] = msgs
	s.st.Older["general"] = true
	s.ms.confirmed["general"] = true
	g.sess["k"], g.curKey = s, "k"
	var n int32
	g.reqOlder = func(_ *session, ch, id string, ts time.Time) error {
		if ch != "general" || id != msgs[0].ID || !ts.Equal(msgs[0].TS) {
			t.Errorf("request for %q before %q", ch, id)
		}
		atomic.AddInt32(&n, 1)
		return nil
	}
	return g, s, &n
}

func waitCount(t *testing.T, n *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(n) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(n); got != want {
		t.Fatalf("requests = %d, want %d", got, want)
	}
}

func TestScrollTopRequestsOlder(t *testing.T) {
	msgs := []state.Message{{ID: "b", TS: time.Unix(2, 0)}, {ID: "c", TS: time.Unix(3, 0)}}
	g, s, n := olderTestGame(t, msgs)
	total, visible := 50, 10
	top := state.ClampScroll(total, visible, math.MaxInt)

	s.ms.scroll = top - 1 // not at the top yet
	g.maybeLoadOlder(s, total, visible)
	if s.st.Loading["general"] {
		t.Fatal("requested away from the top")
	}

	s.ms.scroll = top
	g.maybeLoadOlder(s, total, visible)
	if !s.st.Loading["general"] {
		t.Fatal("Loading not set at the top")
	}
	waitCount(t, n, 1)
	g.maybeLoadOlder(s, total, visible) // still loading: nothing more
	time.Sleep(50 * time.Millisecond)
	waitCount(t, n, 1)
}

func TestShortChannelKeepsFilling(t *testing.T) {
	g, s, n := olderTestGame(t, []state.Message{{ID: "b", TS: time.Unix(2, 0)}})
	g.maybeLoadOlder(s, 3, 10) // everything fits: scroll 0 is the top
	waitCount(t, n, 1)
}

// A history_before that beats the join to the host is refused as not_joined,
// which the client blames on the pending join; so none goes out before then.
func TestNoOlderRequestBeforeJoinConfirmed(t *testing.T) {
	g, s, n := olderTestGame(t, []state.Message{{ID: "b", TS: time.Unix(2, 0)}})
	delete(s.ms.confirmed, "general")
	s.ms.joined["general"] = true // join sent, not answered
	g.maybeLoadOlder(s, 3, 10)
	if s.st.Loading["general"] {
		t.Fatal("requested before the join was confirmed")
	}
	g.applyJoin(joinResult{key: "k", channel: "general", err: errors.New("timeout")})
	if s.ms.confirmed["general"] || s.ms.joined["general"] {
		t.Fatalf("failed join: confirmed=%v joined=%v", s.ms.confirmed, s.ms.joined)
	}
	g.maybeLoadOlder(s, 3, 10)
	if s.st.Loading["general"] {
		t.Fatal("requested after a failed join")
	}
	g.applyJoin(joinResult{key: "k", channel: "general"})
	if !s.ms.confirmed["general"] {
		t.Fatal("join success not recorded")
	}
	g.maybeLoadOlder(s, 3, 10)
	waitCount(t, n, 1)
}

func TestScrollTopSendErrorClearsLoading(t *testing.T) {
	g, s, _ := olderTestGame(t, []state.Message{{ID: "b", TS: time.Unix(2, 0)}})
	g.reqOlder = func(*session, string, string, time.Time) error { return errors.New("boom") }
	g.maybeLoadOlder(s, 3, 10)
	pumpUntil(t, g, func() bool { return !s.st.Loading["general"] })
}

func TestPrependKeepsScroll(t *testing.T) {
	old := []state.Message{{ID: "b", TS: time.Unix(2, 0)}, {ID: "c", TS: time.Unix(3, 0)}}
	prepended := append([]state.Message{{ID: "a", TS: time.Unix(1, 0)}}, old...)
	changed := newestKey(prepended) != newestKey(old)
	if got := adjustScroll(5, 14, 10, changed); got != 5 {
		t.Fatalf("scroll = %d after prepend, want 5", got)
	}
}

func TestAppendWhileScrolledShifts(t *testing.T) {
	old := []state.Message{{ID: "b", TS: time.Unix(2, 0)}, {ID: "c", TS: time.Unix(3, 0)}}
	appended := append(append([]state.Message{}, old...), state.Message{ID: "d", TS: time.Unix(4, 0)})
	changed := newestKey(appended) != newestKey(old)
	if got := adjustScroll(5, 12, 10, changed); got != 7 {
		t.Fatalf("scroll = %d after append, want 7", got)
	}
	if got := adjustScroll(0, 12, 10, changed); got != 0 {
		t.Fatalf("pinned scroll moved to %d", got)
	}
}

func TestNewestKeyFallsBackToTS(t *testing.T) {
	a := []state.Message{{TS: time.Unix(1, 0)}}
	b := []state.Message{{TS: time.Unix(2, 0)}}
	if newestKey(a) == newestKey(b) || newestKey(nil) != "" {
		t.Fatal("bad key")
	}
}
