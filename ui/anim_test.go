package ui

import (
	"math"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/audio"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestClampDT(t *testing.T) {
	for _, c := range []struct{ tps, want float64 }{
		{60, 1.0 / 60}, {1000, 1.0 / 240}, {1, 1.0 / 20}, {0, 1.0 / 20}, {math.NaN(), 1.0 / 20},
	} {
		if got := clampDT(c.tps); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("clampDT(%v) = %v, want %v", c.tps, got, c.want)
		}
	}
}

func TestGlowAlphaFaintAndMonotonic(t *testing.T) {
	if glowAlpha(0) != 0 || glowAlpha(-1) != 0 {
		t.Fatal("silence must not glow")
	}
	prev := 0.0
	for _, e := range []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 5} {
		a := glowAlpha(e)
		if a < prev || a > speakMaxAlpha+1e-12 {
			t.Fatalf("glowAlpha(%v) = %v (prev %v, cap %v)", e, a, prev, speakMaxAlpha)
		}
		prev = a
	}
	if glowAlpha(1) != speakMaxAlpha {
		t.Fatalf("loud level should hit the cap, got %v", glowAlpha(1))
	}
}

// The bar slides: mid-slide the strip is shorter than settled, the content
// above gets the space back gradually, and settled geometry is unchanged.
func TestBarSlideGeometry(t *testing.T) {
	g := testGame(t)
	settledOff := g.geom()
	setCall(g, "voice")
	settledOn := g.geom()

	g.barInit, g.barF = true, 0 // sliding in from hidden
	hidden := g.geom()
	if hidden.barR.y1 > hidden.barR.y0 || hidden.chanR != settledOff.chanR {
		t.Fatalf("hidden bar should take no space: %+v", hidden)
	}
	g.stepBar(1.0 / 60)
	mid := g.geom()
	if h := mid.barR.y1 - mid.barR.y0; h <= 0 || h >= settledOn.barR.y1-settledOn.barR.y0 {
		t.Fatalf("mid-slide bar height %v not between hidden and settled", h)
	}
	if mid.chanR.y1 >= settledOff.chanR.y1 || mid.chanR.y1 <= settledOn.chanR.y1 {
		t.Fatalf("content bottom should be between settled values: %v", mid.chanR.y1)
	}
	for i := 0; i < 600; i++ {
		g.stepBar(1.0 / 60)
	}
	if got := g.geom(); got != settledOn {
		t.Fatalf("settled-in geometry differs:\n%+v\n%+v", got, settledOn)
	}
	setCall(g, "")
	for i := 0; i < 600; i++ {
		g.stepBar(1.0 / 60)
	}
	if got := g.geom(); got != settledOff {
		t.Fatalf("settled-out geometry differs:\n%+v\n%+v", got, settledOff)
	}
}

// The speaking glow comes from our call's levels, so it lights only rows of
// the call's channel on the call's server, not the same fingerprint (our own
// identity) in a voice channel of another server.
func TestSpeakGlowOnlyOnCallSession(t *testing.T) {
	g := testGame(t)
	g.fp = "me"
	g.sess = map[string]*session{}
	for _, k := range []string{"A", "B"} {
		s := newSession(k, "ws://"+k+"/", "me")
		s.st.SetConnected([]proto.ChannelInfo{{Name: "general"}, {Name: "voice", Type: "voice"}, {Name: "other", Type: "voice"}}, "me")
		g.sess[k] = s
	}
	a, b := g.sess["A"], g.sess["B"]
	a.st.Voice = map[string][]proto.VoiceMember{"voice": {{Name: "me", Fingerprint: "me"}}, "other": {{Name: "x", Fingerprint: "fpX"}}}
	b.st.Voice = map[string][]proto.VoiceMember{"voice": {{Name: "me", Fingerprint: "me"}}}
	g.view("B")
	g.callKey, a.st.InCall = "A", "voice"
	g.vc.mic = &audio.Meter{}
	g.vc.mic.Observe(0.5, time.Now())

	g.stepHighlights(1.0 / 60)
	if v := g.speakEnv["me"].Value(); v != 0 {
		t.Fatalf("own row glows on B (not the call's server): %v", v)
	}
	g.view("A")
	g.stepHighlights(1.0 / 60)
	if v := g.speakEnv["me"].Value(); v <= 0 {
		t.Fatalf("own row in the call channel does not glow: %v", v)
	}
	if v := g.speakEnv["fpX"].Value(); v != 0 {
		t.Fatalf("row outside the call channel glows: %v", v)
	}
}

// Speaking envelopes follow the level and are pruned when the occupant leaves.
func TestEnvelopePrunedWithOccupant(t *testing.T) {
	g := testGame(t)
	g.cur().st.Channels = []string{"general"}
	g.cur().st.VoiceChannels = []string{"voice"}
	g.cur().st.Voice = map[string][]proto.VoiceMember{"voice": {{Name: "a", Fingerprint: "fpA"}, {Name: "b", Fingerprint: "fpB"}}}
	g.stepHighlights(1.0 / 60)
	if len(g.speakEnv) != 2 {
		t.Fatalf("want 2 envelopes, got %d", len(g.speakEnv))
	}
	g.cur().st.Voice = map[string][]proto.VoiceMember{"voice": {{Name: "a", Fingerprint: "fpA"}}}
	g.stepHighlights(1.0 / 60)
	if _, ok := g.speakEnv["fpB"]; ok || len(g.speakEnv) != 1 {
		t.Fatalf("fpB not pruned: %v", g.speakEnv)
	}
}
