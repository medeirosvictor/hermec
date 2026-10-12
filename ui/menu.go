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
}

// ctxMenu is the small generic context menu. key is what it was opened for
// (a saved-list key); items are whatever the opener put there, so later rounds
// can add rows without touching the menu itself.
type ctxMenu struct {
	open  bool
	key   string
	x, y  float64 // top-left, already clamped inside the window
	items []menuItem
}

// openTileMenu opens the menu for a rail tile at (ax, ay). A tile without a
// session has nothing to offer, so no menu opens.
func (g *game) openTileMenu(key string, ax, ay float64) {
	g.closeMenu()
	if _, ok := g.sess[key]; !ok {
		return
	}
	items := []menuItem{{label: "Disconnect", act: func() { g.disconnect(key) }}}
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

// menuSize is the box that holds items: widest label plus padding, one row each.
func (g *game) menuSize(items []menuItem) (w, h float64) {
	for _, it := range items {
		w = max(w, text.Advance(it.label, g.face))
	}
	return w + 2*pad, float64(len(items)) * g.menuRowH()
}

// menuItemRect is the rect of item i of the open menu.
func (g *game) menuItemRect(i int) rectF {
	w, _ := g.menuSize(g.menu.items)
	y := g.menu.y + float64(i)*g.menuRowH()
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

// escMenu closes the open menu on Esc and reports whether it took the key, so
// callers can skip their own Esc handling for that press.
func (g *game) escMenu() bool {
	if g.menu.open && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.closeMenu()
		return true
	}
	return false
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
	if h, ok := g.hitAt(); ok && h.Kind == state.HitMenuItem && h.ID >= 0 && h.ID < len(g.menu.items) {
		g.menu.items[h.ID].act() // closes the menu
	} else {
		g.closeMenu()
	}
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
		if i == hover {
			b, a := th.Bright, hoverAlpha*255
			vector.FillRect(screen, float32(r.x0+2), float32(r.y0), float32(r.x1-r.x0-4), float32(r.y1-r.y0),
				premul(color.RGBA{b.R, b.G, b.B, uint8(a)}), false)
			txt = th.Bright
		}
		g.drawText(screen, it.label, r.x0+pad, r.y0+(r.y1-r.y0-g.lineH())/2, txt)
	}
}
