package ui

import (
	"fmt"
	"os"
	"path/filepath"

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

// railTiles is how many entries fit above the "+" tile.
func (g *game) railTiles() int {
	n := int((float64(g.h) - railTileH - pad) / railTileH)
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

// saveServer records the current server and its active channel. Failures go
// to the status line.
func (g *game) saveServer(channel string) {
	if g.curKey == "" || g.c == nil || g.serversPath == "" {
		return
	}
	label := state.HostPort(g.lastURL)
	if g.curKey == localKey {
		label = "local"
	}
	g.servers = state.Touch(g.servers, g.curKey, label, channel)
	if err := state.SaveServers(g.serversPath, g.servers); err != nil {
		g.notice = fmt.Sprintf("save servers: %v", err)
		g.st.Status = g.notice
	} else {
		g.notice = ""
	}
}

// onConnected runs after a successful dial: it picks the channel to open
// (the remembered one if the server still has it) and persists the server.
func (g *game) onConnected(chs []string) {
	idx := 0
	for _, e := range g.servers {
		if e.URL != g.curKey {
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
		g.st.Active = idx
		ch = chs[idx]
		g.joinChannel(ch)
	}
	g.saveServer(ch)
}

// leaveServer tears down the current client and returns to a fresh connect
// scene state.
func (g *game) leaveServer() {
	if g.c != nil {
		_ = g.c.Close()
		g.c = nil
	}
	g.st = state.New()
	g.ms = newMainScene()
}

func (g *game) switchServer(e state.ServerEntry) {
	if g.dialing || (e.URL == g.curKey && g.st.Phase == state.PhaseMain) {
		return
	}
	name := g.connect.name.String()
	if name == "" {
		name = g.opts.Name
	}
	g.leaveServer()
	g.connect = newConnectForm(name, g.realURL(e.URL), e.URL == localKey && g.localURL != "")
	g.startDial(e.URL, name)
}

func (g *game) addServer() {
	if g.dialing {
		return
	}
	name := g.connect.name.String()
	if name == "" {
		name = g.opts.Name
	}
	g.leaveServer()
	g.curKey = ""
	g.connect = newConnectForm(name, "ws://", false)
	g.connect.focus = 1
}

func (g *game) updateRail() {
	if g.railW() == 0 || !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	cx, cy := ebiten.CursorPosition()
	n := g.railTiles()
	switch hit := state.RailHit(float64(cx), float64(cy), railWidth, pad, railTileH, n, float64(g.h)); {
	case hit == state.RailAdd:
		g.addServer()
	case hit >= 0:
		g.switchServer(g.servers[hit])
	}
}

func (g *game) drawRail(screen *ebiten.Image) {
	if g.railW() == 0 {
		return
	}
	th := g.th
	vector.FillRect(screen, 0, 0, railWidth, float32(g.h), th.BG, false)
	vector.FillRect(screen, railWidth-1, 0, 1, float32(g.h), th.Dim, false)
	tile := func(label string, y float64, cur bool) {
		box, txt, w := th.Dim, th.FG, float32(1)
		if cur {
			box, txt, w = th.Bright, th.Bright, 2
		}
		vector.StrokeRect(screen, railInset, float32(y)+railInset, railWidth-2*railInset-1, railTileH-2*railInset, w, box, false)
		tw := text.Advance(label, g.face)
		g.drawText(screen, label, (railWidth-1-tw)/2, y+(railTileH-g.th.FontSize*1.2)/2, txt)
	}
	for i := 0; i < g.railTiles(); i++ {
		e := g.servers[i]
		tile(state.Initials(e), pad+float64(i)*railTileH, e.URL == g.curKey)
	}
	tile("+", float64(g.h)-railTileH, false)
}
