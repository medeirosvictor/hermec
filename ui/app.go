// Package ui is the Ebiten front end of Hermec. Pure logic lives in ui/state
// and ui/theme; this package only wires input, drawing and the network.
package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

// Options configures Run.
type Options struct {
	ServerURL string
	Name      string // empty: derived from the identity fingerprint
	KeyPath   string // empty: <user config dir>/hermec/identity.key
	ThemePath string
	Local     bool // ignore ServerURL; run an in-process server
	Verbose   bool // log breadcrumbs

	ServersPath string // empty: <user config dir>/hermec/servers.toml
}

// Size hierarchy relative to Theme.FontSize.
const (
	smallScale = 0.75 // status and hint lines
	titleScale = 1.5  // connect title
)

// maxEventsPerTick bounds how many client events one Update drains.
const maxEventsPerTick = 64

type dialResult struct {
	c   *client.Client
	err error
}

type game struct {
	opts  Options
	st    *state.State
	th    theme.Theme
	face  *text.GoTextFace // body/chat size (Theme.FontSize)
	faceS *text.GoTextFace // status and hint lines, 0.75x
	faceT *text.GoTextFace // connect title, 1.5x
	id    *identity.Identity
	fp    string
	keyAt string

	c       *client.Client
	dialCh  chan dialResult
	joinCh  chan joinResult
	sendCh  chan error
	dialing bool
	connect connectForm
	ms      mainScene
	frame   int
	w, h    int

	lastURL, lastName string // for reconnect

	serversPath string
	servers     []state.ServerEntry
	localURL    string // URL of the in-process server; empty without -local
	curKey      string // saved-list key of the current server; empty = none
	notice      string // persistent load/save error, shown on the connect scene

	barOnce sync.Once // dark title bar, applied on the first tick

	crt   crt
	crtOn bool // starts from the theme, toggled with F1
}

// DefaultKeyPath returns <user config dir>/hermec/identity.key.
func DefaultKeyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hermec", "identity.key"), nil
}

// LoadOrCreateIdentity loads the identity at path, generating and saving a
// new one (creating the directory 0700) if the file does not exist.
func LoadOrCreateIdentity(path string) (*identity.Identity, error) {
	if _, err := os.Stat(path); err == nil {
		return identity.Load(path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	id, err := identity.Generate()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := id.Save(path); err != nil {
		return nil, err
	}
	return id, nil
}

// DefaultServersPath returns <user config dir>/hermec/servers.toml.
func DefaultServersPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hermec", "servers.toml"), nil
}

// Run opens the window and blocks until it closes.
func Run(opts Options) error {
	if opts.KeyPath == "" {
		p, err := DefaultKeyPath()
		if err != nil {
			return fmt.Errorf("locate key path: %w", err)
		}
		opts.KeyPath = p
	}
	id, err := LoadOrCreateIdentity(opts.KeyPath)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	fp := identity.Fingerprint(id.PublicKey())
	if opts.Name == "" {
		opts.Name = state.DefaultName(fp)
	}
	th, err := theme.Load(opts.ThemePath)
	if err != nil {
		return err
	}
	face, err := theme.Face(th.FontSize)
	if err != nil {
		return fmt.Errorf("font: %w", err)
	}
	faceS, err := theme.Face(th.FontSize * smallScale)
	if err != nil {
		return fmt.Errorf("font: %w", err)
	}
	faceT, err := theme.Face(th.FontSize * titleScale)
	if err != nil {
		return fmt.Errorf("font: %w", err)
	}

	g := &game{
		opts: opts, st: state.New(), th: th, face: face, faceS: faceS, faceT: faceT, id: id, fp: fp, keyAt: opts.KeyPath,
		dialCh: make(chan dialResult, 1), joinCh: make(chan joinResult, 8), sendCh: make(chan error, 8), ms: newMainScene(), w: 960, h: 600, crtOn: th.Scanlines,
	}
	url := opts.ServerURL
	if opts.Local {
		srv := server.New(server.Config{
			Addr:     "127.0.0.1:0",
			Channels: []string{"general"},
			Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		})
		if err := srv.Start(); err != nil {
			return fmt.Errorf("local server: %w", err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("local server shutdown: %v", err)
			}
			g.logf("local server stopped")
		}()
		url = "ws://" + srv.Addr() + "/"
		g.localURL = url
		g.logf("local server on %s", url)
	}
	g.loadServers(opts.ServersPath)
	if !opts.Local {
		for _, e := range g.servers { // one Enter reconnects the most recent dialable
			if e.URL != localKey {
				url = e.URL
				break
			}
		}
	}
	g.connect = newConnectForm(opts.Name, url, opts.Local)

	ebiten.SetWindowSize(960, 600)
	ebiten.SetWindowTitle("Hermec")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	err = ebiten.RunGame(g)
	if g.c != nil {
		_ = g.c.Close()
	}
	return err
}

func (g *game) logf(format string, args ...any) {
	if g.opts.Verbose {
		log.Printf(format, args...)
	}
}

// startDial dials in a goroutine; the result arrives on dialCh.
func (g *game) startDial(url, name string) {
	g.st.ConnectErr = ""
	url = g.realURL(url)
	if url == localKey {
		g.st.ConnectErr = "local server not running (start with -local)"
		return // no dial goroutine: must not set g.dialing
	}
	g.dialing = true
	g.lastURL, g.lastName = url, name
	g.curKey = g.keyFor(url)
	g.logf("dialing %s as %q", url, name)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := client.Dial(ctx, url, g.id, name, "")
		g.dialCh <- dialResult{c, err}
	}()
}

