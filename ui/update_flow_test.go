package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/update"
)

// updGame is a release build at v0.2.0 whose checks and downloads are stubs.
func updGame(t *testing.T) *game {
	t.Helper()
	g := newTestGame(t)
	g.ver = "v0.2.0"
	g.updateCh, g.dlCh = make(chan updateResult, 1), make(chan error, 1)
	g.supportedFn = func() bool { return true }
	g.openFn = func(string) {}
	return g
}

// pollUntil polls until cond holds.
func pollUntil(t *testing.T, g *game, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		g.pollUpdate()
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timed out; status = %v", g.updStatus)
	}
}

func TestUpdateFlowDownloadThenRestart(t *testing.T) {
	g := updGame(t)
	release := make(chan struct{})
	var got update.Available
	g.checkFn = func(context.Context, string) (update.Available, bool, error) {
		return update.Available{Version: "v0.3.0", URL: "https://github.com/medeirosvictor/hermec/releases/tag/v0.3.0"}, true, nil
	}
	g.downloadFn = func(_ context.Context, a update.Available) error {
		got = a
		<-release
		return nil
	}
	applied := 0
	g.applyFn = func() error { applied++; return nil }

	g.startUpdateCheck()
	if g.updStatus != updChecking {
		t.Fatalf("status = %v, want checking", g.updStatus)
	}
	pollUntil(t, g, func() bool { return g.updStatus == updDownloading })
	if want := "update:    downloading v0.3.0…"; updateRowLabel(g.updStatus, g.update, g.version()) != want {
		t.Fatalf("row = %q", updateRowLabel(g.updStatus, g.update, g.version()))
	}
	// Acting on it while it downloads does nothing: no link, no restart.
	g.openFn = func(string) { t.Error("opened the page during a download") }
	g.activateUpdate()
	g.startDownload() // a second download must not start
	if applied != 0 || g.restart {
		t.Fatal("applied before the download finished")
	}

	close(release)
	pollUntil(t, g, func() bool { return g.updStatus == updReady })
	if got.Version != "v0.3.0" {
		t.Fatalf("download got %+v", got)
	}
	if want := "update:    restart to update to v0.3.0 ›"; updateRowLabel(g.updStatus, g.update, g.version()) != want {
		t.Fatalf("row = %q", updateRowLabel(g.updStatus, g.update, g.version()))
	}
	if g.bannerText() != "update v0.3.0 ready — click or ctrl+u to restart" {
		t.Fatalf("banner = %q", g.bannerText())
	}
	g.activateUpdate()
	if applied != 1 || !g.restart {
		t.Fatalf("applied=%d restart=%v", applied, g.restart)
	}
	if err := g.Update(); err == nil {
		t.Fatal("Update must end the game once an update is installed")
	}
}

func TestUpdateDownloadFailureFallsBackToLink(t *testing.T) {
	g := updGame(t)
	g.downloadFn = func(context.Context, update.Available) error { return errors.New("signature does not match") }
	var opened []string
	g.openFn = func(u string) { opened = append(opened, u) }
	g.updateCh <- updateResult{a: update.Available{Version: "v0.3.0", URL: "https://x/rel"}, found: true}
	pollUntil(t, g, func() bool { return g.updStatus == updDownloading })
	pollUntil(t, g, func() bool { return g.updStatus == updAvailable })
	if g.bannerText() != "update v0.3.0 available — ctrl+u to download" {
		t.Fatalf("banner = %q", g.bannerText())
	}
	if g.cur().st.Status != "" || g.notice != "" {
		t.Fatalf("a raw error leaked into the UI: %q / %q", g.cur().st.Status, g.notice)
	}
	g.activateUpdate()
	if len(opened) != 1 || opened[0] != "https://x/rel" {
		t.Fatalf("opened %v", opened)
	}
}

func TestUpdateNotWritableExplains(t *testing.T) {
	g := updGame(t)
	g.downloadFn = func(context.Context, update.Available) error { return fmt.Errorf("x: %w", update.ErrNotWritable) }
	g.updateCh <- updateResult{a: update.Available{Version: "v0.3.0"}, found: true}
	pollUntil(t, g, func() bool { return g.updStatus == updDownloading })
	pollUntil(t, g, func() bool { return g.updStatus == updAvailable })
	if g.notice == "" || g.cur().st.Status != g.notice {
		t.Fatalf("no clear message: %q", g.notice)
	}
}

