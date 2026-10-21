package ui

import (
	"fmt"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/ui/state"
)

// The "voice connected" panel: Discord's spot for the call, at the bottom of
// the channel column while a call is up on any server. The call bar stays as
// it is; the panel is an extra place for the same disconnect and share.

const disconnectLabel = "disconnect"

// voicePanelShown reports whether the panel is wanted: a settled call exists.
// geom additionally drops it when the channel pane is too short to hold it.
func (g *game) voicePanelShown() bool { return g.inCall() }

// voicePanelH is the panel height: three text lines with pad/2 above and below.
func voicePanelH(lh float64) float64 { return 3*lh + pad }

// voicePanelRect is the panel inside chanR, or the zero rect when it is not
// wanted or the pane cannot hold it above a header and two channel rows.
func (g *game) voicePanelRect(chanR rectF, lh float64) rectF {
	if !g.voicePanelShown() {
		return rectF{}
	}
	h := voicePanelH(lh)
	r := rectF{chanR.x0 + pad/2, chanR.y1 - pad/2 - h, chanR.x1 - pad/2, chanR.y1 - pad/2}
	if r.y0 < chanR.y0+pad+3*lh+pad { // header row plus two rows above it
		return rectF{}
	}
	return r
}

// voiceLayout is the pixel layout of the panel, shared by hits and draw.
type voiceLayout struct {
	line1Y, line2Y, btnY, btnH float64
	textX, textW               float64
	leaveX0, leaveX1           float64
	shareX0, shareX1           float64
	signalX                    float64 // left edge of the signal bars on line 1
}

func (g *game) voiceLayout(gm geometry) voiceLayout {
	r := gm.voiceR
	l := voiceLayout{line1Y: r.y0 + pad/2, btnH: gm.lh}
	l.line2Y = l.line1Y + gm.lh
	l.btnY = l.line2Y + gm.lh
	l.textX = r.x0 + pad/2
	l.textW = r.x1 - pad/2 - l.textX
	l.signalX = r.x1 - pad/2 - signalW(gm.lh)

	// Two buttons side by side; when their labels do not fit the pane they
	// share the row equally (the text is clipped to the button).
	gap := float64(pad) / 2
	wl := text.Advance(disconnectLabel, g.face) + pad
	ws := text.Advance(g.shareLabel(), g.face) + pad
	if avail := l.textW - gap; wl+ws > avail {
		wl, ws = avail/2, avail/2
	}
	l.leaveX0, l.leaveX1 = l.textX, l.textX+wl
	l.shareX0, l.shareX1 = l.leaveX1+gap, l.leaveX1+gap+ws
	return l
}

// voicePanelLine is line 2: the call's channel and the server it is on.
func (g *game) voicePanelLine() string {
	s := g.callSess()
	if s == nil {
		return ""
	}
	name := serverTitle(g.servers, g.callKey)
	if name == "" {
		name = state.HostPort(s.url)
	}
	return s.st.InCall + " / " + name
}

// voicePingText is the call connection's round trip, "" without a reading.
func (g *game) voicePingText() string {
	if rtt, ok := g.rttOf(); ok {
		return fmt.Sprintf("%dms", rtt.Milliseconds())
	}
	return ""
}

// addVoicePanelHits registers the panel buttons.
func (g *game) addVoicePanelHits(gm geometry) {
	if gm.voiceR.y1 <= gm.voiceR.y0 {
		return
	}
	l := g.voiceLayout(gm)
	g.hits.Add(state.Hit{X0: l.leaveX0, Y0: l.btnY, X1: l.leaveX1, Y1: l.btnY + l.btnH, Kind: state.HitVoiceLeave})
	g.hits.Add(state.Hit{X0: l.shareX0, Y0: l.btnY, X1: l.shareX1, Y1: l.btnY + l.btnH, Kind: state.HitVoiceShare})
}

func (g *game) drawVoicePanel(screen *ebiten.Image, gm geometry) {
	r := gm.voiceR
	if r.y1 <= r.y0 {
		return
	}
	th := g.th
	l := g.voiceLayout(gm)
	dst := clip(screen, r.x0, r.y0, r.x1, r.y1)
	vector.StrokeLine(dst, float32(r.x0), float32(r.y0), float32(r.x1), float32(r.y0), 1, th.Dim, false)

	// Line 1: the title, then ping and the signal bars at the right.
	right := l.signalX
	g.drawSignal(dst, l.signalX, l.line1Y, gm.lh, g.q.grade)
	if p := g.voicePingText(); p != "" {
		pw := text.Advance(p, g.face)
		if x := l.signalX - pad/2 - pw; x > l.textX+text.Advance("voice", g.face) {
			g.drawText(dst, p, x, l.line1Y, th.Dim)
			right = x
		}
	}
	title := state.Ellipsize("voice connected", int((right-pad/2-l.textX)/gm.adv))
	g.drawText(dst, title, l.textX, l.line1Y, th.Bright)

	g.drawText(dst, state.Ellipsize(g.voicePanelLine(), int(l.textW/gm.adv)), l.textX, l.line2Y, th.FG)

	shareCol := th.FG
	switch {
	case !g.shareSupported():
		shareCol = th.Dim
	case g.share.on:
		shareCol = th.Bright
	}
	g.drawVoiceButton(dst, l.leaveX0, l.leaveX1, l.btnY, gm.lh, disconnectLabel, th.FG)
	g.drawVoiceButton(dst, l.shareX0, l.shareX1, l.btnY, gm.lh, g.shareLabel(), shareCol)
}

func (g *game) drawVoiceButton(dst *ebiten.Image, x0, x1, y, h float64, label string, col color.RGBA) {
	vector.StrokeRect(dst, float32(x0), float32(y), float32(x1-x0), float32(h), 1, g.th.Dim, false)
	g.drawText(clip(dst, x0, y, x1, y+h), label, x0+pad/2, y, col)
}
