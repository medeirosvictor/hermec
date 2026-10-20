package ui

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

// settle waits for the media worker to drain and applies its results, like the
// next Update would.
func settle(t *testing.T, g *game) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !g.media.idle() {
		if time.Now().After(deadline) {
			t.Fatal("media worker did not drain within 5s")
		}
		time.Sleep(time.Millisecond)
	}
	g.media.pump()
}

// A slow first Watch must not let a later one overtake it: the target the
// client ends on is the last click.
func TestWatchOpsAreSerialized(t *testing.T) {
	g, s := shareGame(t)
	s.st.Voice["voice"][1].Sharing = true
	var mu sync.Mutex
	var applied []string
	release := make(chan struct{})
	ops := (&recorder{}).ops(twoMonitors(), true)
	ops.watch = func(c *client.Client, fp string) {
		if fp == "annfp" {
			<-release // the first op is slow
		}
		mu.Lock()
		applied = append(applied, fp)
		mu.Unlock()
	}
	g.shareOps = ops
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "bobfp"})
	g.stopWatching()
	close(release)
	settle(t, g)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(applied, []string{"annfp", "bobfp", ""}) {
		t.Fatalf("applied = %q, want click order [annfp bobfp \"\"]", applied)
	}
}

// StartShare must not run on the game goroutine: the click returns while the
// start is still blocked, and the outcome lands on a later Update.
func TestStartShareIsAsync(t *testing.T) {
	g, s := shareGame(t)
	release := make(chan struct{})
	started := make(chan struct{})
	ops := (&recorder{}).ops(twoMonitors(), true)
	ops.start = func(c *client.Client, src client.FrameSource, w, h, fps, kbps int) error {
		close(started)
		<-release
		return errors.New("encoder exploded")
	}
	g.shareOps = ops
	g.openShareMenu(g.geom())
	done := make(chan struct{})
	go func() { g.menu.items[0].act(); close(done) }()
	select {
	case <-done: // returned while start is (or will be) blocked
	case <-time.After(2 * time.Second):
		t.Fatal("click handler blocked on StartShare")
	}
	<-started
	if !g.share.on || g.media.idle() {
		t.Fatal("share not optimistic-on while the start is in flight")
	}
	close(release)
	settle(t, g)
	if g.share.on || s.st.Status == "" {
		t.Fatalf("rollback missing: on=%v status=%q", g.share.on, s.st.Status)
	}
}

func TestStopThenStartKeepsShare(t *testing.T) {
	g, _ := shareGame(t)
	var order []string
	var mu sync.Mutex
	ops := (&recorder{}).ops(twoMonitors(), true)
	ops.start = func(c *client.Client, src client.FrameSource, w, h, fps, kbps int) error {
		mu.Lock()
		order = append(order, "start")
		mu.Unlock()
		return nil
	}
	ops.stop = func(c *client.Client) {
		time.Sleep(20 * time.Millisecond) // a slow stop must still finish first
		mu.Lock()
		order = append(order, "stop")
		mu.Unlock()
	}
	g.shareOps = ops
	g.openShareMenu(g.geom())
	g.menu.items[0].act()
	g.stopShare()
	g.openShareMenu(g.geom())
	g.menu.items[1].act()
	settle(t, g)
	g.stepShare()
	mu.Lock()
	defer mu.Unlock()
	if !g.share.on || !reflect.DeepEqual(order, []string{"start", "stop", "start"}) {
		t.Fatalf("on=%v order=%v, want share on after start,stop,start", g.share.on, order)
	}
}

func TestResetVideoClearsSinkAndTexture(t *testing.T) {
	g, _ := shareGame(t)
	g.shareOps = (&recorder{}).ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	g.vsink.WriteVideoFrame("annfp", video.NewFrame(8, 8))
	g.stepVideo()
	g.vsink.WriteVideoFrame("annfp", video.NewFrame(8, 8)) // a stale frame left in the slot
	g.resetVideo()
	if _, f := g.vsink.take(); f != nil {
		t.Fatal("sink still holds a stale frame")
	}
	if g.vid.rgba != nil || g.vid.img != nil || g.vid.pending {
		t.Fatal("staged picture kept after reset")
	}
}