func (g *game) Update() error {
	g.frame++
	g.barOnce.Do(darkTitleBar)
	if inpututil.IsKeyJustPressed(ebiten.KeyF1) {
		g.crtOn = !g.crtOn
		g.logf("crt effect: %v", g.crtOn)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF2) {
		// Scenes read g.th on every draw, so the swap is live. Face size
		// does not depend on the palette, so no face rebuild is needed.
		g.th = theme.NextPreset(g.th)
		g.logf("palette: bg=%v fg=%v", g.th.BG, g.th.FG)
	}
	select {
	case r := <-g.dialCh:
		g.dialing = false
		if r.err != nil {
			g.logf("dial failed: %v", r.err)
			g.st.ConnectErr = r.err.Error()
		} else {
			g.logf("connected, channels %v", r.c.Channels())
			g.c = r.c
			g.st.SetConnected(r.c.Channels(), r.c.Fingerprint())
			g.ms = newMainScene()
			g.onConnected(r.c.Channels())
		}
	default:
	}
	select {
	case r := <-g.joinCh:
		if r.err != nil {
			delete(g.ms.joined, r.channel) // allow retry on next switch
			g.st.Status = fmt.Sprintf("join %s failed: %v", r.channel, r.err)
		} else {
			g.logf("joined %s", r.channel)
		}
	default:
	}
	select {
	case err := <-g.sendCh:
		if err != nil {
			g.st.Status = fmt.Sprintf("send failed: %v", err)
		}
	default:
	}

	if g.c != nil {
	drain:
		for i := 0; i < maxEventsPerTick; i++ {
			select {
			case ev, ok := <-g.c.Events():
				g.st.Apply(ev, ok)
				if !ok {
					g.c = nil
					break drain
				}
			default:
				break drain
			}
		}
	}

	g.updateRail()
	if g.st.Phase == state.PhaseConnect {
		if url, name, submit := g.connect.update(g.dialing, g.lineH(), g.w, g.railW()); submit {
			g.startDial(url, name)
		}
	}
	switch g.st.Phase {
	case state.PhaseMain:
		g.updateMain()
	case state.PhaseDisconnected:
		g.updateDisconnected()
	}
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	if !g.crtOn {
		g.drawScene(screen)
		return
	}
	b := screen.Bounds()
	off := g.crt.target(b.Dx(), b.Dy())
	g.drawScene(off)
	g.crt.apply(screen)
}

func (g *game) drawScene(screen *ebiten.Image) {
	screen.Fill(g.th.BG)
	if g.st.Phase == state.PhaseConnect {
		g.drawConnect(screen)
	} else {
		g.drawMain(screen)
	}
	g.drawRail(screen)
}

func (g *game) Layout(w, h int) (int, int) {
	g.w, g.h = w, h
	return w, h
}
