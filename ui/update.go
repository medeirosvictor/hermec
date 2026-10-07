package ui

import (
	"context"
	"os"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/medeirosvictor/hermec/update"
	"github.com/medeirosvictor/hermec/version"
)

// fakeUpdateEnv is a hidden dev aid, deliberately undocumented outside this
// comment: HERMEC_FAKE_UPDATE=vX.Y.Z injects a fake available update (pointing
// at the releases page) so the banner can be seen without a real newer
// release. It is read only when set, and bypasses the dev-build and opt-out
// checks.
const fakeUpdateEnv = "HERMEC_FAKE_UPDATE"

// startUpdateCheck runs the release check in the background (house
// goroutine+channel pattern). The result arrives on g.updateCh, drained in
// pollUpdate. Check bounds its own time, so context.Background is enough.
func (g *game) startUpdateCheck() {
	if v := os.Getenv(fakeUpdateEnv); v != "" {
		g.updateCh <- update.Available{Version: v, URL: "https://github.com/medeirosvictor/hermec/releases"}
		return
	}
	if version.Version == "dev" || !g.set.UpdateCheckEnabled() {
		return
	}
	go func() {
		if a, ok := update.Check(context.Background(), version.Version); ok {
			g.updateCh <- a
		}
	}()
}

func (g *game) pollUpdate() {
	select {
	case a := <-g.updateCh:
		g.update = &a
		g.logf("update %s available: %s", a.Version, a.URL)
	default:
	}
}

// updateKey opens the release page on Ctrl+U while the banner is showing.
// Plain U would be typed into the always-focused chat input (and the connect
// fields), so, like Ctrl+M (mute) and Ctrl+R (refresh), it needs Ctrl.
func (g *game) updateKey() {
	if g.update == nil || !inpututil.IsKeyJustPressed(ebiten.KeyU) {
		return
	}
	if ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) {
		g.logf("opening %s", g.update.URL)
		openURL(g.update.URL) // URL is validated upstream by update.Check
	}
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
