package ui

import (
	"math"
	"testing"

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
	g.st.SetInCall("voice")
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
	g.st.SetInCall("")
	for i := 0; i < 600; i++ {
		g.stepBar(1.0 / 60)
	}
	if got := g.geom(); got != settledOff {
		t.Fatalf("settled-out geometry differs:\n%+v\n%+v", got, settledOff)
	}
}

// Speaking envelopes follow the level and are pruned when the occupant leaves.
func TestEnvelopePrunedWithOccupant(t *testing.T) {
	g := testGame(t)
	g.st.Channels = []string{"general"}
	g.st.VoiceChannels = []string{"voice"}
	g.st.Voice = map[string][]proto.VoiceMember{"voice": {{Name: "a", Fingerprint: "fpA"}, {Name: "b", Fingerprint: "fpB"}}}
	g.stepHighlights(1.0 / 60)
	if len(g.speakEnv) != 2 {
		t.Fatalf("want 2 envelopes, got %d", len(g.speakEnv))
	}
	g.st.Voice = map[string][]proto.VoiceMember{"voice": {{Name: "a", Fingerprint: "fpA"}}}
	g.stepHighlights(1.0 / 60)
	if _, ok := g.speakEnv["fpB"]; ok || len(g.speakEnv) != 1 {
		t.Fatalf("fpB not pruned: %v", g.speakEnv)
	}
}
