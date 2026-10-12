package ui

import (
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/ui/state"
)

// Hit IDs for state.HitSignal.
const (
	signalBars  = 0 // the bars: toggle the panel
	signalPanel = 1 // the open panel: swallows the click
)

// quality is the connection-quality readout: the bar count and the detail
// panel. It is refreshed once per tick by stepQuality.
type quality struct {
	grade int
	open  bool    // the owner asked for the detail panel
	f     float64 // panel open/close ease, 0 closed .. 1 open
	parts []state.Participant
	lines []string
	rect  rectF // the drawn panel, empty while closed
}

// signalW is the width of the four bars for bar height h.
func signalW(h float64) float64 { return h * 1.1 }

// signalOnBar reports whether the bars live on the call bar (else the status
// line). Draw, hits and the panel anchor all use this one condition.
func (g *game) signalOnBar() bool {
	s := g.callSess()
	return s != nil && s.st.InCall != "" && g.barFrac() > 0
}

// signalRect is where the bars sit: at the right end of the call bar while in
// a call, otherwise at the right end of the status line.
func (g *game) signalRect(gm geometry) rectF {
	h := gm.lh
	w := signalW(h)
	if g.signalOnBar() {
		x1 := gm.barR.x1 - pad
		return rectF{x1 - w, gm.barY, x1, gm.barY + h}
	}
	x1 := gm.statusR.x1 - pad
	return rectF{x1 - w, gm.statusY, x1, gm.statusY + h}
}

// occupantName is the display name for a fingerprint from the voice rows.
func occupantName(rows []state.Row, fp string) string {
	for _, r := range rows {
		if r.Kind == state.RowOccupant && r.FP == fp {
			return r.Name
		}
	}
	if len(fp) > 8 {
		fp = fp[:8]
	}
	return fp
}

// stepQuality refreshes the grade and the panel. It runs before buildHits so
// the hit rects follow the drawn panel.
func (g *game) stepQuality(dt float64) {
	q := &g.q
	rtt, rttOK := g.rttOf()
	q.parts = q.parts[:0]
	if cs := g.callSess(); g.vc.spk != nil && cs != nil && cs.st.InCall != "" {
		if fps := g.vc.spk.Senders(); len(fps) > 0 { // allocates: once per frame
			rows := cs.st.Rows() // names from the call's server, whatever is viewed
			for _, fp := range fps {
				h := g.vc.spk.Health(fp)
				q.parts = append(q.parts, state.Participant{
					Name: occupantName(rows, fp), Concealed: h.Concealed, Jitter: h.Jitter, OK: h.OK,
				})
			}
		}
	}
	worstC, worstJ := state.WorstHealth(q.parts)
	q.grade = state.Grade(rtt, rttOK, worstC, worstJ)

	if g.cur().st.Phase != state.PhaseMain {
		q.open = false
	}
	target := 0.0
	if q.open {
		target = 1
	}
	q.f = state.Approach(q.f, target, dt, fadeTau)
	if math.Abs(q.f-target) < 0.002 {
		q.f = target
	}
	q.rect = rectF{}
	if q.f <= 0 {
		q.lines = q.lines[:0]
		return
	}
	q.lines = state.QualityLines(rtt, rttOK, q.parts)
	gm := g.geom()
	var tw float64
	for _, l := range q.lines {
		tw = math.Max(tw, text.Advance(l, g.face))
	}
	w := tw + 2*pad
	full := float64(len(q.lines))*gm.lh + 2*pad
	h := full * q.f
	x1 := gm.statusR.x1
	if g.signalOnBar() {
		q.rect = rectF{x1 - w, gm.barR.y0 - pad/2 - h, x1, gm.barR.y0 - pad/2}
	} else {
		q.rect = rectF{x1 - w, gm.statusR.y1 + pad/2, x1, gm.statusR.y1 + pad/2 + h}
	}
}

// rttOf reads the smoothed RTT of the call session's client while a call
// exists, else of the viewed session's; not ok with no connection.
func (g *game) rttOf() (time.Duration, bool) {
	c := g.cur().c
	if s := g.callSess(); s != nil {
		c = s.c
	}
	if c == nil {
		return 0, false
	}
	return c.RTT()
}

// addSignalHits registers the bars and, while open, the panel (registered
// last so it wins, and clicks inside it do not fall through).
func (g *game) addSignalHits(gm geometry) {
	r := g.signalRect(gm)
	y0, y1 := r.y0, r.y1
	if g.signalOnBar() { // while the bar slides, clip to the drawn strip
		y0, y1 = math.Max(y0, gm.barR.y0), math.Min(y1, gm.barR.y1)
	}
	if y1 > y0 {
		g.hits.Add(state.Hit{X0: r.x0 - pad/2, Y0: y0, X1: r.x1 + pad/2, Y1: y1, Kind: state.HitSignal, ID: signalBars})
	}
	if q := g.q.rect; q.y1 > q.y0 {
		g.hits.Add(state.Hit{X0: q.x0, Y0: q.y0, X1: q.x1, Y1: q.y1, Kind: state.HitSignal, ID: signalPanel})
	}
}

// toggleQuality is a click on the bars; Esc and a second click close it.
func (g *game) toggleQuality() { g.q.open = !g.q.open }

// drawSignal draws four ascending bars of height h at (x, y); the first
// `bars` are lit.
func (g *game) drawSignal(dst *ebiten.Image, x, y, h float64, bars int) {
	bw := h / 5
	gap := bw / 2
	for i := 0; i < 4; i++ {
		bh := h * float64(i+1) / 4
		c := g.th.Dim
		if i < bars {
			c = g.th.Bright
		}
		vector.FillRect(dst, float32(x+float64(i)*(bw+gap)), float32(y+h-bh), float32(bw), float32(bh), c, false)
	}
}

// drawQualityPanel draws the open detail panel on top of the scene.
func (g *game) drawQualityPanel(screen *ebiten.Image, gm geometry) {
	q := &g.q
	if q.rect.y1 <= q.rect.y0 {
		return
	}
	g.drawPanelR(screen, q.rect)
	dst := clip(screen, q.rect.x0, q.rect.y0, q.rect.x1, q.rect.y1)
	y := q.rect.y0 + pad
	for i, l := range q.lines {
		col := g.th.FG
		if i == 0 {
			col = g.th.Bright
		}
		g.drawText(dst, l, q.rect.x0+pad, y, col)
		y += gm.lh
	}
}
