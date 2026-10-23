package client

import (
	"errors"

	"github.com/medeirosvictor/hermec/core/proto"
)

const (
	cameraWidth  = 640
	cameraHeight = 360
	cameraFPS    = 15
	cameraKbps   = 300
)

var (
	ErrAlreadyCamera     = errors.New("client: camera already active")
	ErrCameraUnsupported = errors.New("client: server does not support camera")
)

// StartCamera publishes src using HERMEC's fixed camera profile.
func (c *Client) StartCamera(src FrameSource) error {
	if !c.SupportsMediaLanes() {
		return ErrCameraUnsupported
	}
	cl := c.getCall()
	if cl == nil {
		return ErrNotInVoice
	}
	if src == nil {
		return errors.New("client: nil camera source")
	}
	enc, err := newEncoder(cameraWidth, cameraHeight, cameraFPS, cameraKbps)
	if err != nil {
		return err
	}
	publisher := newVideoPublisher(proto.MediaCamera, src, enc, cameraWidth, cameraHeight, cameraFPS, nil)
	c.bindPublisher(cl, publisher)
	cl.smu.Lock()
	if cl.sclosed {
		cl.smu.Unlock()
		enc.Close()
		return ErrNotInVoice
	}
	if cl.publishers[proto.MediaCamera] != nil {
		cl.smu.Unlock()
		enc.Close()
		return ErrAlreadyCamera
	}
	cl.publishers[proto.MediaCamera] = publisher
	publisher.start()
	cl.smu.Unlock()
	if err := c.sendPublisherStart(proto.MediaCamera); err != nil {
		if cl.dropPublisher(proto.MediaCamera, publisher) {
			publisher.stop()
		}
		return err
	}
	return nil
}

// StopCamera stops only the camera lane. It is a no-op when camera is off.
func (c *Client) StopCamera() error { return c.stopOwnedPublisher(proto.MediaCamera) }

// CameraKeyframe requests a fresh camera keyframe when camera is active.
func (c *Client) CameraKeyframe() {
	if cl := c.getCall(); cl != nil {
		if publisher := cl.curPublisher(proto.MediaCamera); publisher != nil {
			publisher.requestKey()
		}
	}
}

// stopOwnedPublisher follows an in-progress/adopted call so a user stop
// cannot be undone by a saved failover snapshot.
func (c *Client) stopOwnedPublisher(kind proto.MediaKind) error {
	c.adoptMu.Lock()
	if redirect := c.adoptRedirect; redirect != nil {
		c.adoptMu.Unlock()
		return redirect.stopOwnedPublisher(kind)
	}
	if publisher := c.adoptPending[kind]; publisher != nil {
		delete(c.adoptPending, kind)
		c.adoptMu.Unlock()
		publisher.stop()
		return nil
	}
	cl := c.getCall()
	c.adoptMu.Unlock()
	if cl == nil {
		return nil
	}
	return c.stopPublisher(cl, kind, nil)
}
