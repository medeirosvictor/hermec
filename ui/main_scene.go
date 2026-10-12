package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

const (
	channelPaneW  = 180 // fixed left pane width in px
	maxInputRunes = 1000
	minCols       = 10

	wheelLinesPerNotch = 3
)

// mainScene is the per-connection view state of the chat scene.
type mainScene struct {
	joined     map[string]bool
	scroll     int // lines scrolled up from the bottom; 0 = pinned
	prevTotal  int
	wheelCarry float64      // fractional wheel lines carried between frames
	lines      []state.Line // laid out in updateMain, drawn in drawMain
	layout     state.LayoutCache
	layoutGen  int           // bumps on every relayout
	sbImg      *ebiten.Image // cached render of the scrollback pane
	sbKey      sbKey
	visible    int
}

func newMainScene() mainScene { return mainScene{joined: map[string]bool{}} }

// geometry is the pixel layout of the main scene for the current window.
// The *R fields are panel surfaces; the other fields are text-area
// coordinates inside them (a pad inside the panel edge).
type geometry struct {
	w, h                int
	ox                  float64 // left edge (rail width)
	statusR, chanR      rectF   // status line panel; channel pane (runs beside the input)
	chatR, inputR, barR rectF   // chat pane; input line (right column); call bar (only while barVisible)
	statusY             float64
	paneY0, paneY1      float64 // scrollback (chat pane) text extent
	chanY1              float64 // channel pane text bottom
	inputY              float64 // chat input text top
	barY                float64 // call bar text top; meaningful only while barVisible
	rightX, rightW      float64 // chat text column

	adv, lh              float64
	cols, visible, lcols int
}

func (g *game) geom() geometry {
	lh := g.lineH()
	adv := text.Advance("M", g.face)
	if adv < 1 {
		adv = 1
	}
	gm := geometry{w: g.w, h: g.h, ox: g.railW(), adv: adv, lh: lh}
	left, right := gm.ox+margin, float64(g.w)-margin
	slim := lh + pad // one text line on a panel, pad/2 above and below

	sy := margin + g.bannerH()
	gm.statusR = rectF{left, sy, right, sy + slim}
	top := gm.statusR.y1 + pad
	bottom := float64(g.h) - margin
	// The call bar (when shown) is the full-width bottom panel; everything
	// above it is content. The input panel sits under the right column only.
	// The bar slides in and out: f eases 0..1, and the content above gives up
	// the space gradually. At rest f is exactly 0 or 1 (the settled layout).
	if f := g.barFrac(); f > 0 {
		gm.barR = rectF{left, bottom - slim*f, right, bottom}
		bottom = gm.barR.y0 - pad*f
	}
	gm.chanR = rectF{left, top, left + channelPaneW, bottom}
	chatX := gm.chanR.x1 + pad
	gm.inputR = rectF{chatX, bottom - slim, right, bottom}
	gm.chatR = rectF{chatX, top, right, gm.inputR.y0 - pad}

	gm.statusY = gm.statusR.y0 + pad/2
	gm.barY = gm.barR.y0 + pad/2
	gm.inputY = gm.inputR.y0 + pad/2
	gm.paneY0 = top + pad
	gm.paneY1 = gm.chatR.y1 - pad
	gm.chanY1 = bottom - pad
	gm.rightX = chatX + pad
	gm.rightW = right - pad - gm.rightX
	gm.cols = int(gm.rightW / adv)
	if gm.cols < minCols {
		gm.cols = minCols
	}
	gm.lcols = int((channelPaneW - 2*pad) / adv)
	gm.visible = int((gm.paneY1 - gm.paneY0) / lh)
	if gm.visible < 1 {
		gm.visible = 1
	}
	return gm
}

// addMainHits registers the channel/voice rows, the chat input and the call
// bar buttons from the same geometry drawMain uses.
func (g *game) addMainHits(gm geometry) {
	// Only rows drawChannels actually draws are clickable.
	y := gm.paneY0
	for _, r := range g.cur().st.Rows() {
		if y+gm.lh > gm.chanY1 {
			break
		}
		switch r.Kind {
		case state.RowText:
			g.hits.Add(state.Hit{X0: gm.chanR.x0, Y0: y, X1: gm.chanR.x1, Y1: y + gm.lh, Kind: state.HitChannel, ID: r.Index})
		case state.RowVoice:
			g.hits.Add(state.Hit{X0: gm.chanR.x0, Y0: y, X1: gm.chanR.x1, Y1: y + gm.lh, Kind: state.HitVoice, Key: r.Channel})
		}
		y += gm.lh
	}
	g.hits.Add(state.Hit{X0: gm.inputR.x0, Y0: gm.inputR.y0, X1: gm.inputR.x1, Y1: gm.inputR.y1, Kind: state.HitText})
	g.addBarHits(gm)
	g.addSignalHits(gm) // last: the open panel wins over anything under it
}

