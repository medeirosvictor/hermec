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
	pad           = 8
	maxInputRunes = 1000
	minCols       = 10

	wheelLinesPerNotch = 3
)

// joinResult is the outcome of a background Join.
type joinResult struct {
	channel string
	err     error
}

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
type geometry struct {
	w, h                 int
	ox                   float64 // left edge (rail width)
	statusY              float64
	paneY0, paneY1       float64 // scrollback (chat pane) vertical extent
	chanY1               float64 // channel pane bottom; runs below the chat pane, beside the input line
	inputY               float64 // chat input line: right column only, directly under the chat pane
	barY                 float64 // call bar top; meaningful only while barVisible
	rightX, rightW       float64
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
	gm.statusY = pad + g.bannerH()
	gm.paneY0 = gm.statusY + lh + pad
	// The call bar (when shown) is the full-width bottom strip; everything
	// above it is content. The input line sits under the right column only.
	gm.chanY1 = float64(g.h) - pad
	if g.barVisible() {
		gm.barY = float64(g.h) - pad - lh
		gm.chanY1 = gm.barY - pad
	}
	gm.inputY = gm.chanY1 - lh
	gm.paneY1 = gm.inputY - pad
	gm.rightX = gm.ox + channelPaneW + pad
	gm.rightW = float64(g.w) - gm.rightX - pad
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

// joinChannel joins ch in the background unless it is already joined/joining.
func (g *game) joinChannel(ch string) {
	if g.c == nil || g.ms.joined[ch] {
		return
	}
	g.ms.joined[ch] = true
	g.logf("joining %s", ch)
	go func(c *client.Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.joinCh <- joinResult{ch, c.Join(ctx, ch)}
	}(g.c)
}

func (g *game) switchChannel(next bool) {
	if len(g.st.Channels) < 2 {
		return
	}
	if next {
		g.st.NextChannel()
	} else {
		g.st.PrevChannel()
	}
	g.afterChannelChange()
}

// selectChannel activates channel index i (mouse click), sharing the
// keyboard path's reset-and-join behaviour.
func (g *game) selectChannel(i int) {
	if i < 0 || i >= len(g.st.Channels) || i == g.st.Active {
		return
	}
	g.st.Active = i
	g.afterChannelChange()
}

func (g *game) afterChannelChange() {
	g.ms.scroll, g.ms.prevTotal, g.ms.wheelCarry = 0, 0, 0
	g.joinChannel(g.st.Channels[g.st.Active])
	g.saveServer(g.st.Channels[g.st.Active])
}

func (g *game) sendChat() {
	txt := g.st.Input.Submit()
	g.ms.scroll = 0
	if txt == "" || g.c == nil || len(g.st.Channels) == 0 {
		return
	}
	ch := g.st.Channels[g.st.Active]
	go func(c *client.Client) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		g.sendCh <- c.SendChat(ctx, ch, txt)
	}(g.c)
}

func enterPressed() bool {
	return inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter)
}

