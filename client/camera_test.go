package client

import (
	"errors"
	"testing"

	"github.com/medeirosvictor/hermec/core/proto"
)

func TestCameraAndScreenStartStopIndependently(t *testing.T) {
	s := newStub(t, true)
	s.mediaLanes = true
	c := dialStub(t, s)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	if err := c.JoinVoice(testCtx(t), "lobby", nopSource{stop: stop}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartCamera(&countSource{}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartShare(&countSource{}, 16, 16, 15, 100); err != nil {
		t.Fatal(err)
	}
	cl := c.getCall()
	if cl.curPublisher(proto.MediaCamera) == nil || cl.curPublisher(proto.MediaScreen) == nil {
		t.Fatal("camera and screen did not run independently")
	}
	if err := c.StopCamera(); err != nil {
		t.Fatal(err)
	}
	if cl.curPublisher(proto.MediaCamera) != nil || cl.curPublisher(proto.MediaScreen) == nil {
		t.Fatal("stopping camera changed the screen publisher")
	}
}

func TestCameraUnsupportedWithoutMediaLanes(t *testing.T) {
	s := newStub(t, true)
	c := dialStub(t, s)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	if err := c.JoinVoice(testCtx(t), "lobby", nopSource{stop: stop}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartCamera(&countSource{}); !errors.Is(err, ErrCameraUnsupported) {
		t.Fatalf("StartCamera = %v, want ErrCameraUnsupported", err)
	}
}

func TestE2EMixedVersionScreenCompatibility(t *testing.T) {
	s := newStub(t, true)
	c := dialStub(t, s)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	if err := c.JoinVoice(testCtx(t), "lobby", nopSource{stop: stop}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartShare(&countSource{}, 16, 16, 15, 100); err != nil {
		t.Fatalf("legacy screen share: %v", err)
	}
	if !s.waitType(proto.TypeShareStart) {
		t.Fatal("screen share did not use the legacy share_start message")
	}
	if err := c.StartCamera(&countSource{}); !errors.Is(err, ErrCameraUnsupported) {
		t.Fatalf("camera on legacy server = %v, want ErrCameraUnsupported", err)
	}
}

func TestAdoptCallCarriesActivePublishersAndScreenSubscription(t *testing.T) {
	oldServer, newServer := newStub(t, true), newStub(t, true)
	oldServer.mediaLanes, newServer.mediaLanes = true, true
	oldClient, newClient := dialStub(t, oldServer), dialStub(t, newServer)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	if err := oldClient.JoinVoice(testCtx(t), "lobby", nopSource{stop: stop}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := oldClient.StartCamera(&countSource{}); err != nil {
		t.Fatal(err)
	}
	if err := oldClient.StartShare(&countSource{}, 16, 16, 15, 100); err != nil {
		t.Fatal(err)
	}
	if err := oldClient.SubscribeScreen("screen-publisher"); err != nil {
		t.Fatal(err)
	}
	oldCall := oldClient.getCall()
	camera := oldCall.curPublisher(proto.MediaCamera)
	screen := oldCall.curPublisher(proto.MediaScreen)
	if err := newClient.AdoptCall(testCtx(t), oldClient); err != nil {
		t.Fatal(err)
	}
	newCall := newClient.getCall()
	if newCall.curPublisher(proto.MediaCamera) != camera || newCall.curPublisher(proto.MediaScreen) != screen {
		t.Fatal("active publisher ownership was not transferred exactly once")
	}
	if got := newClient.watchSnapshot().fp; got != "screen-publisher" {
		t.Fatalf("restored screen subscription = %q", got)
	}
}

func TestStopDuringAdoptionWinsOverSavedCameraState(t *testing.T) {
	oldServer, newServer := newStub(t, true), newStub(t, true)
	oldServer.mediaLanes, newServer.mediaLanes = true, true
	gate := make(chan struct{})
	newServer.offerGate = gate
	oldClient, newClient := dialStub(t, oldServer), dialStub(t, newServer)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	if err := oldClient.JoinVoice(testCtx(t), "lobby", nopSource{stop: stop}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := oldClient.StartCamera(&countSource{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- newClient.AdoptCall(testCtx(t), oldClient) }()
	if !newServer.waitType(proto.TypeRTCOffer) {
		t.Fatal("adoption never reached the new media offer")
	}
	if err := oldClient.StopCamera(); err != nil {
		t.Fatal(err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if newClient.getCall().curPublisher(proto.MediaCamera) != nil {
		t.Fatal("camera stopped during adoption was restored")
	}
	if newServer.sawType(proto.TypeMediaStart) {
		t.Fatal("stopped camera was redeclared on the new host")
	}
}
