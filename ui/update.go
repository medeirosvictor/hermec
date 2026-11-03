package ui

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/update"
	"github.com/medeirosvictor/hermec/version"
)

// fakeUpdateEnv is a hidden dev aid, deliberately undocumented outside this
// comment: HERMEC_FAKE_UPDATE=vX.Y.Z injects a fake available update (pointing
// at the releases page) so the banner can be seen without a real newer
// release. It is read only when set, and bypasses the dev-build
// check.
const fakeUpdateEnv = "HERMEC_FAKE_UPDATE"

// updateStatus is what the settings row shows about the release check. The
// banner and Ctrl+U read g.update; the row reads this as well.
type updateStatus int

const (
	updChecking    updateStatus = iota // a check is in flight (the zero value)
	updUpToDate                        // checked, nothing newer (or a dev build)
	updAvailable                       // g.update holds a newer release; acting on it opens the releases page
	updFailed                          // the check could not complete
	updDownloading                     // the release is downloading in the background
	updReady                           // the new binary is staged; acting on it restarts into it
)

// updateResult is what one release check sends back on g.updateCh.
type updateResult struct {
	a     update.Available
	found bool
	err   error
}

// version is the running build's version: g.ver when set (tests), otherwise
// the stamped version.Version.
func (g *game) version() string {
	if g.ver != "" {
		return g.ver
	}
	return version.Version
}

// updateRowLabel is the settings row for the release check. cur is the running
// version, "" or "dev" for a dev build.
func updateRowLabel(st updateStatus, a *update.Available, cur string) string {
	const p = "update:    "
	switch st {
	case updUpToDate:
		if cur == "" || cur == "dev" {
			cur = "dev"
		} else if !strings.HasPrefix(cur, "v") {
			cur = "v" + cur
		}
		return p + "up to date (" + cur + ")"
	case updAvailable:
		if a != nil {
			return p + a.Version + " available ›"
		}
		return p + "available ›"
	case updDownloading:
		if a != nil {
			return p + "downloading " + a.Version + "…"
		}
		return p + "downloading…"
	case updReady:
		if a != nil {
			return p + "restart to update to " + a.Version + " ›"
		}
		return p + "restart to update ›"
	case updFailed:
		return p + "check failed"
	}
	return p + "checking…"
}

// startUpdateCheck runs the release check in the background (house
// goroutine+channel pattern). The result arrives on g.updateCh, drained in
// pollUpdate. Check bounds its own time, so context.Background is enough. Only
// one check is in flight at a time.
func (g *game) startUpdateCheck() {
	if v := os.Getenv(fakeUpdateEnv); v != "" && !g.updBusy {
		g.updBusy, g.updStatus = true, updChecking
		g.updateCh <- updateResult{a: update.Available{Version: v, URL: "https://github.com/medeirosvictor/hermec/releases"}, found: true}
		return
	}
	cur := g.version()
	if cur == "dev" {
		g.updStatus = updUpToDate
		return
	}
	if g.updBusy {
		return
	}
	g.updBusy, g.updStatus = true, updChecking
	check, ch := g.checkFn, g.updateCh
	if check == nil {
		check = update.Latest
	}
	go func() {
		a, found, err := check(context.Background(), cur)
		ch <- updateResult{a: a, found: found, err: err}
	}()
}

func (g *game) pollUpdate() {
	select {
	case r := <-g.updateCh:
		g.updBusy = false
		switch {
		case r.err != nil:
			g.updStatus = updFailed
			g.logf("update check failed: %v", r.err)
		case r.found:
			a := r.a
			g.update, g.updStatus, g.updOffer = &a, updAvailable, ""
			g.logf("update %s available: %s", a.Version, a.URL)
			g.startDownload()
		default:
			g.updStatus = updUpToDate
			if g.updOffer != "" { // the server's build is not published (yet)
				g.updOffer = ""
				g.updateNote("no newer release is published yet")
			}
		}
	default:
	}
	select {
	case err := <-g.dlCh:
		g.cancelUpdate()
		switch {
		case err == nil:
			g.updStatus = updReady
			g.logf("update %s downloaded and verified", g.update.Version)
		case errors.Is(err, update.ErrNotWritable):
			g.updStatus = updAvailable
			g.logf("update download: %v", err)
			g.updateNote("can't update in place (install folder is not writable) — ctrl+u opens the releases page")
		default:
			g.updStatus = updAvailable // the releases page is the fallback
			g.logf("update download failed: %v", err)
		}
	default:
	}
}

// startDownload fetches and verifies the release in g.update in the
// background, if this build can update itself. The result arrives on g.dlCh.
// Only one download runs at a time; a failure leaves the releases-page link.
func (g *game) startDownload() {
	supported := g.supportedFn
	if supported == nil {
		supported = update.Supported
	}
	if g.update == nil || g.updStatus == updDownloading || g.version() == "dev" ||
		os.Getenv(fakeUpdateEnv) != "" || !supported() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.updCancel = cancel
	g.updStatus = updDownloading
	a, dl, ch := *g.update, g.downloadFn, g.dlCh
	if dl == nil {
		cur := g.version()
		dl = func(ctx context.Context, a update.Available) error { return update.Download(ctx, a, cur) }
	}
	g.logf("downloading update %s", a.Version)
	go func() { ch <- dl(ctx, a) }()
}

