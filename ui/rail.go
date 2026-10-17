package ui

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

const (
	railWidth = 56.0
	railTileH = 56.0
	railInset = 6.0

	// localKey identifies the embedded -local server in servers.toml; its real
	// URL changes (random port) on every run.
	localKey = "hermec://local"
)

// railW is the rail's width, or 0 while there are no servers.
func (g *game) railW() float64 {
	if len(g.servers) == 0 {
		return 0
	}
	return railWidth
}

// railTiles is how many entries fit above the "+" and gear tiles.
func (g *game) railTiles() int {
	n := int((float64(g.h) - 2*railTileH - pad) / railTileH)
	if n > len(g.servers) {
		n = len(g.servers)
	}
	if n < 0 {
		n = 0
	}
	return n
}

// realURL maps the saved key of the local server to this run's URL.
func (g *game) realURL(u string) string {
	if u == localKey && g.localURL != "" {
		return g.localURL
	}
	return u
}

// keyFor is the inverse of realURL.
func (g *game) keyFor(u string) string {
	if g.localURL != "" && u == g.localURL {
		return localKey
	}
	return u
}

func (g *game) loadServers(path string) {
	if path == "" {
		p, err := DefaultServersPath()
		if err != nil {
			g.notice = fmt.Sprintf("servers: %v", err)
			return
		}
		path = p
	}
	g.serversPath = path
	es, err := state.LoadServers(path)
	if err != nil {
		// Keep the unreadable file around rather than overwrite it later.
		g.notice = fmt.Sprintf("servers.toml unreadable (%v); moved to .bad", err)
		_ = os.Rename(path, filepath.Clean(path)+".bad")
		return
	}
	g.servers = es
}

// serverNameOf is the name c's server announced, cleaned for display: the
// server is untrusted, so control and format runes go and the length is capped.
func serverNameOf(c *client.Client) string {
	return state.CleanName(c.ServerName(), state.MaxServerNameRunes)
}

// saveServer records s and its active channel in the saved list. Servers
// with a session are never evicted by the cap. Failures go to the status line.
func (g *game) saveServer(s *session, channel string) {
	if s == nil || s.key == "" || s.c == nil || g.serversPath == "" {
		return
	}
	label := state.HostPort(s.url)
	if s.key == localKey {
		label = "local"
	}
	live := func(u string) bool { _, ok := g.sess[u]; return ok }
	g.servers = state.Touch(g.servers, s.key, label, serverNameOf(s.c), channel, live)
	if err := state.SaveServers(g.serversPath, g.servers); err != nil {
		g.notice = fmt.Sprintf("save servers: %v", err)
		s.st.Status = g.notice
	} else {
		g.notice = ""
	}
}

// onConnected runs after a successful dial of s: it joins every text channel
// (so chat for all of them arrives and can count as unread), shows the
// remembered one if the server still has it, and persists the server.
func (g *game) onConnected(s *session) {
	chs := s.st.Channels
	idx := 0
	for _, e := range g.servers {
		if e.URL != s.key {
			continue
		}
		for i, ch := range chs {
			if ch == e.LastChannel {
				idx = i
			}
		}
	}
	ch := ""
	if len(chs) > 0 {
		s.st.Active = idx
		ch = chs[idx]
		if s.st.Viewed {
			s.st.MarkRead(ch)
		}
		g.joinChannel(s, ch) // the shown channel first
		for _, c := range chs {
			g.joinChannel(s, c)
		}
	}
	g.saveServer(s, ch)
}

// switchServer views the tile's session, creating and dialing it if there
// is none. It never closes another session.
func (g *game) switchServer(e state.ServerEntry) {
	if _, ok := g.sess[e.URL]; ok {
		g.view(e.URL)
		return
	}
	name := g.connect.name.String()
	if name == "" {
		name = g.opts.Name
	}
	g.connect = newConnectForm(name, g.realURL(e.URL), e.URL == localKey && g.localURL != "")
	key := ""
	if s := g.openSession(e.URL, name); s != nil {
		key = s.key
	}
	g.view(key)
}

