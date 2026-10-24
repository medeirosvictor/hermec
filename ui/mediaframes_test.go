package ui

import (
	"sync"
	"testing"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

func TestMediaFramesKeepNewestPerSourceWithoutCrossTalk(t *testing.T) {
	var frames mediaFrames
	inputs := []client.MediaFrame{
		{Kind: proto.MediaCamera, Fingerprint: "ann", Slot: 0, Frame: video.NewFrame(4, 4)},
		{Kind: proto.MediaCamera, Fingerprint: "bob", Slot: 1, Frame: video.NewFrame(8, 8)},
		{Kind: proto.MediaScreen, Fingerprint: "ann", Slot: 0, Gen: 3, Frame: video.NewFrame(16, 9)},
	}
	var wg sync.WaitGroup
	for _, frame := range inputs {
		frame := frame
		wg.Add(1)
		go func() {
			defer wg.Done()
			frames.WriteMediaFrame(frame)
		}()
	}
	wg.Wait()

	got := frames.take()
	if len(got) != 3 {
		t.Fatalf("frames = %+v, want three independent sources", got)
	}
	for _, want := range inputs {
		key := mediaViewKey{Kind: want.Kind, Fingerprint: want.Fingerprint}
		if got[key].Frame != want.Frame {
			t.Fatalf("%+v frame crossed sources", key)
		}
	}
}

func TestFrameFromRetiredGenerationCannotReplaceReassignedTile(t *testing.T) {
	var frames mediaFrames
	current := client.MediaFrame{Kind: proto.MediaScreen, Fingerprint: "new", Slot: 0, Gen: 8, Frame: video.NewFrame(8, 8)}
	frames.WriteMediaFrame(current)
	_ = frames.take()
	frames.WriteMediaFrame(client.MediaFrame{Kind: proto.MediaScreen, Fingerprint: "old", Slot: 0, Gen: 7, Frame: video.NewFrame(4, 4)})
	if got := frames.take(); len(got) != 0 {
		t.Fatalf("retired generation replaced the slot: %+v", got)
	}
}

func TestSelectingScreenDoesNotClearCameraFrames(t *testing.T) {
	g, _ := shareGame(t)
	g.shareOps = (&recorder{}).ops(twoMonitors(), true)
	g.mframes.WriteMediaFrame(client.MediaFrame{Kind: proto.MediaCamera, Fingerprint: "annfp", Slot: 0, Frame: video.NewFrame(4, 4)})
	g.mframes.WriteMediaFrame(client.MediaFrame{Kind: proto.MediaCamera, Fingerprint: "bobfp", Slot: 1, Frame: video.NewFrame(8, 8)})
	g.stepMediaFrames()

	g.selectScreen("annfp")
	g.selectScreen("bobfp")
	g.selectScreen("")
	for _, fp := range []string{"annfp", "bobfp"} {
		if g.mediaViews[mediaViewKey{Kind: proto.MediaCamera, Fingerprint: fp}] == nil {
			t.Fatalf("camera %q was cleared while selecting a screen", fp)
		}
	}
}

func TestEndCallMediaClearsRemoteViewsAndPendingFrames(t *testing.T) {
	g, _ := shareGame(t)
	key := mediaViewKey{Kind: proto.MediaCamera, Fingerprint: "annfp"}
	g.mediaViews = map[mediaViewKey]*videoView{key: {rgba: frameRGBA(video.NewFrame(4, 4))}}
	g.mframes.WriteMediaFrame(client.MediaFrame{Kind: proto.MediaCamera, Fingerprint: "annfp", Slot: 0, Frame: video.NewFrame(8, 8)})

	g.endCallMedia()
	g.stepMediaFrames()

	if len(g.mediaViews) != 0 {
		t.Fatalf("remote media survived call teardown: %+v", g.mediaViews)
	}
}
