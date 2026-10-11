package ui

import (
	"math"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// Animation tuning.
const (
	fadeTau       = 0.15 // hover / active highlight and call-bar slide, seconds
	speakAttack   = 0.08 // speaking envelope rise time constant
	speakRelease  = 0.4  // speaking envelope fall time constant
	speakMaxAlpha = 0.35 // faint cap on the speaking glow (fraction of Bright)
	speakGain     = 3.0  // sqrt(envelope) gain: speech RMS is small

	hoverAlpha  = 0.12 // row highlight at full strength, fraction of Bright
	hoverWeight = 0.6  // hover target relative to the active row (1.0)

	minDT = 1.0 / 240
	maxDT = 1.0 / 20
)

// clampDT turns a tick rate into a frame step, bounded so a stall or an
// absurd rate cannot make the eased values jump.
func clampDT(tps float64) float64 {
	if tps <= 0 || math.IsNaN(tps) {
		return maxDT
	}
	return math.Min(maxDT, math.Max(minDT, 1/tps))
}

// glowAlpha is the speaking-glow alpha (0..speakMaxAlpha) for an envelope value.
func glowAlpha(env float64) float64 {
	return math.Min(1, math.Sqrt(math.Max(0, env))*speakGain) * speakMaxAlpha
}

// barFrac is how far the call bar has slid in: 0 hidden, 1 fully shown. Before
// the first animation step it is the settled value, so geometry computed
// outside the running game (tests) is the settled layout.
func (g *game) barFrac() float64 {
	if !g.barInit {
		if g.barVisible() {
			return 1
		}
		return 0
	}
	return g.barF
}

// stepBar eases the call bar toward its target. It runs before the hit
// registry is rebuilt so hit rects follow the drawn bar.
func (g *game) stepBar(dt float64) {
	if !g.barInit {
		g.barF, g.barInit = g.barFrac(), true
	}
	target := 0.0
	if g.barVisible() {
		target = 1
	}
	g.barF = state.Approach(g.barF, target, dt, fadeTau)
	if math.Abs(g.barF-target) < 0.002 {
		g.barF = target
	}
}

// stepHighlights advances the per-occupant speaking envelopes and the row
// hover/active highlights, and prunes entries for rows that are gone.
func (g *game) stepHighlights(dt float64) {
	rows := g.st.Rows()
	if g.speakEnv == nil {
		g.speakEnv = map[string]*state.Envelope{}
		g.rowHL = map[string]float64{}
	}
	var hoverKey string
	if h, ok := g.hitAt(); ok {
		switch h.Kind {
		case state.HitChannel:
			if h.ID >= 0 && h.ID < len(g.st.Channels) {
				hoverKey = textRowKey(g.st.Channels[h.ID])
			}
		case state.HitVoice:
			hoverKey = voiceRowKey(h.Key)
		}
	}
	for _, r := range rows {
		switch r.Kind {
		case state.RowOccupant:
			e := g.speakEnv[r.FP]
			if e == nil {
				e = &state.Envelope{Attack: speakAttack, Release: speakRelease}
				g.speakEnv[r.FP] = e
			}
			lvl := 0.0
			if !r.Muted {
				lvl = g.speakLevel(r.FP)
			}
			e.Step(lvl, dt)
		case state.RowText, state.RowVoice:
			k, active := textRowKey(r.Channel), r.Index == g.st.Active
			if r.Kind == state.RowVoice {
				k, active = voiceRowKey(r.Channel), r.Channel == g.st.InCall
			}
			target := 0.0
			switch {
			case active:
				target = 1
			case k == hoverKey:
				target = hoverWeight
			}
			g.rowHL[k] = state.Approach(g.rowHL[k], target, dt, fadeTau)
		}
	}
	for fp := range g.speakEnv {
		if !hasOccupant(rows, fp) {
			delete(g.speakEnv, fp)
		}
	}
	for k := range g.rowHL {
		if !hasRowKey(rows, k) {
			delete(g.rowHL, k)
		}
	}
}

func textRowKey(ch string) string  { return "t:" + ch }
func voiceRowKey(ch string) string { return "v:" + ch }

func hasOccupant(rows []state.Row, fp string) bool {
	for _, r := range rows {
		if r.Kind == state.RowOccupant && r.FP == fp {
			return true
		}
	}
	return false
}

func hasRowKey(rows []state.Row, k string) bool {
	for _, r := range rows {
		switch r.Kind {
		case state.RowText:
			if textRowKey(r.Channel) == k {
				return true
			}
		case state.RowVoice:
			if voiceRowKey(r.Channel) == k {
				return true
			}
		}
	}
	return false
}

// frameDT is the step for this tick.
func frameDT() float64 { return clampDT(ebiten.ActualTPS()) }