// joinChannel joins ch on s in the background unless it is already
// joined/joining.
func (g *game) joinChannel(s *session, ch string) {
	if s.c == nil || s.ms.joined[ch] {
		return
	}
	s.ms.joined[ch] = true
	g.logf("joining %s on %s", ch, s.url)
	go func(key string, c *client.Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.joinCh <- joinResult{key, ch, c.Join(ctx, ch)}
	}(s.key, s.c)
}

func (g *game) switchChannel(next bool) {
	st := g.cur().st
	if len(st.Channels) < 2 {
		return
	}
	if next {
		st.NextChannel()
	} else {
		st.PrevChannel()
	}
	g.afterChannelChange()
}

// selectChannel activates channel index i (mouse click), sharing the
// keyboard path's reset-and-join behaviour.
func (g *game) selectChannel(i int) {
	st := g.cur().st
	if i < 0 || i >= len(st.Channels) || i == st.Active {
		return
	}
	st.Active = i
	g.afterChannelChange()
}

func (g *game) afterChannelChange() {
	s := g.cur()
	s.ms.scroll, s.ms.prevTotal, s.ms.wheelCarry = 0, 0, 0
	ch := s.st.Channels[s.st.Active]
	s.st.MarkRead(ch)
	g.joinChannel(s, ch)
	g.saveServer(s, ch)
}

func (g *game) sendChat() {
	s := g.cur()
	txt := s.st.Input.Submit()
	s.ms.scroll = 0
	if txt == "" || s.c == nil || len(s.st.Channels) == 0 {
		return
	}
	ch := s.st.Channels[s.st.Active]
	go func(key string, c *client.Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.sendCh <- sendResult{key, c.SendChat(ctx, ch, txt)}
	}(s.key, s.c)
}

func enterPressed() bool {
	return inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter)
}

// updateMain consumes keyboard/mouse input for PhaseMain and refreshes the
// cached scrollback layout.
func (g *game) updateMain() {
	gm := g.geom()
	s := g.cur() // fixed for this tick: nothing below changes the view
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)
	shift := ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight)

	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyTab):
		g.switchChannel(!shift)
	case ctrl && inpututil.IsKeyJustPressed(ebiten.KeyArrowDown):
		g.switchChannel(true)
	case ctrl && inpututil.IsKeyJustPressed(ebiten.KeyArrowUp):
		g.switchChannel(false)
	}

	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if h, ok := g.hitAt(); ok {
			switch h.Kind {
			case state.HitChannel:
				g.selectChannel(h.ID)
			case state.HitVoice:
				g.clickVoice(h.Key)
			case state.HitSignal:
				if h.ID == signalBars {
					g.toggleQuality()
				}
			}
		}
	}

	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= ' ' && r != 0x7f && len([]rune(s.st.Input.String())) < maxInputRunes {
			s.st.Input.AppendRunes([]rune{r})
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		s.st.Input.Backspace()
	} else if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); d > 30 && d%3 == 0 {
		s.st.Input.Backspace()
	}
	g.mainEsc(s, g.escPressed())
	if enterPressed() {
		g.sendChat()
	}

	// Layout the active channel and apply scroll input.
	var msgs []state.Message
	if len(s.st.Channels) > 0 {
		msgs = s.st.Messages[s.st.Channels[s.st.Active]]
	}
	ch := ""
	if len(s.st.Channels) > 0 {
		ch = s.st.Channels[s.st.Active]
	}
	// Re-wrap only when the layout key changes; colours are applied at draw.
	var relaid bool
	s.ms.lines, relaid = s.ms.layout.Lines(ch, msgs, gm.cols, g.th.FontSize)
	if relaid {
		s.ms.layoutGen++
	}
	total := len(s.ms.lines)
	if s.ms.scroll > 0 && total > s.ms.prevTotal {
		s.ms.scroll += total - s.ms.prevTotal // keep the view stable while scrolled up
	}
	s.ms.prevTotal = total
	if inpututil.IsKeyJustPressed(ebiten.KeyPageUp) {
		s.ms.scroll += gm.visible - 1
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageDown) {
		s.ms.scroll -= gm.visible - 1
	}
	mx, my := g.cursor()
	fx, fy := float64(mx), float64(my)
	if _, wy := ebiten.Wheel(); wy != 0 && state.InRect(fx, fy, gm.chatR.x0, gm.chatR.y0, gm.chatR.x1, gm.chatR.y1) {
		var n int
		n, s.ms.wheelCarry = state.WheelLines(s.ms.wheelCarry, wy, wheelLinesPerNotch)
		s.ms.scroll += n
	}
	s.ms.scroll = state.ClampScroll(total, gm.visible, s.ms.scroll)
	s.ms.visible = gm.visible
}

