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

// voicePanelShown reports whether the panel is wanted: a call exists or a join
// or leave is in flight (the panel keeps its place so the channel list does not
// jump). Its buttons are live only in a settled call (inCall).
func (g *game) voicePanelShown() bool { return g.inCall() || g.vc.busy }

// voiceTextW is the width of the panel's content for a channel pane.
func voiceTextW(chanR rectF) float64 { return chanR.x1 - chanR.x0 - 2*pad }

// voiceStacked reports whether "disconnect" and "stop share" (the wider share
// label, so the layout does not change when sharing starts) do not fit side by
// side in textW at the real font; each button then gets its own row.
func (g *game) voiceStacked(textW float64) bool {
	need := text.Advance(disconnectLabel, g.face) + text.Advance(shareLabelOn, g.face) + text.Advance("stop camera", g.face) + 5*pad
	return need > textW
}

// voicePanelH is the panel height: title, channel line and the button row(s),
// with pad/2 above and below.
func (g *game) voicePanelH(lh, textW float64) float64 {
	rows := 3.0
	if g.voiceStacked(textW) {
		rows = 5
	}
	return rows*lh + pad
}

// voicePanelRect is the panel inside chanR, or the zero rect when it is not
// wanted or the pane cannot hold it above a header and two channel rows.
func (g *game) voicePanelRect(chanR rectF, lh float64) rectF {
	if !g.voicePanelShown() {
		return rectF{}
	}
	h := g.voicePanelH(lh, voiceTextW(chanR))
	r := rectF{chanR.x0 + pad/2, chanR.y1 - pad/2 - h, chanR.x1 - pad/2, chanR.y1 - pad/2}
	if r.y0 < chanR.y0+pad+3*lh+pad { // header row plus two rows above it
		return rectF{}
	}
	return r
}

// voiceLayout is the pixel layout of the panel, shared by hits and draw.
type voiceLayout struct {
	line1Y, line2Y     float64
	leaveY, shareY     float64 // button row tops
	cameraY            float64
	btnH               float64
	textX, textW       float64
	leaveX0, leaveX1   float64
	shareX0, shareX1   float64
	cameraX0, cameraX1 float64
	signalX            float64 // left edge of the signal bars on line 1
}

func (g *game) voiceLayout(gm geometry) voiceLayout {
	r := gm.voiceR
	l := voiceLayout{line1Y: r.y0 + pad/2, btnH: gm.lh}
	l.line2Y = l.line1Y + gm.lh
	l.leaveY = l.line2Y + gm.lh
	l.textX = r.x0 + pad/2
	l.textW = r.x1 - pad/2 - l.textX
	l.signalX = r.x1 - pad/2 - signalW(gm.lh)

	gap := float64(pad) / 2
	if g.voiceStacked(l.textW) {
		l.shareY = l.leaveY + gm.lh
		l.cameraY = l.shareY + gm.lh
		l.leaveX0, l.leaveX1 = l.textX, l.textX+l.textW
		l.shareX0, l.shareX1 = l.leaveX0, l.leaveX1
		l.cameraX0, l.cameraX1 = l.leaveX0, l.leaveX1
		return l
	}
	l.shareY, l.cameraY = l.leaveY, l.leaveY
	wl := text.Advance(disconnectLabel, g.face) + pad
	ws := text.Advance(g.shareLabel(), g.face) + pad
	l.leaveX0, l.leaveX1 = l.textX, l.textX+wl
	l.shareX0, l.shareX1 = l.leaveX1+gap, l.leaveX1+gap+ws
	wc := text.Advance(g.cameraLabel(), g.face) + pad
	l.cameraX0, l.cameraX1 = l.shareX1+gap, l.shareX1+gap+wc
	return l
}

// voicePanelLine is line 2: the call's channel and the server it is on.
func (g *game) voicePanelLine() string {
	s := g.callSess()
	if s == nil {
		switch {
		case g.vc.joining != "":
			return "joining " + g.vc.joining + "..."
		case g.vc.busy:
			return "leaving..."
		}
		return ""
	}
	name := serverTitle(g.servers, g.callKey)
	if name == "" {
		name = state.HostPort(s.url)
	}
	line := s.st.InCall + " / " + name
	if by := s.st.HostedBy; by != "" && by != name {
		line += " · hosted by " + by // clipped to the panel by the caller
	}
	return line
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
	if gm.voiceR.y1 <= gm.voiceR.y0 || !g.inCall() { // inert while a join or leave is in flight
		return
	}
	l := g.voiceLayout(gm)
	// The bars, like the call bar's, open the quality panel.
	g.hits.Add(state.Hit{X0: l.signalX - pad/2, Y0: l.line1Y, X1: l.signalX + signalW(gm.lh) + pad/2, Y1: l.line1Y + gm.lh, Kind: state.HitSignal, ID: signalBars})
	g.hits.Add(state.Hit{X0: l.leaveX0, Y0: l.leaveY, X1: l.leaveX1, Y1: l.leaveY + l.btnH, Kind: state.HitVoiceLeave})
	g.hits.Add(state.Hit{X0: l.shareX0, Y0: l.shareY, X1: l.shareX1, Y1: l.shareY + l.btnH, Kind: state.HitVoiceShare})
	g.hits.Add(state.Hit{X0: l.cameraX0, Y0: l.cameraY, X1: l.cameraX1, Y1: l.cameraY + l.btnH, Kind: state.HitVoiceCamera})
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
	live := g.inCall()
	right := l.signalX
	if live {
		g.drawSignal(dst, l.signalX, l.line1Y, gm.lh, g.q.grade)
	}
	if p := g.voicePingText(); live && p != "" {
		pw := text.Advance(p, g.face)
		if x := l.signalX - pad/2 - pw; x > l.textX+text.Advance("voice", g.face) {
			g.drawText(dst, p, x, l.line1Y, th.Dim)
			right = x
		}
	}
	titleText, titleCol := "voice connected", th.Bright
	if !live {
		titleText, titleCol = "voice", th.Dim
	}
	title := state.Ellipsize(titleText, int((right-pad/2-l.textX)/gm.adv))
	g.drawText(dst, title, l.textX, l.line1Y, titleCol)

	g.drawText(dst, state.Ellipsize(g.voicePanelLine(), int(l.textW/gm.adv)), l.textX, l.line2Y, th.FG)

	shareCol := th.FG
	switch {
	case !g.shareSupported():
		shareCol = th.Dim
	case g.share.on:
		shareCol = th.Bright
	}
	leaveCol := th.FG
	if !live {
		leaveCol, shareCol = th.Dim, th.Dim
	}
	cameraCol := th.FG
	if !live || !g.cameraSupported() {
		cameraCol = th.Dim
	} else if g.camera.on {
		cameraCol = th.Bright
	}
	g.drawVoiceButton(dst, l.leaveX0, l.leaveX1, l.leaveY, gm.lh, disconnectLabel, leaveCol)
	g.drawVoiceButton(dst, l.shareX0, l.shareX1, l.shareY, gm.lh, g.shareLabel(), shareCol)
	g.drawVoiceButton(dst, l.cameraX0, l.cameraX1, l.cameraY, gm.lh, g.cameraLabel(), cameraCol)
}

func (g *game) drawVoiceButton(dst *ebiten.Image, x0, x1, y, h float64, label string, col color.RGBA) {
	vector.StrokeRect(dst, float32(x0), float32(y), float32(x1-x0), float32(h), 1, g.th.Dim, false)
	g.drawText(clip(dst, x0, y, x1, y+h), label, x0+pad/2, y, col)
}
