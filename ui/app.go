// Package ui is the Ebiten front end of Hermec. Pure logic lives in ui/state
// and ui/theme; this package only wires input, drawing and the network.
package ui

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/icon"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
	"github.com/medeirosvictor/hermec/update"
)

// Options configures Run.
type Options struct {
	ServerURL string
	Name      string // empty: derived from the identity fingerprint
	KeyPath   string // empty: <user config dir>/hermec/identity.key
	ThemePath string
	Local     bool // ignore ServerURL; run an in-process server
	Verbose   bool // log breadcrumbs

	ServersPath  string // empty: <user config dir>/hermec/servers.toml
	SettingsPath string // empty: <user config dir>/hermec/settings.toml
}

// Size hierarchy relative to Theme.FontSize.
const (
	smallScale = 0.75 // status and hint lines
	titleScale = 1.5  // connect title
)

// maxEventsPerTick bounds how many client events one Update drains.
const maxEventsPerTick = 64

type game struct {
	opts  Options
	th    theme.Theme
	face  *text.GoTextFace // body/chat size (Theme.FontSize)
	faceS *text.GoTextFace // status and hint lines, 0.75x

	barIcon *ebiten.Image    // 16px app icon for the title bar, built on first draw
	faceT   *text.GoTextFace // connect title, 1.5x
	id      *identity.Identity
	fp      string
	keyAt   string

	sess    map[string]*session // one per server, by saved-list key
	none    *session            // connect-scene placeholder returned by cur()
	dialCh  chan dialResult
	joinCh  chan joinResult
	sendCh  chan sendResult
	voiceCh chan voiceResult

	closeStale func(*client.Client) // test seam: closes stale dial results; nil = Close

	muteCh     chan muteReq
	muteFailCh chan muteFail
	vc         voiceCall
	callKey    string      // session holding the call; "" = no call (or a join in flight)
	openAudio  audioOpener // test seam: opens call devices; nil = the real devices

	probeCh    chan []state.DiscoveredIn
	probing    bool // a discovery probe is in flight
	discActive bool // connect scene was active last tick
	nextProbe  time.Time
	discRows   []state.DiscoveredRow

	hits    state.Hits // click targets, rebuilt each Update
	shape   ebiten.CursorShapeType
	connect connectForm
	frame   int
	w, h    int

	serversPath string
	servers     []state.ServerEntry
	localURL    string // URL of the in-process server; empty without -local
	curKey      string // key of the viewed session; empty = connect scene
	notice      string // persistent load/save error, shown on the connect scene

	settingsPath  string
	set           state.Settings
	settingsOpen  bool     // settings scene replaces the connect/main scene
	setRow        int      // focused settings row
	devIn, devOut []string // device names, enumerated on settings scene entry
	autoName      string   // name in effect at startup; not persisted unless edited
	nameDirty     bool     // settings name field edited, not yet saved

	sw, sh  int        // screen size; w, h above are the scene (screen minus title bar)
	tzone   state.Zone // title-bar zone under the mouse, ZoneNone elsewhere
	maxed   bool       // window is maximized (no resize edges)
	barInst bool       // custom-frame install attempted (first tick)

	dt       float64                    // seconds per tick, for eased values
	barF     float64                    // call bar slide, 0 hidden .. 1 shown; valid once barInit
	barInit  bool                       // barF has been seeded by the first animation step
	menu     ctxMenu                    // right-click menu on a rail tile
	dlg      dialog                     // open modal dialog, if any
	modal    bool                       // a dialog was open at the start of this tick: input handlers stand down
	hover    state.Hover                // rail tile hover timer
	tip      tooltipState               // rail tooltip
	escSpent bool                       // this tick's Esc press was already consumed
	q        quality                    // signal bars and their detail panel
	speakEnv map[string]*state.Envelope // per-occupant speaking envelope, by fingerprint
	rowHL    map[string]float64         // eased hover/active highlight per channel row

	crt     crt
	panel   panelShader
	startAt time.Time  // first CRT frame; drives the scan bar
	crtOn   bool       // starts from the theme, toggled with F1
	dev     *devReload // live reload of shaders/theme; nil unless HERMEC_DEV is set

	update   *update.Available // non-nil: a newer release exists, banner shown
	updateCh chan updateResult

	updStatus updateStatus                                                  // what the settings update row shows
	updBusy   bool                                                          // a release check is in flight
	ver       string                                                        // version override for tests; "" = version.Version
	checkFn   func(context.Context, string) (update.Available, bool, error) // nil = update.Latest
	openFn    func(string)                                                  // nil = openURL

	helpOpen   bool    // the settings help view is showing
	helpScroll int     // first visible help line
	helpCarry  float64 // fractional wheel remainder
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

// DefaultHostedPath returns <user config dir>/hermec/hosted.toml.
func DefaultHostedPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hermec", "hosted.toml"), nil
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
	var boot game // holds load results until the real game is built
	boot.loadSettings(opts.SettingsPath)
	if opts.Name == "" {
		opts.Name = boot.set.Name
	}
	if opts.Name == "" {
		opts.Name = state.DefaultName(fp)
	}
	th, crtOn, themeNotice, err := startupTheme(opts.ThemePath, boot.set)
	if err != nil {
		return err
	}
	if themeNotice != "" && boot.notice == "" {
		boot.notice = themeNotice
	}
	if boot.notice == "" {
		boot.notice = savedDevicesNotice(boot.set)
	}
	face, faceS, faceT, err := buildFaces(boot.set.FontID(), th.FontSize, boot.set.FontScalePct())
	if err != nil {
		return fmt.Errorf("font: %w", err)
	}

	g := &game{
		opts: opts, th: th, face: face, faceS: faceS, faceT: faceT, id: id, fp: fp, keyAt: opts.KeyPath,
		sess: map[string]*session{}, none: newSession("", "", ""),
		dialCh: make(chan dialResult, 8), probeCh: make(chan []state.DiscoveredIn, 1), joinCh: make(chan joinResult, 8), sendCh: make(chan sendResult, 8), voiceCh: make(chan voiceResult, 4), w: 960, h: 600, crtOn: crtOn,
		settingsPath: boot.settingsPath, set: boot.set, notice: boot.notice, autoName: opts.Name,
	}
	g.updateCh = make(chan updateResult, 1)
	g.startUpdateCheck()
	g.muteCh, g.muteFailCh = make(chan muteReq, 1), make(chan muteFail, 4)
	go g.muteWorker()
	url := opts.ServerURL
	if opts.Local {
		srv := server.New(server.Config{
			Addr:          "127.0.0.1:0",
			Discoverable:  true,
			ServerName:    "local",
			Channels:      []string{"general"},
			VoiceChannels: []string{"voice"},
			Roles:         roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
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
	g.dev = devFromEnv(g)

	ebiten.SetWindowSize(960, 600)
	ebiten.SetWindowTitle("Hermec")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	var icons []image.Image
	for _, s := range []int{16, 32, 48, 256} {
		icons = append(icons, icon.Render(s))
	}
	ebiten.SetWindowIcon(icons)
	err = ebiten.RunGame(g)
	g.flushName()
	if g.vc.src != nil || g.vc.sink != nil {
		if s := g.callSess(); s != nil && s.c != nil {
			c := s.c
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = c.LeaveVoice(ctx)
			cancel()
		}
		closeAll(g.vc.src, g.vc.sink)
	}
	for _, k := range g.sessionKeys() {
		if c := g.sess[k].c; c != nil {
			_ = c.Close()
		}
	}
	return err
}

func (g *game) logf(format string, args ...any) {
	if g.opts.Verbose {
		log.Printf(format, args...)
	}
}

func (g *game) Update() error {
	g.frame++
	if !g.barInst { // once the window exists: take over the non-client area
		g.barInst = true
		ok, info := installBar()
		g.logf("custom frame: ok=%v %s", ok, info)
	}
	if m := ebiten.Monitor(); m != nil {
		setBarScale(m.DeviceScaleFactor())
	}
	if g.updateTitleBar() {
		return ebiten.Termination
	}
	if g.dev != nil {
		g.dev.poll(time.Now())
	}
	// A dialog blocks input only: everything per-tick (sessions, reconnects,
	// voice and update results, discovery, animation) runs regardless.
	g.modal = g.dialogOpen()
	if !g.modal {
		if inpututil.IsKeyJustPressed(ebiten.KeyF1) {
			g.setScanlines(!g.crtOn)
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyF3) {
			g.toggleCurvature()
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyF2) {
			g.cyclePalette()
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyF10) {
			g.toggleSettings()
		}
	}
	g.tickBackground(time.Now())
	g.escSpent = false
	g.closeMenuOnEsc(inpututil.IsKeyJustPressed(ebiten.KeyEscape)) // first taker of Esc
	if !g.settingsOpen && !g.modal {
		g.updateKey()
	}
	g.updateVoice()
	g.updateDiscovery() // mutates connect.disc, which the hit targets depend on
	g.dt = frameDT()
	g.stepBar(g.dt)     // before buildHits: hit rects follow the drawn bar
	g.stepQuality(g.dt) // after stepBar (geometry), before buildHits
	g.buildHits()
	g.stepHighlights(g.dt)
	g.updateCursorShape()
	hit, hitOK := g.hitAt()
	g.stepTooltip(hit, hitOK, time.Now())
	if g.modal {
		g.updateDialog() // buttons, Esc, Enter and typing; nothing under it acts
	} else if !g.updateMenu() { // a click spent on the menu is not also a rail click
		g.updateBanner()
		g.updateRail()
	}
	g.connect.disc = g.discRows // updateRail may have replaced the form
	if g.settingsOpen {
		if !g.modal {
			g.updateSettings()
		}
		return nil
	}
	if g.viewingFailed() {
		if !g.modal && enterPressed() {
			g.retryNow(g.curKey)
		}
	} else if g.cur().st.Phase == state.PhaseConnect && !g.modal {
		g.connect.dy = g.bannerH() // updateRail may have replaced the form
		if url, name, submit := g.connect.update(g.cur().dialing, g.hitAt); submit {
			if s := g.openSession(url, name); s != nil {
				g.view(s.key)
			}
		}
	}
	switch g.cur().st.Phase {
	case state.PhaseMain:
		g.updateMain()
	case state.PhaseDisconnected:
		if !g.modal {
			g.updateDisconnected()
		}
	}
	if !g.modal {
		g.updateBar(g.geom()) // the call may be on another server: any scene but settings
	}
	return nil
}

// tickBackground is the per-tick work that is not input: network results,
// reconnect timers and the update check. It never depends on what is open.
func (g *game) tickBackground(now time.Time) {
	g.pumpSessions()
	g.tickReconnect(now)
	g.pollUpdate()
}

func (g *game) Draw(screen *ebiten.Image) {
	bh := int(g.barH())
	if !g.crtOn && bh == 0 {
		g.drawScene(screen)
		return
	}
	// The scene renders into an offscreen of the scene size (screen minus the
	// title bar) and lands below the bar; the CRT and its corner mask apply to
	// that area only. The bar is drawn flat on top afterwards.
	b := screen.Bounds()
	sceneH := b.Dy() - bh
	if sceneH < 1 {
		sceneH = 1
	}
	off := g.crt.target(b.Dx(), sceneH)
	g.drawScene(off)
	area := screen.SubImage(image.Rect(b.Min.X, b.Min.Y+bh, b.Max.X, b.Max.Y)).(*ebiten.Image)
	if g.crtOn {
		if g.startAt.IsZero() {
			g.startAt = time.Now()
		}
		th := g.th
		th.Curvature = g.curvature()
		g.crt.apply(area, th, time.Since(g.startAt).Seconds())
	} else {
		blit(area, off)
	}
	g.drawTitleBar(screen)
}

func (g *game) drawScene(screen *ebiten.Image) {
	screen.Fill(g.th.BG)
	if g.settingsOpen {
		g.drawSettings(screen)
	} else if g.viewingFailed() {
		g.drawFailed(screen)
		g.drawCallBar(screen, g.geom())
		g.drawBanner(screen)
	} else if g.cur().st.Phase == state.PhaseConnect {
		g.drawConnect(screen)
		g.drawCallBar(screen, g.geom()) // a call on a server keeps its bar while "+" is open
		g.drawBanner(screen)
	} else {
		g.drawMain(screen)
		g.drawBanner(screen)
	}
	g.drawRail(screen)
	g.drawTooltip(screen)
	g.drawMenu(screen)
	g.drawDialog(screen)
}

func (g *game) Layout(w, h int) (int, int) {
	g.sw, g.sh = w, h
	g.w, g.h = w, h-int(g.barH()) // the one choke point: scenes never see the bar
	if g.h < 1 {
		g.h = 1
	}
	return w, h
}
