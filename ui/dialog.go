package ui

import (
	"fmt"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/ui/state"
)

// dialogMode says which modal dialog, if any, is open.
type dialogMode int

const (
	dlgNone dialogMode = iota
	dlgRename
	dlgLeave
)

// dialog is the open modal dialog; key is the saved-list key it is for.
type dialog struct {
	mode dialogMode
	key  string
	nick field // rename: the text being edited
}

const (
	dialogCols  = 48 // preferred panel width in glyph columns
	renameHint  = "Only you see this. Leave empty to use the server's name."
	leaveSuffix = "You can add it back later."
)

// title is the dialog heading for entry e.
func (d dialog) title(e state.ServerEntry) string {
	if d.mode == dlgRename {
		return "Rename " + state.DisplayName(e)
	}
	return "Leave Server"
}

// leaveSuffixFor is the small print under the leave question for key.
func (g *game) leaveSuffixFor(key string) string {
	if key == hostedKey {
		return "This stops hosting and deletes this server's settings."
	}
	return leaveSuffix
}

func (g *game) dialogOpen() bool { return g.dlg.mode != dlgNone }

// entryFor is the saved entry with key, if any.
func (g *game) entryFor(key string) (state.ServerEntry, bool) {
	for _, e := range g.servers {
		if e.URL == key {
			return e, true
		}
	}
	return state.ServerEntry{}, false
}

// openRenameDialog opens the rename dialog for a saved server, prefilled with
// its current nickname. An unknown key does nothing.
func (g *game) openRenameDialog(key string) {
	e, ok := g.entryFor(key)
	if !ok {
		return
	}
	g.dlg = dialog{mode: dlgRename, key: key, nick: field{runes: []rune(e.Nick)}}
}

// openLeaveDialog opens the leave confirmation for a saved server.
func (g *game) openLeaveDialog(key string) {
	if _, ok := g.entryFor(key); !ok {
		return
	}
	g.dlg = dialog{mode: dlgLeave, key: key}
}

// persistServers writes the saved list; a failure goes on the status line the
// way saveServer reports it.
func (g *game) persistServers() {
	if g.serversPath == "" {
		return
	}
	if err := state.SaveServers(g.serversPath, g.servers); err != nil {
		g.notice = fmt.Sprintf("save servers: %v", err)
		g.cur().st.Status = g.notice
	} else {
		g.notice = ""
	}
}

// dialogConfirm applies the open dialog (Save or Leave) and closes it.
func (g *game) dialogConfirm() {
	d := g.dlg
	g.dlg = dialog{}
	switch d.mode {
	case dlgRename:
		g.servers = state.SetNick(g.servers, d.key, d.nick.String())
		g.persistServers()
	case dlgLeave:
		if d.key == hostedKey {
			g.leaveHosted()
		}
		if g.sess[d.key] != nil {
			g.disconnect(d.key)
		}
		g.servers = state.Remove(g.servers, d.key)
		g.persistServers()
		g.hover = state.Hover{}
		g.tip = tooltipState{}
	}
}

// dialogCancel closes the open dialog without changing anything.
func (g *game) dialogCancel() { g.dlg = dialog{} }

// dialogKey applies one tick of keyboard input to the open dialog. Esc cancels
// (and is spent), Enter confirms, typed runes edit the rename field.
func (g *game) dialogKey(esc, enter bool, typed []rune, backspace bool) {
	g.dialogKeys(esc, enter, editKeys{backspace: backspace}, typed)
}

// dialogKeys is dialogKey with the full set of editing keys.
func (g *game) dialogKeys(esc, enter bool, k editKeys, typed []rune) {
	if !g.dialogOpen() {
		return
	}
	if esc {
		g.escSpent = true
		g.dialogCancel()
		return
	}
	if g.dlg.mode == dlgRename {
		g.dlg.nick.edit(k, typed, state.MaxNickRunes, nil, false)
	}
	if enter {
		g.dialogConfirm()
	}
}

// dialogPress handles a left press at (x, y) while a dialog is open: a button
// runs, elsewhere in the panel is ignored, outside it cancels. The press is
// always spent so no handler under the dialog acts on it.
func (g *game) dialogPress(x, y float64) {
	h, ok := g.hitFor(x, y)
	switch {
	case ok && h.Kind == state.HitDialogBtn && h.ID == 0:
		g.dialogConfirm()
	case ok && h.Kind == state.HitDialogBtn && h.ID == 1:
		g.dialogCancel()
	default:
		r := g.dialogRect()
		if !state.InRect(x, y, r.x0, r.y0, r.x1, r.y1) {
			g.dialogCancel()
		}
	}
	g.hits.Reset()
}