// updateDisconnected retries the viewed session now on Enter.
func (g *game) updateDisconnected() {
	if enterPressed() {
		g.retryNow(g.curKey)
	}
}

// lostText is the disconnected-screen headline for s at now.
func lostText(s *session, now time.Time) string {
	if s.dialing {
		return "reconnecting..."
	}
	n := 0
	if !s.retryAt.IsZero() {
		n = int(math.Ceil(s.retryAt.Sub(now).Seconds()))
	}
	if n < 0 {
		n = 0
	}
	return fmt.Sprintf("CONNECTION LOST — reconnecting in %ds, Enter to retry now", n)
}

func (g *game) connStatus() string {
	s := g.cur()
	switch {
	case s.dialing:
		return "reconnecting..."
	case s.st.Phase == state.PhaseDisconnected:
		return "disconnected"
	default:
		return "connected"
	}
}

func (g *game) drawMain(screen *ebiten.Image) {
	th := g.th
	gm := g.geom()
	g.drawPanelR(screen, gm.statusR)
	g.drawStatus(screen, gm)

	if s := g.cur(); s.st.Phase == state.PhaseDisconnected {
		msg := lostText(s, time.Now())
		sub := ""
		if !s.dialing && s.st.ConnectErr != "" {
			sub = "error: " + s.st.ConnectErr
		}
		g.drawCentered(screen, msg, float64(g.h)/2-gm.lh, th.Bright)
		if sub != "" {
			g.drawCentered(screen, sub, float64(g.h)/2, th.Dim)
		}
		g.drawCallBar(screen, gm) // a call on another server keeps its bar
		return
	}

	// Panels separate the areas; the gaps between them are the only dividers.
	g.drawPanelR(screen, gm.chanR)
	g.drawPanelR(screen, gm.chatR)
	g.drawPanelR(screen, gm.inputR)
	g.drawChannels(screen, gm)
	g.drawCallBar(screen, gm)
	g.drawScrollback(screen, gm)
	g.drawInput(screen, gm)
	g.drawQualityPanel(screen, gm)
}

// drawCallBar draws the call bar panel while it is (sliding) in. The bar is the
// bottom strip of geom(), so every scene but settings can show it.
func (g *game) drawCallBar(screen *ebiten.Image, gm geometry) {
	if g.barFrac() > 0 {
		g.drawPanelR(screen, gm.barR)
		g.drawBar(screen, gm)
	}
}

// clip returns a sub-image so text cannot bleed outside its pane.
func clip(img *ebiten.Image, x0, y0, x1, y1 float64) *ebiten.Image {
	b := img.Bounds()
	r := b.Intersect(image.Rect(int(x0), int(y0), int(x1), int(y1)))
	return img.SubImage(r).(*ebiten.Image)
}

func (g *game) drawCentered(screen *ebiten.Image, s string, y float64, c color.RGBA) {
	w := text.Advance(s, g.face)
	g.drawText(screen, s, (float64(g.w)-w)/2, y, c)
}

func (g *game) drawStatus(screen *ebiten.Image, gm geometry) {
	fp := g.fp
	if len(fp) > 16 {
		fp = fp[:16]
	}
	s := g.cur()
	line := fmt.Sprintf("%s  |  %s (%s)  |  %s", s.url, s.name, fp, g.connStatus())
	if s.st.Status != "" && s.st.Phase == state.PhaseMain {
		line += "  |  " + s.st.Status
	}
	r := gm.statusR
	textR := r.x1 - pad
	if !g.signalOnBar() { // otherwise the bars live on the call bar
		textR -= signalW(gm.lh) + pad
		sr := g.signalRect(gm)
		g.drawSignal(screen, sr.x0, sr.y0, gm.lh, g.q.grade)
	}
	g.drawTextF(clip(screen, r.x0+pad, r.y0, textR, r.y1), g.faceS, line, r.x0+pad, gm.statusY, g.th.Dim)
}

