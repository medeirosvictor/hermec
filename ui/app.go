// Package ui is the Ebiten front end of Hermec. Pure logic lives in ui/state
// and ui/theme; this package only wires input, drawing and the network.
package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
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
}

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
	face  *text.GoTextFace
	id    *identity.Identity
	fp    string
	keyAt string

	c       *client.Client
	dialCh  chan dialResult
	joinCh  chan error
	dialing bool
	connect connectForm
	frame   int
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

	g := &game{
		opts: opts, st: state.New(), th: th, face: face, id: id, fp: fp, keyAt: opts.KeyPath,
		dialCh: make(chan dialResult, 1), joinCh: make(chan error, 1),
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
		g.logf("local server on %s", url)
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
	g.dialing = true
	g.st.ConnectErr = ""
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
			if chs := r.c.Channels(); len(chs) > 0 {
				go func(c *client.Client, ch string) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					g.joinCh <- c.Join(ctx, ch)
				}(r.c, chs[0])
			}
		}
	default:
	}
	select {
	case err := <-g.joinCh:
		if err != nil {
			g.st.Status = fmt.Sprintf("join failed: %v", err)
		} else {
			g.logf("joined first channel")
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

	if g.st.Phase == state.PhaseConnect {
		if url, name, submit := g.connect.update(g.dialing); submit {
			g.startDial(url, name)
		}
	}
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	screen.Fill(g.th.BG)
	if g.st.Phase == state.PhaseConnect {
		g.drawConnect(screen)
	} else {
		g.drawMain(screen)
	}
}

func (g *game) Layout(w, h int) (int, int) { return w, h }

// drawMain is a placeholder until Task 4.
func (g *game) drawMain(screen *ebiten.Image) {
	line := "connected"
	if len(g.st.Channels) > 0 {
		line = "connected - channel: " + g.st.Channels[g.st.Active]
	}
	if g.st.Phase == state.PhaseDisconnected {
		line = "disconnected"
	}
	g.drawText(screen, line, 24, 24, g.th.FG)
	if g.st.Status != "" {
		g.drawText(screen, g.st.Status, 24, 24+g.lineH(), g.th.Bright)
	}
}