// addServer shows a fresh connect form; every session keeps running.
func (g *game) addServer() {
	name := g.connect.name.String()
	if name == "" {
		name = g.opts.Name
	}
	g.view("")
	g.noneSession().st.ConnectErr = ""
	g.connect = newConnectForm(name, "ws://", false)
	g.connect.focus = 1
}

func (g *game) updateRail() {
	if g.railW() == 0 {
		return
	}
	right := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight)
	if !right && !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	h, ok := g.hitAt()
	if !ok && !right {
		return
	}
	x, y := g.cursor()
	g.clickRail(h, ok, right, x, y)
}

// clickRail handles a press on h (ok: there was a target under the pointer) at
// (x, y). A right press opens the tile menu; a left press acts on the target.
func (g *game) clickRail(h state.Hit, ok, right bool, x, y float64) {
	if right {
		if ok && (h.Kind == state.HitRailTile || h.Kind == state.HitRailRetry) && h.ID >= 0 && h.ID < len(g.servers) {
			g.openTileMenu(g.servers[h.ID].URL, x, y)
		}
		return
	}
	if !ok {
		return
	}
	switch h.Kind {
	case state.HitGear:
		g.toggleSettings()
	case state.HitRailAdd:
		g.closeSettings()
		g.addServer()
	case state.HitRailTile:
		g.closeSettings()
		g.switchServer(g.servers[h.ID])
	case state.HitRailRetry:
		if h.ID >= 0 && h.ID < len(g.servers) {
			g.retryNow(g.servers[h.ID].URL)
		}
	}
}

// addRailHits registers the server tiles, "+" and gear. Added in that order so
// the gear wins over "+" wins over tiles in a short window, like state.RailHit.
func (g *game) addRailHits() {
	if g.railW() == 0 {
		return
	}
	for i := 0; i < g.railTiles(); i++ {
		y := pad + float64(i)*railTileH
		g.hits.Add(state.Hit{X0: 0, Y0: y, X1: railWidth, Y1: y + railTileH, Kind: state.HitRailTile, ID: i})
	}
	for i := 0; i < g.railTiles(); i++ { // after the tiles: the last registered wins, so a badge beats its tile
		if s := g.sess[g.servers[i].URL]; s != nil && s.failed {
			x0, y0, x1, y1 := retryBadgeRect(pad + float64(i)*railTileH)
			g.hits.Add(state.Hit{X0: x0, Y0: y0, X1: x1, Y1: y1, Kind: state.HitRailRetry, ID: i})
		}
	}
	h := float64(g.h)
	g.hits.Add(state.Hit{X0: 0, Y0: h - 2*railTileH, X1: railWidth, Y1: h - railTileH, Kind: state.HitRailAdd})
	g.hits.Add(state.Hit{X0: 0, Y0: h - railTileH, X1: railWidth, Y1: h, Kind: state.HitGear})
}

// tooltipState is the rail tooltip: the tile it is for and whether it shows.
type tooltipState struct {
	idx    int
	show   bool
	anchor state.Hit // the hovered target; the fingerprint tooltip sits beside it
}

// stepTooltip advances the hover timer for the hit under the pointer. Only
// rail tiles get a tooltip, and none shows over the menu or a dialog.
func (g *game) stepTooltip(h state.Hit, ok bool, now time.Time) {
	id := -1
	switch {
	case ok && (h.Kind == state.HitRailTile || h.Kind == state.HitRailRetry): // the badge sits on its tile
		id = h.ID
	case ok && h.Kind == state.HitFingerprint:
		id = fpTipID
	}
	held := g.hover.Step(id, now)
	g.tip = tooltipState{idx: id, show: held && !g.menu.open && g.dlg.mode == dlgNone, anchor: h}
}