// updateMain consumes keyboard/mouse input for PhaseMain and refreshes the
// cached scrollback layout.
func (g *game) updateMain() {
	gm := g.geom()
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
		cx, cy := ebiten.CursorPosition()
		x, y := float64(cx), float64(cy)
		if state.InRect(x, y, gm.ox, gm.paneY0, gm.ox+channelPaneW, gm.chanY1) {
			// Only rows drawChannels actually draws are clickable.
			rows := g.st.Rows()
			n := int((gm.chanY1 - gm.paneY0) / gm.lh)
			if n > len(rows) {
				n = len(rows)
			}
			if i := state.RowAt(y, gm.paneY0, gm.lh, n); i >= 0 {
				switch r := rows[i]; r.Kind {
				case state.RowText:
					g.selectChannel(r.Index)
				case state.RowVoice:
					g.clickVoice(r.Channel)
				}
			}
		}
	}
	g.updateBar(gm)

	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= ' ' && r != 0x7f && len([]rune(g.st.Input.String())) < maxInputRunes {
			g.st.Input.AppendRunes([]rune{r})
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		g.st.Input.Backspace()
	} else if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); d > 30 && d%3 == 0 {
		g.st.Input.Backspace()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.st.Input.Submit()
	}
	if enterPressed() {
		g.sendChat()
	}

	// Layout the active channel and apply scroll input.
	var msgs []state.Message
	if len(g.st.Channels) > 0 {
		msgs = g.st.Messages[g.st.Channels[g.st.Active]]
	}
	ch := ""
	if len(g.st.Channels) > 0 {
		ch = g.st.Channels[g.st.Active]
	}
	// Re-wrap only when the layout key changes; colours are applied at draw.
	var relaid bool
	g.ms.lines, relaid = g.ms.layout.Lines(ch, msgs, gm.cols, g.th.FontSize)
	if relaid {
		g.ms.layoutGen++
	}
	total := len(g.ms.lines)
	if g.ms.scroll > 0 && total > g.ms.prevTotal {
		g.ms.scroll += total - g.ms.prevTotal // keep the view stable while scrolled up
	}
	g.ms.prevTotal = total
	if inpututil.IsKeyJustPressed(ebiten.KeyPageUp) {
		g.ms.scroll += gm.visible - 1
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageDown) {
		g.ms.scroll -= gm.visible - 1
	}
	mx, my := ebiten.CursorPosition()
	fx, fy := float64(mx), float64(my)
	if _, wy := ebiten.Wheel(); wy != 0 && state.InRect(fx, fy, gm.rightX, gm.paneY0, float64(g.w), gm.paneY1) {
		var n int
		n, g.ms.wheelCarry = state.WheelLines(g.ms.wheelCarry, wy, wheelLinesPerNotch)
		g.ms.scroll += n
	}
	g.ms.scroll = state.ClampScroll(total, gm.visible, g.ms.scroll)
	g.ms.visible = gm.visible
}

// updateDisconnected reconnects on Enter.
func (g *game) updateDisconnected() {
	if !g.dialing && enterPressed() && g.lastURL != "" {
		g.startDial(g.lastURL, g.lastName)
	}
}

func (g *game) connStatus() string {
	switch {
	case g.dialing:
		return "reconnecting..."
	case g.st.Phase == state.PhaseDisconnected:
		return "disconnected"
	default:
		return "connected"
	}
}

func (g *game) drawMain(screen *ebiten.Image) {
	th := g.th
	gm := g.geom()
	g.drawStatus(screen, gm)

	if g.st.Phase == state.PhaseDisconnected {
		msg := "CONNECTION LOST — press Enter to reconnect"
		sub := ""
		switch {
		case g.dialing:
			msg = "reconnecting..."
		case g.st.ConnectErr != "":
			sub = "error: " + g.st.ConnectErr
		}
		g.drawCentered(screen, msg, float64(g.h)/2-gm.lh, th.Bright)
		if sub != "" {
			g.drawCentered(screen, sub, float64(g.h)/2, th.Dim)
		}
		return
	}

	// Separators.
	ox := float32(gm.ox)
	vector.FillRect(screen, ox, float32(gm.paneY0-pad/2), float32(g.w)-ox, 1, th.Dim, false)
	vector.FillRect(screen, ox+channelPaneW, float32(gm.paneY0-pad/2), 1, float32(gm.chanY1-gm.paneY0+pad/2), th.Dim, false)
	vector.FillRect(screen, ox+channelPaneW, float32(gm.paneY1+pad/2), float32(g.w)-ox-channelPaneW, 1, th.Dim, false)

	g.drawChannels(screen, gm)
	if g.barVisible() {
		g.drawBar(screen, gm)
		vector.FillRect(screen, ox, float32(gm.barY-pad/2), float32(g.w)-ox, 1, th.Dim, false)
	}
	g.drawScrollback(screen, gm)
	g.drawInput(screen, gm)
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
	line := fmt.Sprintf("%s  |  %s (%s)  |  %s", g.lastURL, g.lastName, fp, g.connStatus())
	if g.st.Status != "" && g.st.Phase == state.PhaseMain {
		line += "  |  " + g.st.Status
	}
	g.drawTextF(clip(screen, gm.ox, 0, float64(g.w), gm.paneY0-pad/2), g.faceS, line, gm.ox+pad, gm.statusY, g.th.Dim)
}

