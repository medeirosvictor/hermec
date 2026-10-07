package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
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
)

// joinResult is the outcome of a background Join.
type joinResult struct {
	channel string
	err     error
}

// mainScene is the per-connection view state of the chat scene.
type mainScene struct {
	joined    map[string]bool
	scroll    int // lines scrolled up from the bottom; 0 = pinned
	prevTotal int
	lines     []state.Line // laid out in updateMain, drawn in drawMain
	visible   int
}

func newMainScene() mainScene { return mainScene{joined: map[string]bool{}} }

// geometry is the pixel layout of the main scene for the current window.
type geometry struct {
	w, h                 int
	statusY              float64
	paneY0, paneY1       float64 // scrollback/channel vertical extent
	inputY               float64
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
	gm := geometry{w: g.w, h: g.h, adv: adv, lh: lh}
	gm.statusY = pad
	gm.paneY0 = gm.statusY + lh + pad
	gm.inputY = float64(g.h) - pad - lh
	gm.paneY1 = gm.inputY - pad
	gm.rightX = channelPaneW + pad
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
	g.ms.scroll, g.ms.prevTotal = 0, 0
	g.joinChannel(g.st.Channels[g.st.Active])
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
	g.ms.lines = state.Scrollback(msgs, gm.cols)
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
	if _, wy := ebiten.Wheel(); wy != 0 {
		g.ms.scroll += int(wy * 3)
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
	vector.FillRect(screen, 0, float32(gm.paneY0-pad/2), float32(g.w), 1, th.Dim, false)
	vector.FillRect(screen, channelPaneW, float32(gm.paneY0-pad/2), 1, float32(gm.paneY1-gm.paneY0+pad/2), th.Dim, false)
	vector.FillRect(screen, 0, float32(gm.paneY1+pad/2), float32(g.w), 1, th.Dim, false)

	g.drawChannels(screen, gm)
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
	g.drawText(clip(screen, 0, 0, float64(g.w), gm.paneY0-pad/2), line, pad, gm.statusY, g.th.Dim)
}

func (g *game) drawChannels(screen *ebiten.Image, gm geometry) {
	th := g.th
	dst := clip(screen, 0, gm.paneY0, channelPaneW, gm.paneY1)
	y := gm.paneY0
	for i, ch := range g.st.Channels {
		if y+gm.lh > gm.paneY1 {
			break
		}
		count := fmt.Sprintf("%d", len(g.st.Members[ch]))
		name := ch
		marker, col := "  ", th.FG
		if i == g.st.Active {
			marker, col = "> ", th.Bright
		}
		room := gm.lcols - len(marker) - len(count) - 1
		if r := []rune(name); room > 0 && len(r) > room {
			name = string(r[:room])
		}
		g.drawText(dst, marker+name, pad, y, col)
		cw := text.Advance(count, g.face)
		g.drawText(dst, count, channelPaneW-pad-cw, y, col)
		y += gm.lh
	}
}

func (g *game) drawScrollback(screen *ebiten.Image, gm geometry) {
	th := g.th
	dst := clip(screen, gm.rightX, gm.paneY0, float64(g.w), gm.paneY1)
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
		g.drawText(dst, ind, float64(g.w)-pad-text.Advance(ind, g.face), gm.paneY1-gm.lh, th.Dim)
	}
}

func (g *game) drawInput(screen *ebiten.Image, gm geometry) {
	th := g.th
	dst := clip(screen, 0, gm.inputY, float64(g.w), float64(g.h))
	in := []rune(g.st.Input.String())
	room := int((float64(g.w)-2*pad)/gm.adv) - 3 // prompt + cursor
	if room < 1 {
		room = 1
	}
	if len(in) > room {
		in = in[len(in)-room:]
	}
	s := "> " + string(in)
	g.drawText(dst, s, pad, gm.inputY, th.FG)
	if (g.frame/30)%2 == 0 {
		cx := pad + float64(len([]rune(s)))*gm.adv
		vector.FillRect(dst, float32(cx), float32(gm.inputY+2), float32(gm.adv), float32(g.th.FontSize), th.Bright, false)
	}
}
