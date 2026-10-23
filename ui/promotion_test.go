package ui

import (
	"context"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

// Another client's takeover promotes the standby before this session's own
// watchdog fires: the session still ends on the standby with its call, never
// on the reconnect screen.
func TestSessionFollowsPromotionByOtherClient(t *testing.T) {
	g, p, s := pairGame(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.c.JoinVoice(ctx, "voice", nopSource{}, nopSink{}); err != nil {
		t.Fatalf("JoinVoice: %v", err)
	}
	d := &duplexNop{}
	g.vc = voiceCall{start: time.Now(), src: d, sink: d}
	g.callKey = s.key
	s.st.SetInCall("voice")
	warm := s.warm
	other, err := client.DialWith(ctx, p.bURL, mustIdentity(t), "other", "", client.DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	p.killA(t)
	for { // the other client wins the race: our session is not pumped meanwhile
		if other.Takeover(ctx) == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i := 0; warm.Warm(); i++ {
		if i > 500 {
			t.Fatal("our warm session never saw the promotion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.drainWarm(s) // runs before drainEvents in a tick
	pumpUntil(t, g, func() bool {
		if s.failed || s.st.Phase == state.PhaseDisconnected {
			t.Fatalf("reconnect screen shown: failed=%v phase=%v", s.failed, s.st.Phase)
		}
		return s.c == warm && !s.switching
	})
	if g.callKey != s.key || s.st.InCall != "voice" {
		t.Fatalf("call lost: callKey %q InCall %q", g.callKey, s.st.InCall)
	}
}