func (g *game) drawChannels(screen *ebiten.Image, gm geometry) {
	th := g.th
	dst := clip(screen, gm.ox, gm.paneY0, gm.ox+channelPaneW, gm.chanY1)
	y := gm.paneY0
	for _, row := range g.st.Rows() {
		if y+gm.lh > gm.chanY1 {
			break
		}
		x := gm.ox + pad
		switch row.Kind {
		case state.RowText:
			ch := row.Channel
			count := fmt.Sprintf("%d", len(g.st.Members[ch]))
			name := ch
			marker, col := "  ", th.FG
			if row.Index == g.st.Active {
				marker, col = "> ", th.Bright
			}
			room := gm.lcols - len(marker) - len(count) - 1
			if r := []rune(name); room > 0 && len(r) > room {
				name = string(r[:room])
			}
			g.drawText(dst, marker+name, x, y, col)
			cw := text.Advance(count, g.face)
			g.drawText(dst, count, gm.ox+channelPaneW-pad-cw, y, col)
		case state.RowDivider:
			label := "voice"
			lw := text.Advance(label, g.face)
			mid := y + gm.lh/2
			side := (channelPaneW - 2*pad - lw - 2*pad) / 2
			vector.FillRect(dst, float32(x), float32(mid), float32(side), 1, th.Dim, false)
			g.drawText(dst, label, x+side+pad, y, th.Dim)
			vector.FillRect(dst, float32(x+side+2*pad+lw), float32(mid), float32(side), 1, th.Dim, false)
		case state.RowVoice:
			col := th.FG
			if row.Channel == g.st.InCall {
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
			lvl := g.speakLevel(row.FP)
			col := th.FG
			if lvl > speakingLevel && !row.Muted {
				col = th.Bright
				a := uint8(40 + 160*math.Min(1, math.Sqrt(lvl)))
				glow := color.RGBA{th.Bright.R, th.Bright.G, th.Bright.B, a}
				vector.FillRect(dst, float32(ox-2), float32(y), float32(channelPaneW-pad-(ox-gm.ox)+2), float32(gm.lh), premul(glow), false)
				col = th.BG
			} else if row.Muted {
				col = th.Dim
			}
			name := row.Name
			room := gm.lcols - 2 - 3
			if r := []rune(name); room > 0 && len(r) > room {
				name = string(r[:room])
			}
			g.drawText(dst, name, ox, y, col)
			if row.Muted {
				drawMic(dst, float32(gm.ox+channelPaneW-pad-gm.lh), float32(y), float32(gm.lh), th.Dim, true)
			}
		}
		y += gm.lh
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
	r := image.Rect(int(gm.rightX), int(gm.paneY0), g.w, int(gm.paneY1)).Intersect(screen.Bounds())
	if r.Empty() {
		return
	}
	ms := &g.ms
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
	lines := g.ms.lines
	total := len(lines)
	off := state.ClampScroll(total, gm.visible, g.ms.scroll)
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
		g.drawText(dst, ind, float64(g.w)-ox-pad-text.Advance(ind, g.face), gm.paneY1-gm.lh, th.Dim)
	}
}

func (g *game) drawInput(screen *ebiten.Image, gm geometry) {
	th := g.th
	dst := clip(screen, gm.rightX, gm.inputY, float64(g.w), gm.inputY+gm.lh)
	in := []rune(g.st.Input.String())
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