func (g *game) drawChannels(screen *ebiten.Image, gm geometry) {
	th := g.th
	cx0 := gm.chanR.x0
	dst := clip(screen, cx0, gm.paneY0, gm.chanR.x1, gm.chanY1)
	y := gm.paneY0
	st := g.cur().st
	for _, row := range st.Rows() {
		if y+gm.lh > gm.chanY1 {
			break
		}
		x := cx0 + pad
		switch row.Kind {
		case state.RowText:
			g.drawRowHL(dst, textRowKey(row.Channel), cx0, y, gm)
			ch := row.Channel
			count := fmt.Sprintf("%d", len(st.Members[ch]))
			name := ch
			marker, col := "  ", th.FG
			if st.Unread[ch] {
				col = th.Bright
			}
			if row.Index == st.Active {
				marker, col = "> ", th.Bright
			}
			room := gm.lcols - len(marker) - len(count) - 1
			if r := []rune(name); room > 0 && len(r) > room {
				name = string(r[:room])
			}
			g.drawText(dst, marker+name, x, y, col)
			cw := text.Advance(count, g.face)
			g.drawText(dst, count, cx0+channelPaneW-pad-cw, y, col)
		case state.RowDivider:
			label := "voice"
			lw := text.Advance(label, g.face)
			mid := y + gm.lh/2
			gap := float64(pad) / 2
			side := (channelPaneW - 2*pad - lw - 2*gap) / 2
			fadeRule(dst, x, mid, side, th.Dim, true)
			g.drawText(dst, label, x+side+gap, y, th.Dim)
			fadeRule(dst, x+side+2*gap+lw, mid, side, th.Dim, false)
		case state.RowVoice:
			g.drawRowHL(dst, voiceRowKey(row.Channel), cx0, y, gm)
			col := th.FG
			if row.Channel == st.InCall {
				col = th.Bright
			}
			drawSpeaker(dst, float32(x), float32(y), float32(gm.lh), col)
			name := row.Channel
			room := gm.lcols - 3
			if r := []rune(name); room > 0 && len(r) > room {
				name = string(r[:room])
			}
			g.drawText(dst, name, x+gm.lh+4, y, col)
		case state.RowOccupant:
			ox := x + gm.adv*2
			col := th.FG
			if row.Muted {
				col = th.Dim
			} else if e := g.speakEnv[row.FP]; e != nil {
				// Faint glow that fades with the envelope; the text keeps its
				// normal colour (no inversion).
				if a := glowAlpha(e.Value()); a*255 >= 1 {
					glow := color.RGBA{th.Bright.R, th.Bright.G, th.Bright.B, uint8(a * 255)}
					vector.FillRect(dst, float32(ox-2), float32(y), float32(channelPaneW-pad-(ox-cx0)+2), float32(gm.lh), premul(glow), false)
					col = th.Bright
				}
			}
			name := row.Name
			room := gm.lcols - 2 - 3
			if r := []rune(name); room > 0 && len(r) > room {
				name = string(r[:room])
			}
			g.drawText(dst, name, ox, y, col)
			if row.Muted {
				drawMic(dst, float32(cx0+channelPaneW-pad-gm.lh), float32(y), float32(gm.lh), th.Dim, true)
			}
		}
		y += gm.lh
	}
}

// drawRowHL fills a channel row with its eased hover/active highlight.
func (g *game) drawRowHL(dst *ebiten.Image, key string, cx0, y float64, gm geometry) {
	v := g.rowHL[key]
	if v*hoverAlpha*255 < 1 {
		return
	}
	b := g.th.Bright
	c := color.RGBA{b.R, b.G, b.B, uint8(v * hoverAlpha * 255)}
	vector.FillRect(dst, float32(cx0+2), float32(y), float32(channelPaneW-4), float32(gm.lh), premul(c), false)
}

// ruleAlphas are the three alpha steps of a faded divider rule, outer to inner.
var ruleAlphas = [3]uint8{50, 100, 170}

// fadeRule draws a 1px rule of width w at (x, y) in three alpha steps. With
// towardEnd the rule is strongest at its right end, otherwise at its left, so
// both rules around a label fade away from it.
func fadeRule(dst *ebiten.Image, x, y, w float64, c color.RGBA, towardEnd bool) {
	seg := w / 3
	for i, a := range ruleAlphas {
		pos := i
		if !towardEnd {
			pos = 2 - i
		}
		cc := color.RGBA{c.R, c.G, c.B, uint8(uint32(c.A) * uint32(a) / 255)}
		vector.FillRect(dst, float32(x+float64(pos)*seg), float32(y), float32(seg), 1, premul(cc), false)
	}
}

// speakLevel is fp's current output level; own level comes from the mic
// meter (playback never carries our own voice back).
func (g *game) speakLevel(fp string) float64 {
	if fp == g.fp {
		if g.vc.muted {
			return 0
		}
		return g.vc.mic.Level()
	}
	return g.vc.spk.Level(fp)
}

