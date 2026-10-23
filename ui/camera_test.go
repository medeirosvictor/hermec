package ui

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/medeirosvictor/hermec/cameracap"
	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

type fakeCameraSource struct {
	fail   atomic.Bool
	closes atomic.Int32
}

func (s *fakeCameraSource) Next(frame *video.Frame) error {
	if s.fail.Load() {
		return errors.New("camera disconnected")
	}
	frame.W, frame.H = 4, 4
	frame.Y = make([]byte, 16)
	frame.U, frame.V = make([]byte, 4), make([]byte, 4)
	return nil
}

func (s *fakeCameraSource) Close() error { s.closes.Add(1); return nil }

func TestCameraOpensOnlyAfterExplicitToggleAndPersistsDeviceChoice(t *testing.T) {
	g, _ := shareGame(t)
	g.fp = "me"
	g.set.CameraDevice = "saved-id"
	source := &fakeCameraSource{}
	var opens atomic.Int32
	var openedID string
	g.cameraOps = cameraOps{
		devices: func() ([]cameracap.Device, error) {
			return []cameracap.Device{{ID: "saved-id", Name: "Saved"}, {ID: "next-id", Name: "Next"}}, nil
		},
		open: func(id string) (cameracap.Source, error) { opens.Add(1); openedID = id; return source, nil },
		start: func(_ *client.Client, src client.FrameSource) error {
			return src.Next(video.NewFrame(4, 4))
		},
	}
	if opens.Load() != 0 {
		t.Fatal("joining voice opened the camera")
	}
	g.toggleCamera()
	settle(t, g)
	g.stepCamera()
	g.stepMediaFrames()
	if opens.Load() != 1 || openedID != "saved-id" || !g.camera.on {
		t.Fatalf("opens=%d id=%q on=%v", opens.Load(), openedID, g.camera.on)
	}
	if g.mediaViews[mediaViewKey{Kind: proto.MediaCamera, Fingerprint: g.fp}] == nil {
		t.Fatal("local camera preview did not reach the self tile")
	}
	g.devCamera = []cameracap.Device{{ID: "saved-id", Name: "Saved"}, {ID: "next-id", Name: "Next"}}
	g.cycleCameraDevice()
	if g.set.CameraDevice != "next-id" {
		t.Fatalf("saved camera = %q", g.set.CameraDevice)
	}
}

func TestCameraFailureFallsBackToAvatarWithoutStoppingOtherMedia(t *testing.T) {
	g, s := shareGame(t)
	g.fp = "me"
	source := &fakeCameraSource{}
	g.cameraOps = cameraOps{
		open:  func(string) (cameracap.Source, error) { return source, nil },
		start: func(_ *client.Client, src client.FrameSource) error { return nil },
	}
	g.share.on = true
	g.toggleCamera()
	settle(t, g)
	source.fail.Store(true)
	_ = g.camera.src.Next(video.NewFrame(4, 4))
	g.stepCamera()
	settle(t, g)
	if g.camera.on || source.closes.Load() != 1 || s.st.Status == "" {
		t.Fatalf("on=%v closes=%d status=%q", g.camera.on, source.closes.Load(), s.st.Status)
	}
	if !g.share.on || !g.inCall() {
		t.Fatal("camera failure stopped screen or voice")
	}
	if g.mediaViews[mediaViewKey{Kind: proto.MediaCamera, Fingerprint: g.fp}] != nil {
		t.Fatal("failed camera preview did not fall back to avatar")
	}
}

func TestLeavingCallClosesCameraAndScreenSources(t *testing.T) {
	g, s := shareGame(t)
	g.fp = "me"
	camera := &fakeCameraSource{}
	g.cameraOps.stop = func(*client.Client) error { return nil }
	g.camera = cameraState{on: true, src: &cameraSource{src: camera}, c: s.c}
	screen := twoMonitors()
	g.shareOps.stop = func(*client.Client) {}
	g.share = shareState{on: true, src: &captureSource{src: screen}, c: s.c}

	g.endCallMedia()
	settle(t, g)
	if camera.closes.Load() != 1 || !screen.closed.Load() {
		t.Fatalf("camera closes=%d screen closed=%v", camera.closes.Load(), screen.closed.Load())
	}
	if g.camera.on || g.share.on {
		t.Fatalf("controls stayed active: camera=%v screen=%v", g.camera.on, g.share.on)
	}
}