// tooltipText is the text of the visible tooltip. It re-checks the index
// because the saved list can shrink while a tooltip is up.
func (g *game) tooltipText() (string, bool) {
	if g.tip.show && g.tip.idx == fpTipID {
		return fingerprintTip, true
	}
	if !g.tip.show || g.tip.idx < 0 || g.tip.idx >= len(g.servers) {
		return "", false
	}
	e := g.servers[g.tip.idx]
	name := state.DisplayName(e)
	if s := g.sess[e.URL]; s != nil && s.failed && s.lastErr != "" {
		name += "\n" + s.lastErr
	}
	return name, true
}

// drawTooltip draws the hovered tile's server name beside the rail.
func (g *game) drawTooltip(screen *ebiten.Image) {
	s, ok := g.tooltipText()
	if !ok {
		return
	}
	if g.tip.idx == fpTipID {
		g.drawFingerprintTip(screen, s)
		return
	}
	lh := g.lineH()
	lines := []string{s}
	if name, rest, ok := strings.Cut(s, "\n"); ok { // a failed tile: name, then its wrapped error
		lines = append([]string{name}, tooltipLines(rest, fpTipCols)...)
	}
	var tw float64
	for _, l := range lines {
		tw = max(tw, text.Advance(l, g.face))
	}
	w, h := tw+pad, float64(len(lines))*lh+pad/2
	y0 := pad + float64(g.tip.idx)*railTileH
	x, y := state.TooltipOrigin(railWidth, y0, y0+railTileH, w, h, float64(g.w), float64(g.h))
	g.drawPanelFade(screen, x, y, x+w, y+h, g.th.Panel, 4)
	for i, l := range lines {
		g.drawText(screen, l, x+pad/2, y+pad/4+float64(i)*lh, g.th.Bright)
	}
}

// railEpoch is the monotonic origin for the reconnecting pulse.
var railEpoch = time.Now()

// tileFacts gathers what decides how a saved server's rail tile looks.
func (g *game) tileFacts(e state.ServerEntry) state.TileFacts {
	f := state.TileFacts{Viewed: e.URL == g.curKey}
	s, ok := g.sess[e.URL]
	if !ok {
		return f
	}
	f.HasSession = true
	f.Reconnecting = s.everUp && s.c == nil
	f.InCall = g.callKey != "" && g.callKey == e.URL
	f.Unread = s.st.AnyUnread()
	f.Failed = s.failed
	return f
}

// drawBust draws a "you are here" head-and-shoulders glyph in a size-px box.
func drawBust(dst *ebiten.Image, x, y, size float32, c color.RGBA) {
	u := size / 12
	vector.FillCircle(dst, x+6*u, y+3.5*u, 2.5*u, c, true)
	var p vector.Path
	p.MoveTo(x+1.5*u, y+11.5*u)
	p.QuadTo(x+6*u, y+3.5*u, x+10.5*u, y+11.5*u)
	vector.StrokePath(dst, &p, &vector.StrokeOptions{Width: 1.5 * u}, &vector.DrawPathOptions{ColorScale: scaleOf(c), AntiAlias: true})
}

// retryBadgeRect is the click rect of the retry badge of the tile whose top
// is at y: a 14 px square centred on the badge at (l+6, b-6) of drawRail.
func retryBadgeRect(y float64) (x0, y0, x1, y1 float64) {
	cx, cy := railInset+4+6, y+railTileH-railInset-4-6
	return cx - 7, cy - 7, cx + 7, cy + 7
}

// drawCross draws an X of half-size h centred on (cx, cy).
func drawCross(dst *ebiten.Image, cx, cy, h float32, c color.RGBA) {
	var p vector.Path
	p.MoveTo(cx-h, cy-h)
	p.LineTo(cx+h, cy+h)
	p.MoveTo(cx+h, cy-h)
	p.LineTo(cx-h, cy+h)
	vector.StrokePath(dst, &p, &vector.StrokeOptions{Width: 1.6}, &vector.DrawPathOptions{ColorScale: scaleOf(c), AntiAlias: true})
}