func TestUpdateApplyFailureFallsBack(t *testing.T) {
	g := updGame(t)
	g.update, g.updStatus = &update.Available{Version: "v0.3.0", URL: "https://x/rel"}, updReady
	g.applyFn = func() error { return errors.New("rename: access denied") }
	g.activateUpdate()
	if g.restart || g.updStatus != updAvailable || g.notice == "" {
		t.Fatalf("restart=%v status=%v notice=%q", g.restart, g.updStatus, g.notice)
	}
}

func TestUpdateNoDownloadWhenUnsupportedOrDev(t *testing.T) {
	for name, set := range map[string]func(*game){
		"unsupported": func(g *game) { g.supportedFn = func() bool { return false } },
		"dev":         func(g *game) { g.ver = "dev" },
	} {
		g := updGame(t)
		set(g)
		g.downloadFn = func(context.Context, update.Available) error {
			t.Errorf("%s: downloaded", name)
			return nil
		}
		g.updateCh <- updateResult{a: update.Available{Version: "v0.3.0"}, found: true}
		g.pollUpdate()
		if g.updStatus != updAvailable {
			t.Errorf("%s: status = %v", name, g.updStatus)
		}
	}
}

func TestUpdateQuitCancelsDownload(t *testing.T) {
	g := updGame(t)
	ctxCh := make(chan context.Context, 1)
	g.downloadFn = func(ctx context.Context, _ update.Available) error {
		ctxCh <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	g.updateCh <- updateResult{a: update.Available{Version: "v0.3.0"}, found: true}
	g.pollUpdate()
	ctx := <-ctxCh
	g.cancelUpdate()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("download context not cancelled")
	}
}

// ---- version_mismatch ----

func mismatchDial(t *testing.T, g *game, server, ours string) {
	t.Helper()
	s := newSession("k", "ws://127.0.0.1:1/", "me")
	s.dialing = true
	s.st.Phase = state.PhaseConnect
	g.sess["k"] = s
	err := fmt.Errorf("client: %w", &client.ServerError{Code: proto.CodeVersionMismatch, Message: proto.VersionMismatchMessage(server, ours)})
	g.applyDial(dialResult{key: "k", s: s, gen: s.dialGen, err: err})
}

func TestVersionMismatchOffersUpdateOnlyWhenServerNewer(t *testing.T) {
	g := updGame(t)
	mismatchDial(t, g, "v0.3.0", "v0.2.0")
	if g.bannerText() != "server runs v0.3.0 — ctrl+u to update now" {
		t.Fatalf("banner = %q", g.bannerText())
	}

	for _, server := range []string{"v0.1.0", "v0.2.0", "", "dev", "garbage"} {
		g := updGame(t)
		mismatchDial(t, g, server, "v0.2.0")
		if g.bannerText() != "" || g.updOffer != "" {
			t.Errorf("server %q: offered %q", server, g.bannerText())
		}
	}
	g = updGame(t)
	g.ver = "dev"
	mismatchDial(t, g, "v0.3.0", "dev")
	if g.updOffer != "" {
		t.Error("a dev build was offered an update")
	}
}

func TestUpdateNowChecksAndDownloads(t *testing.T) {
	g := updGame(t)
	g.checkFn = func(context.Context, string) (update.Available, bool, error) {
		return update.Available{Version: "v0.3.0"}, true, nil
	}
	g.downloadFn = func(context.Context, update.Available) error { return nil }
	mismatchDial(t, g, "v0.3.0", "v0.2.0")
	g.activateUpdate()
	if g.bannerText() != "checking for v0.3.0…" {
		t.Fatalf("banner = %q", g.bannerText())
	}
	pollUntil(t, g, func() bool { return g.updStatus == updReady })
	if g.updOffer != "" || g.update == nil {
		t.Fatalf("offer=%q update=%v", g.updOffer, g.update)
	}
}

func TestUpdateNowWhenReleaseNotPublished(t *testing.T) {
	g := updGame(t)
	g.checkFn = func(context.Context, string) (update.Available, bool, error) { return update.Available{}, false, nil }
	mismatchDial(t, g, "v0.3.0", "v0.2.0")
	g.activateUpdate()
	pollUntil(t, g, func() bool { return g.updStatus == updUpToDate })
	if g.updOffer != "" || g.notice == "" {
		t.Fatalf("offer=%q notice=%q", g.updOffer, g.notice)
	}
}
