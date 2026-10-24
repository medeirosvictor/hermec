package ui

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/medeirosvictor/hermec/cameracap"
	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

type cameraOps struct {
	devices func() ([]cameracap.Device, error)
	open    func(string) (cameracap.Source, error)
	start   func(*client.Client, client.FrameSource) error
	stop    func(*client.Client) error
}

type cameraState struct {
	on, busy bool
	gen      uint64
	src      *cameraSource
	c        *client.Client
	cancel   chan struct{}
}

type cameraSource struct {
	src cameracap.Source
	tap func(*video.Frame)

	mu   sync.Mutex
	err  error
	once sync.Once
}

func (s *cameraSource) Next(frame *video.Frame) error {
	err := s.src.Next(frame)
	if err != nil && !errors.Is(err, client.ErrNoFrame) {
		s.mu.Lock()
		if s.err == nil {
			s.err = err
		}
		s.mu.Unlock()
		return err
	}
	if err == nil && s.tap != nil {
		preview := video.NewFrame(frame.W, frame.H)
		copy(preview.Y, frame.Y)
		copy(preview.U, frame.U)
		copy(preview.V, frame.V)
		s.tap(preview)
	}
	return err
}

func (s *cameraSource) Close() error {
	var err error
	s.once.Do(func() { err = s.src.Close() })
	return err
}

func (s *cameraSource) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (g *game) cameraSupported() bool { return g.cameraOps.open != nil || cameracap.Supported }

func (g *game) toggleCamera() {
	if g.camera.busy {
		if g.camera.cancel != nil {
			close(g.camera.cancel)
			g.camera.cancel = nil
			g.camera.gen++
		}
		return
	}
	if g.camera.on {
		g.stopCamera()
		return
	}
	s := g.callSess()
	if s == nil || s.c == nil {
		return
	}
	if !g.cameraSupported() {
		s.st.Status = "camera: unsupported"
		return
	}
	if g.cameraOps.start == nil && !s.c.SupportsMediaLanes() {
		s.st.Status = "server does not support webcams"
		return
	}
	g.camera.gen++
	gen, c, deviceID, fp := g.camera.gen, s.c, g.set.CameraDevice, g.fp
	cancel := make(chan struct{})
	g.camera.busy, g.camera.c, g.camera.cancel = true, c, cancel
	open := g.cameraOps.open
	if open == nil {
		open = cameracap.Open
	}
	start := g.cameraOps.start
	if start == nil {
		start = func(c *client.Client, src client.FrameSource) error { return c.StartCamera(src) }
	}
	stop := g.cameraOps.stop
	if stop == nil {
		stop = func(c *client.Client) error { return c.StopCamera() }
	}
	g.media.do(func() {
		raw, err := open(deviceID)
		var src *cameraSource
		if err == nil {
			src = &cameraSource{src: raw, tap: func(frame *video.Frame) {
				g.mframes.WriteMediaFrame(client.MediaFrame{Kind: proto.MediaCamera, Fingerprint: fp, Slot: -1, Frame: frame})
			}}
			select {
			case <-cancel:
				_ = src.Close()
				g.media.post(func() {
					if g.camera.c == c && !g.camera.on {
						g.camera.busy, g.camera.c, g.camera.cancel = false, nil, nil
					}
				})
				return
			default:
			}
			err = start(c, src)
			select {
			case <-cancel:
				if err == nil {
					_ = stop(c)
				}
				_ = src.Close()
				g.media.post(func() {
					if g.camera.c == c && !g.camera.on {
						g.camera.busy, g.camera.c, g.camera.cancel = false, nil, nil
					}
				})
				return
			default:
			}
		}
		g.media.post(func() {
			current := g.callSess()
			if g.camera.gen != gen || current == nil || current.c != c {
				g.camera.busy, g.camera.c, g.camera.cancel = false, nil, nil
				g.media.do(func() {
					if err == nil {
						_ = stop(c)
					}
					if src != nil {
						_ = src.Close()
					}
				})
				return
			}
			g.camera.busy, g.camera.cancel = false, nil
			if err != nil {
				if src != nil {
					_ = src.Close()
				}
				current.st.Status = cameraNotice(err)
				return
			}
			g.camera.on, g.camera.src, g.camera.c = true, src, c
		})
	})
}

func cameraNotice(err error) string {
	var serverErr *client.ServerError
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, cameracap.ErrUnsupported), errors.Is(err, client.ErrCameraUnsupported):
		return "server does not support webcams"
	case errors.As(err, &serverErr) && serverErr.Code == proto.CodeMediaFull:
		return "camera limit reached for the room"
	case strings.Contains(message, "permission"), strings.Contains(message, "access denied"):
		return "camera permission denied"
	default:
		return fmt.Sprintf("camera device unavailable or disconnected: %v", err)
	}
}

func (g *game) stopCamera() {
	cam := g.camera
	g.camera.gen++
	if cam.busy && cam.cancel != nil {
		close(cam.cancel)
	}
	g.camera.on, g.camera.busy, g.camera.src, g.camera.c, g.camera.cancel = false, false, nil, nil, nil
	g.retireMediaView(mediaViewKey{Kind: proto.MediaCamera, Fingerprint: g.fp})
	if cam.src == nil {
		return
	}
	stop := g.cameraOps.stop
	if stop == nil {
		stop = func(c *client.Client) error { return c.StopCamera() }
	}
	g.media.do(func() {
		if cam.c != nil {
			_ = stop(cam.c)
		}
		_ = cam.src.Close()
	})
}

func (g *game) stepCamera() {
	if g.camera.on && g.camera.src != nil {
		if err := g.camera.src.failure(); err != nil {
			if s := g.callSess(); s != nil {
				s.st.Status = cameraNotice(err)
			}
			g.stopCamera()
		}
	}
}

func (g *game) cycleCameraDevice() {
	ids := make([]string, 0, len(g.devCamera))
	for _, device := range g.devCamera {
		ids = append(ids, device.ID)
	}
	g.set.CameraDevice = state.NextDevice(g.set.CameraDevice, ids)
	g.persistSettings()
	if g.camera.on {
		g.cur().st.Status = "applies the next time camera starts"
	}
}