// updateDialog reads the dialog's input and reports whether a dialog was open
// this tick; Update then skips every input handler under it (but none of the
// per-tick work).
func (g *game) updateDialog() bool {
	if !g.dialogOpen() {
		return false
	}
	g.dialogKeys(g.escPressed(), enterPressed(), readEditKeys(), ebiten.AppendInputChars(nil))
	if g.dialogOpen() && inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		g.dialogPress(g.cursor())
	}
	return true
}

func (g *game) glyphW() float64 { return text.Advance("M", g.face) }

// dialogRect is the centred panel, clamped inside the window.
func (g *game) dialogRect() rectF {
	lh := g.lineH()
	w := float64(dialogCols)*g.glyphW() + 2*pad
	h := pad + lh + pad + lh + pad/2 + lh + pad + lh + pad // title, field or message, hint, buttons
	w, h = min(w, float64(g.w)), min(h, float64(g.h))
	x0, y0 := (float64(g.w)-w)/2, (float64(g.h)-h)/2
	return rectF{x0, y0, x0 + w, y0 + h}
}

// dialogButtonRect is button i (0 primary, 1 Cancel), right-aligned along the
// bottom of the panel, Cancel last.
func (g *game) dialogButtonRect(i int) rectF {
	r := g.dialogRect()
	h := g.lineH() + pad/2
	y1 := r.y1 - pad/2
	y0 := max(r.y0, y1-h)
	cx1 := r.x1 - pad/2
	cw := text.Advance("Cancel", g.face) + pad
	pw := text.Advance(g.primaryLabel(), g.face) + pad
	if i == 1 {
		return rectF{max(r.x0, cx1-cw), y0, cx1, y1}
	}
	x1 := cx1 - cw - pad/2
	return rectF{max(r.x0, x1-pw), y0, max(r.x0, x1), y1}
}

func (g *game) primaryLabel() string {
	if g.dlg.mode == dlgLeave {
		return "Leave"
	}
	return "Save"
}

// addDialogHits registers the two buttons; buildHits adds them last so they win.
func (g *game) addDialogHits() {
	if !g.dialogOpen() {
		return
	}
	for i := 0; i < 2; i++ {
		r := g.dialogButtonRect(i)
		g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: state.HitDialogBtn, ID: i})
	}
}

// drawDialog draws the open dialog over everything, behind a dimming scrim.
func (g *game) drawDialog(screen *ebiten.Image) {
	if !g.dialogOpen() {
		return
	}
	th := g.th
	e, _ := g.entryFor(g.dlg.key)
	vector.FillRect(screen, 0, 0, float32(g.w), float32(g.h), premul(color.RGBA{th.BG.R, th.BG.G, th.BG.B, 170}), false)
	r := g.dialogRect()
	g.drawPanelFade(screen, r.x0, r.y0, r.x1, r.y1, th.Panel, 4)
	lh := g.lineH()
	cols := max(int((r.x1-r.x0-2*pad)/g.glyphW()), 4)
	x, y := r.x0+pad, r.y0+pad
	if g.dlg.mode == dlgRename {
		g.drawText(screen, state.Ellipsize(g.dlg.title(e), cols), x, y, th.Bright)
		y += lh + pad
		in, c := g.dlg.nick.runes, g.dlg.nick.caret()
		room := max(cols-3, 1) // prompt and cursor
		st := window(len(in), c, room)
		in = in[st:min(len(in), st+room)]
		g.drawText(screen, "> "+string(in), x, y, th.FG)
		g.drawCaret(screen, x, y, "> "+string(g.dlg.nick.runes[st:c]))
		y += lh + pad/2
		g.drawTextF(screen, g.faceS, state.Ellipsize(renameHint, int(float64(cols)/smallScale)), x, y, th.Dim)
	} else {
		g.drawText(screen, state.Ellipsize(g.dlg.title(e), cols), x, y, th.Bright)
		y += lh + pad
		g.drawText(screen, state.Ellipsize("Leave "+state.DisplayName(e)+"?", cols), x, y, th.FG)
		y += lh
		g.drawTextF(screen, g.faceS, state.Ellipsize(g.leaveSuffixFor(g.dlg.key), int(float64(cols)/smallScale)), x, y, th.Dim)
	}
	hover := -1
	if h, ok := g.hitAt(); ok && h.Kind == state.HitDialogBtn {
		hover = h.ID
	}
	for i, label := range []string{g.primaryLabel(), "Cancel"} {
		b := g.dialogButtonRect(i)
		txt := th.FG
		if i == 0 {
			txt = th.Bright
		}
		if i == hover {
			c, a := th.Bright, hoverAlpha*255
			vector.FillRect(screen, float32(b.x0), float32(b.y0), float32(b.x1-b.x0), float32(b.y1-b.y0),
				premul(color.RGBA{c.R, c.G, c.B, uint8(a)}), false)
			txt = th.Bright
		}
		tw := text.Advance(label, g.face)
		g.drawText(screen, label, b.x0+(b.x1-b.x0-tw)/2, b.y0+(b.y1-b.y0-lh)/2, txt)
	}
}
