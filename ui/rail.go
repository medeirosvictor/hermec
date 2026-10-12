package ui

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

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

// railW is the rail's width, or 0 while no server has been saved.
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
	g.servers = state.Touch(g.servers, s.key, label, channel, live)
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
	if g.railW() == 0 || !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	h, ok := g.hitAt()
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
	h := float64(g.h)
	g.hits.Add(state.Hit{X0: 0, Y0: h - 2*railTileH, X1: railWidth, Y1: h - railTileH, Kind: state.HitRailAdd})
	g.hits.Add(state.Hit{X0: 0, Y0: h - railTileH, X1: railWidth, Y1: h, Kind: state.HitGear})
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
		g.drawText(screen, label, (railWidth-tw)/2, y+(railTileH-g.th.FontSize*1.2)/2, txt)
		l, r, t, b := float32(railInset+4), float32(railWidth-railInset-4), float32(y+railInset+4), float32(y+railTileH-railInset-4)
		if ts.Dot {
			vector.FillCircle(screen, r-1, t+1, 3, th.Bright, true)
		}
		if ts.Bust {
			drawBust(screen, r-12, b-12, 12, th.Bright)
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