// drawRetry draws a ~300 degree arc of radius r centred on (cx, cy) with an
// arrowhead at its end.
func drawRetry(dst *ebiten.Image, cx, cy, r float32, c color.RGBA) {
	const start, sweep = -math.Pi / 3, 5 * math.Pi / 3
	var p vector.Path
	p.Arc(cx, cy, r, start, start+sweep, vector.Clockwise)
	// The arc ends where it started going round: the arrowhead sits there,
	// pointing along the clockwise tangent.
	end := float64(start + sweep)
	ex, ey := cx+r*float32(math.Cos(end)), cy+r*float32(math.Sin(end))
	tx, ty := float32(-math.Sin(end)), float32(math.Cos(end)) // clockwise tangent
	nx, ny := -ty, tx
	p.MoveTo(ex+nx*2.5-tx*1.5, ey+ny*2.5-ty*1.5)
	p.LineTo(ex+tx*2.5, ey+ty*2.5)
	p.LineTo(ex-nx*2.5-tx*1.5, ey-ny*2.5-ty*1.5)
	vector.StrokePath(dst, &p, &vector.StrokeOptions{Width: 1.4}, &vector.DrawPathOptions{ColorScale: scaleOf(c), AntiAlias: true})
}

func (g *game) drawRail(screen *ebiten.Image) {
	if g.railW() == 0 {
		return
	}
	th := g.th
	vector.FillRect(screen, 0, 0, railWidth, float32(g.h), th.BG, false)
	tile := func(label string, y float64, ts state.TileState) {
		fill, txt := th.Panel, th.FG
		if ts.Accent { // the active tile is a panel tinted toward the accent
			fill, txt = mixRGBA(th.Panel, th.Bright, 0.22), th.Bright
		}
		if ts.Dim {
			txt = th.Dim
		}
		g.drawPanelFade(screen, railInset, y+railInset, railWidth-railInset, y+railTileH-railInset, fill, 4)
		tw := text.Advance(label, g.face)
		g.drawText(screen, label, (railWidth-tw)/2, y+(railTileH-g.fontSize()*1.2)/2, txt)
		l, r, t, b := float32(railInset+4), float32(railWidth-railInset-4), float32(y+railInset+4), float32(y+railTileH-railInset-4)
		if ts.Dot {
			vector.FillCircle(screen, r-1, t+1, 3, th.Bright, true)
		}
		if ts.Bust {
			drawBust(screen, r-12, b-12, 12, th.Bright)
		}
		if ts.FailX { // a red cross at the bottom-right
			drawCross(screen, r-6, b-6, 3.5, th.Warn)
		}
		if ts.Retry { // a circular arrow at the bottom-left
			drawRetry(screen, l+6, b-6, 4.5, th.Bright)
		}
		if ts.Pulse {
			a := 0.5 + 0.5*math.Sin(2*math.Pi*time.Since(railEpoch).Seconds()/1.2)
			c := th.Bright
			c = color.RGBA{uint8(float64(c.R) * a), uint8(float64(c.G) * a), uint8(float64(c.B) * a), uint8(255 * a)}
			vector.FillCircle(screen, l+2, b-2, 2.5, c, true)
		}
	}
	for i := 0; i < g.railTiles(); i++ {
		e := g.servers[i]
		tile(state.Initials(e), pad+float64(i)*railTileH, state.Tile(g.tileFacts(e)))
	}
	tile("+", float64(g.h)-2*railTileH, state.TileState{})
	gy := float64(g.h) - railTileH
	tile("", gy, state.TileState{Accent: g.settingsOpen})
	gc := th.FG
	if g.settingsOpen {
		gc = th.Bright
	}
	drawGear(screen, (railWidth-1)/2, float32(gy+railTileH/2), 11, gc)
}