// cancelUpdate stops a download in flight and releases its context.
func (g *game) cancelUpdate() {
	if g.updCancel != nil {
		g.updCancel()
		g.updCancel = nil
	}
}

// updateNote shows msg on the connect screen and in the status line.
func (g *game) updateNote(msg string) {
	g.notice = msg
	g.cur().st.Status = msg
}

// applyUpdate swaps the staged binary in and quits so Run can start it. A
// failed swap is rolled back by update.Apply; the releases page stays the
// fallback.
func (g *game) applyUpdate() {
	apply := g.applyFn
	if apply == nil {
		apply = update.Apply
	}
	if err := apply(); err != nil {
		g.logf("update apply failed: %v", err)
		g.updStatus = updAvailable
		g.updateNote("couldn't install the update — ctrl+u opens the releases page")
		return
	}
	g.logf("update %s installed; restarting", g.update.Version)
	g.restart = true
}

// offerUpdateFor is called with a failed dial's error: a version_mismatch from
// a server running a newer release than ours offers "update now" in the banner.
// When ours is newer the plain message (telling the host to update) stands.
func (g *game) offerUpdateFor(err error) {
	var se *client.ServerError
	if g.update != nil || !errors.As(err, &se) || se.Code != proto.CodeVersionMismatch {
		return
	}
	if sv, _, ok := proto.VersionMismatchBuilds(se.Message); ok && update.Newer(sv, g.version()) {
		g.updOffer = sv
	}
}

// activateUpdate is what the banner, Ctrl+U and the settings row do: restart
// into a staged update, wait for a download, open the releases page, or (for a
// server's "update now" offer) check for the release and download it.
func (g *game) activateUpdate() {
	switch {
	case g.updStatus == updReady && g.update != nil:
		g.applyUpdate()
	case g.updStatus == updDownloading:
	case g.update != nil:
		g.openUpdate()
	case g.updOffer != "":
		g.startUpdateCheck()
	}
}

// activateUpdateRow is Enter or a click on the settings row: with an update
// known it does what the banner does, otherwise it re-checks.
func (g *game) activateUpdateRow() {
	if g.update != nil {
		g.activateUpdate()
		return
	}
	g.startUpdateCheck()
}

// updateKey acts on the banner on Ctrl+U while it is showing.
// Plain U would be typed into the always-focused chat input (and the connect
// fields), so, like Ctrl+M (mute) and Ctrl+R (refresh), it needs Ctrl.
func (g *game) updateKey() {
	if g.bannerText() == "" || !inpututil.IsKeyJustPressed(ebiten.KeyU) {
		return
	}
	if ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) {
		g.activateUpdate()
	}
}

func (g *game) openUpdate() {
	g.logf("opening %s", g.update.URL)
	open := g.openFn
	if open == nil {
		open = openURL
	}
	open(g.update.URL) // URL is validated upstream by update.Latest
}

// updateBanner acts on the banner when it is clicked.
func (g *game) updateBanner() {
	if g.bannerText() == "" || g.settingsOpen || !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	if h, ok := g.hitAt(); ok && h.Kind == state.HitBanner {
		g.activateUpdate()
	}
}

// addBannerHit registers the banner line (same rect the text is drawn in).
func (g *game) addBannerHit() {
	if g.bannerText() == "" {
		return
	}
	g.hits.Add(state.Hit{X0: g.railW(), Y0: 0, X1: float64(g.w), Y1: pad + g.lineH(), Kind: state.HitBanner})
}

// bannerText is the banner line, "" when there is nothing to say.
func (g *game) bannerText() string {
	switch {
	case g.update != nil && g.updStatus == updReady:
		return "update " + g.update.Version + " ready — click or ctrl+u to restart"
	case g.update != nil && g.updStatus == updDownloading:
		return "downloading update " + g.update.Version + "…"
	case g.update != nil:
		return "update " + g.update.Version + " available — ctrl+u to download"
	case g.updOffer != "" && g.updBusy:
		return "checking for " + g.updOffer + "…"
	case g.updOffer != "":
		return "server runs " + g.updOffer + " — ctrl+u to update now"
	}
	return ""
}

// bannerH is the height the banner takes at the top of the connect and main
// scenes: one line when it has something to say, otherwise nothing so the
// layout does not move.
func (g *game) bannerH() float64 {
	if g.bannerText() == "" {
		return 0
	}
	return g.lineH()
}

func (g *game) drawBanner(screen *ebiten.Image) {
	t := g.bannerText()
	if t == "" {
		return
	}
	g.drawTextF(screen, g.faceS, t, g.railW()+pad, pad, g.th.Bright)
}
