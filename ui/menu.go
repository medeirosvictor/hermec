package ui

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/ui/state"
)

// menuItem is one row of a context menu: a label and what picking it does.
// The menu closes itself after act runs.
type menuItem struct {
	label string
	act   func()
	sep   bool // a thin separator is drawn above this row
	warn  bool // draw in th.Bright (a destructive action)
}

// sepH is the height of the separator gap above a sep row.
const sepH = 5

// ctxMenu is the small generic context menu. key is what it was opened for
// (a saved-list key); items are whatever the opener put there, so later rounds
// can add rows without touching the menu itself.
type ctxMenu struct {
	open  bool
	key   string
	x, y  float64 // top-left, already clamped inside the window
	items []menuItem
}

// openTileMenu opens the menu for a rail tile at (ax, ay): Retry while the
// session's last dial failed, Rename, Disconnect while a session exists, and
// Leave Server.
func (g *game) openTileMenu(key string, ax, ay float64) {
	g.closeMenu()
	var items []menuItem
	if s := g.sess[key]; s != nil && s.failed && !g.hostedStopped(key) {
		items = append(items, menuItem{label: "Retry", act: func() { g.retryNow(key) }})
	}
	if h := g.hosted; key == hostedKey && h != nil {
		if h.srv != nil {
			items = append(items,
				menuItem{label: "Copy invite address", act: g.copyInvite},
				menuItem{label: "Stop hosting", act: g.stopHosted})
		} else {
			items = append(items, menuItem{label: "Start hosting", act: g.startHostedNow})
		}
	}
	if it, ok := g.friendMenuItem(key); ok {
		items = append(items, it)
	}
	if id := g.serverIDFor(key); id != "" {
		items = append(items, menuItem{label: "id " + shortID(id), act: func() { g.copyID(id) }})
	}
	items = append(items, menuItem{label: "Rename", act: func() { g.openRenameDialog(key) }})
	if _, ok := g.sess[key]; ok {
		items = append(items, menuItem{label: "Disconnect", act: func() { g.disconnect(key) }})
	}
	items = append(items, menuItem{label: "Leave Server", sep: true, warn: true, act: func() { g.openLeaveDialog(key) }})
	for i := range items { // picking any item closes the menu first
		act := items[i].act
		items[i].act = func() { g.closeMenu(); act() }
	}
	w, h := g.menuSize(items)
	x, y := state.MenuOrigin(ax, ay, w, h, float64(g.w), float64(g.h))
	g.menu = ctxMenu{open: true, key: key, x: x, y: y, items: items}
}

func (g *game) closeMenu() { g.menu = ctxMenu{} }

func (g *game) menuRowH() float64 { return g.lineH() + pad }

// menuSize is the box that holds items: widest label plus padding, one row
// each, plus the gap above every separated row.
func (g *game) menuSize(items []menuItem) (w, h float64) {
	for _, it := range items {
		w = max(w, text.Advance(it.label, g.face))
		if it.sep {
			h += sepH
		}
	}
	return w + 2*pad, h + float64(len(items))*g.menuRowH()
}

// menuItemRect is the rect of item i of the open menu.
func (g *game) menuItemRect(i int) rectF {
	w, _ := g.menuSize(g.menu.items)
	y := g.menu.y + float64(i)*g.menuRowH()
	for _, it := range g.menu.items[:i+1] {
		if it.sep {
			y += sepH
		}
	}
	return rectF{g.menu.x, y, g.menu.x + w, y + g.menuRowH()}
}

// addMenuHits registers the open menu's rows. Called last in buildHits so they
// win over everything under them.
func (g *game) addMenuHits() {
	if !g.menu.open {
		return
	}
	for i := range g.menu.items {
		r := g.menuItemRect(i)
		g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: state.HitMenuItem, ID: i})
	}
}

// closeMenuOnEsc closes the open menu on an Esc press and marks the press
// spent, so no other Esc handler acts on it this tick. Update calls it once,
// before any scene handler runs.
func (g *game) closeMenuOnEsc(pressed bool) {
	if pressed && g.menu.open {
		g.closeMenu()
		g.escSpent = true
	}
}

// escPressed reports an Esc press no handler has consumed yet this tick.
func (g *game) escPressed() bool {
	return !g.escSpent && inpututil.IsKeyJustPressed(ebiten.KeyEscape)
}

// menuPress handles a left press at (x, y) while the menu is open: an item runs
// (and closes the menu), anywhere else just closes it. Either way the press is
// spent: the hit registry is cleared so no later handler acts on it, like a
// desktop context menu swallowing its dismissing click.
func (g *game) menuPress(x, y float64) {
	g.menuSpent = true
	if h, ok := g.hitFor(x, y); ok && h.Kind == state.HitMenuItem && h.ID >= 0 && h.ID < len(g.menu.items) {
		g.menu.items[h.ID].act() // closes the menu
	} else {
		g.closeMenu()
	}
	g.hits.Reset()
}

// updateMenu handles mouse input while the menu is open and reports whether a
// left click was spent on it (the rail should then ignore that click).
func (g *game) updateMenu() bool {
	if !g.menu.open {
		return false
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight) {
		g.closeMenu() // a right press on a tile may open a fresh menu after this
		return false
	}
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return false
	}
	g.menuPress(g.cursor())
	return true
}

// drawMenu draws the open menu on top of the scene.
func (g *game) drawMenu(screen *ebiten.Image) {
	if !g.menu.open {
		return
	}
	th := g.th
	w, h := g.menuSize(g.menu.items)
	g.drawPanelFade(screen, g.menu.x, g.menu.y, g.menu.x+w, g.menu.y+h, th.Panel, 4)
	hover := -1
	if hit, ok := g.hitAt(); ok && hit.Kind == state.HitMenuItem {
		hover = hit.ID
	}
	for i, it := range g.menu.items {
		r := g.menuItemRect(i)
		txt := th.FG
		if it.warn {
			txt = th.Bright
		}
		if it.sep {
			vector.FillRect(screen, float32(r.x0+pad/2), float32(r.y0-sepH/2), float32(r.x1-r.x0-pad), 1, th.Dim, false)
		}
		if i == hover {
			b, a := th.Bright, hoverAlpha*255
			vector.FillRect(screen, float32(r.x0+2), float32(r.y0), float32(r.x1-r.x0-4), float32(r.y1-r.y0),
				premul(color.RGBA{b.R, b.G, b.B, uint8(a)}), false)
			txt = th.Bright
		}
		g.drawText(screen, it.label, r.x0+pad, r.y0+(r.y1-r.y0-g.lineH())/2, txt)
	}
}