// premul converts a straight-alpha colour to the premultiplied form Ebiten's
// vector drawing expects.
func premul(c color.RGBA) color.RGBA {
	a := uint32(c.A)
	return color.RGBA{uint8(uint32(c.R) * a / 255), uint8(uint32(c.G) * a / 255), uint8(uint32(c.B) * a / 255), c.A}
}

// sbKey is everything the rendered scrollback pane depends on. The pane is
// redrawn into an offscreen image only when it changes; every other frame is a
// single blit instead of per-glyph draws (the dominant idle cost).
type sbKey struct {
	gen        int // layout generation: bumps whenever the wrapped lines change
	off        int
	w, h       int
	adv, lh    float64
	fg, br, dm color.RGBA
}

func (g *game) drawScrollback(screen *ebiten.Image, gm geometry) {
	r := image.Rect(int(gm.rightX), int(gm.paneY0), int(gm.rightX+gm.rightW), int(gm.paneY1)).Intersect(screen.Bounds())
	if r.Empty() {
		return
	}
	ms := &g.cur().ms
	k := sbKey{ms.layoutGen, state.ClampScroll(len(ms.lines), gm.visible, ms.scroll), r.Dx(), r.Dy(), gm.adv, gm.lh, g.th.FG, g.th.Bright, g.th.Dim}
	if ms.sbImg == nil || k != ms.sbKey {
		if ms.sbImg == nil || ms.sbImg.Bounds().Dx() != k.w || ms.sbImg.Bounds().Dy() != k.h {
			if ms.sbImg != nil {
				ms.sbImg.Deallocate()
			}
			ms.sbImg = ebiten.NewImage(k.w, k.h)
		} else {
			ms.sbImg.Clear()
		}
		ms.sbKey = k
		g.renderScrollback(ms.sbImg, gm, float64(r.Min.X), float64(r.Min.Y))
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(r.Min.X), float64(r.Min.Y))
	screen.DrawImage(ms.sbImg, op)
}

// renderScrollback draws the visible scrollback lines into dst, whose origin
// is the pane's top-left at (ox, oy) in screen coordinates.
func (g *game) renderScrollback(dst *ebiten.Image, gm geometry, ox, oy float64) {
	th := g.th
	gm.rightX -= ox
	gm.paneY0 -= oy
	gm.paneY1 -= oy
	ms := &g.cur().ms
	lines := ms.lines
	total := len(lines)
	off := state.ClampScroll(total, gm.visible, ms.scroll)
	end := total - off
	start := end - gm.visible
	if start < 0 {
		start = 0
	}
	y := gm.paneY0
	for _, l := range lines[start:end] {
		x := gm.rightX + float64(l.Indent)*gm.adv
		if l.Stamp != "" {
			g.drawText(dst, l.Stamp, x, y, th.Dim)
			x += float64(len([]rune(l.Stamp))) * gm.adv
			g.drawText(dst, l.Name, x, y, th.Bright)
			x += float64(len([]rune(l.Name))) * gm.adv
		}
		g.drawText(dst, l.Text, x, y, th.FG)
		y += gm.lh
	}
	if off > 0 {
		ind := fmt.Sprintf("-- %d lines up (PgDn) --", off)
		g.drawText(dst, ind, float64(dst.Bounds().Dx())-text.Advance(ind, g.face), gm.paneY1-gm.lh, th.Dim)
	}
}

func (g *game) drawInput(screen *ebiten.Image, gm geometry) {
	th := g.th
	dst := clip(screen, gm.rightX, gm.inputR.y0, gm.rightX+gm.rightW, gm.inputR.y1)
	in := []rune(g.cur().st.Input.String())
	room := int(gm.rightW/gm.adv) - 3 // prompt + cursor
	if room < 1 {
		room = 1
	}
	if len(in) > room {
		in = in[len(in)-room:]
	}
	s := "> " + string(in)
	g.drawText(dst, s, gm.rightX, gm.inputY, th.FG)
	if (g.frame/30)%2 == 0 {
		cx := gm.rightX + float64(len([]rune(s)))*gm.adv
		vector.FillRect(dst, float32(cx), float32(gm.inputY+2), float32(gm.adv), float32(g.th.FontSize), th.Bright, false)
	}
}

// mainEsc handles an Esc press in the main scene: it closes the quality panel,
// else submits (clears) the draft. A press the menu already took is spent.
func (g *game) mainEsc(s *session, pressed bool) {
	if !pressed || g.escSpent {
		return
	}
	g.escSpent = true
	if g.q.open {
		g.q.open = false
		return
	}
	s.st.Input.Submit()
}
