package ui

import (
	"context"
	"os"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

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
	updChecking  updateStatus = iota // a check is in flight (the zero value)
	updUpToDate                      // checked, nothing newer (or a dev build)
	updAvailable                     // g.update holds a newer release
	updFailed                        // the check could not complete
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
	case updFailed:
		return p + "check failed"
	}
	return p + "checking…"
}

// startUpdateCheck runs the release check in the background (house
// goroutine+channel pattern). The result arrives on g.updateCh, drained in
// pollUpdate. Check bounds its own time, so context.Background is enough. It
// always runs: the old update_check setting is no longer read. Only one check
// is in flight at a time.
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
			g.update, g.updStatus = &a, updAvailable
			g.logf("update %s available: %s", a.Version, a.URL)
		default:
			g.updStatus = updUpToDate
		}
	default:
	}
}

// activateUpdateRow is Enter or a click on the settings row: with an update
// available it does what the banner and Ctrl+U do, otherwise it re-checks.
func (g *game) activateUpdateRow() {
	if g.update != nil {
		g.openUpdate()
		return
	}
	g.startUpdateCheck()
}

// updateKey opens the release page on Ctrl+U while the banner is showing.
// Plain U would be typed into the always-focused chat input (and the connect
// fields), so, like Ctrl+M (mute) and Ctrl+R (refresh), it needs Ctrl.
func (g *game) updateKey() {
	if g.update == nil || !inpututil.IsKeyJustPressed(ebiten.KeyU) {
		return
	}
	if ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) {
		g.openUpdate()
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

// updateBanner opens the release page when the banner is clicked.
func (g *game) updateBanner() {
	if g.update == nil || g.settingsOpen || !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	if h, ok := g.hitAt(); ok && h.Kind == state.HitBanner {
		g.openUpdate()
	}
}

// addBannerHit registers the banner line (same rect the text is drawn in).
func (g *game) addBannerHit() {
	if g.update == nil {
		return
	}
	g.hits.Add(state.Hit{X0: g.railW(), Y0: 0, X1: float64(g.w), Y1: pad + g.lineH(), Kind: state.HitBanner})
}

// bannerH is the height the banner takes at the top of the connect and main
// scenes: one line when an update is available, otherwise nothing so the
// layout does not move.
func (g *game) bannerH() float64 {
	if g.update == nil {
		return 0
	}
	return g.lineH()
}

func (g *game) drawBanner(screen *ebiten.Image) {
	if g.update == nil {
		return
	}
	g.drawTextF(screen, g.faceS, "update "+g.update.Version+" available — ctrl+u to download", g.railW()+pad, pad, g.th.Bright)
}
