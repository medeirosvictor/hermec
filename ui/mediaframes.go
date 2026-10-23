package ui

import (
	"image"
	"sync"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

type mediaViewKey struct {
	Kind        proto.MediaKind
	Fingerprint string
}

type mediaSlotKey struct {
	Kind proto.MediaKind
	Slot int
}

type mediaSlotSource struct {
	Fingerprint string
	Gen         uint32
}

// mediaFrames is the thread-safe handoff from decoder goroutines to the game
// goroutine. Each source owns an independent newest-frame slot.
type mediaFrames struct {
	mu      sync.Mutex
	pending map[mediaViewKey]client.MediaFrame
	slots   map[mediaSlotKey]mediaSlotSource
	retired map[mediaViewKey]bool
}

func genBefore(a, b uint32) bool { return a != b && int32(a-b) < 0 }

func (m *mediaFrames) WriteMediaFrame(frame client.MediaFrame) {
	if frame.Frame == nil || frame.Fingerprint == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = make(map[mediaViewKey]client.MediaFrame)
		m.slots = make(map[mediaSlotKey]mediaSlotSource)
		m.retired = make(map[mediaViewKey]bool)
	}
	slot := mediaSlotKey{Kind: frame.Kind, Slot: frame.Slot}
	previous, have := m.slots[slot]
	if have && previous.Gen != 0 && frame.Gen != 0 && genBefore(frame.Gen, previous.Gen) {
		return
	}
	key := mediaViewKey{Kind: frame.Kind, Fingerprint: frame.Fingerprint}
	if have && previous.Fingerprint != "" && previous.Fingerprint != frame.Fingerprint {
		old := mediaViewKey{Kind: frame.Kind, Fingerprint: previous.Fingerprint}
		delete(m.pending, old)
		m.retired[old] = true
	}
	delete(m.retired, key)
	m.slots[slot] = mediaSlotSource{Fingerprint: frame.Fingerprint, Gen: frame.Gen}
	m.pending[key] = frame
}

func (m *mediaFrames) take() map[mediaViewKey]client.MediaFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.pending
	m.pending = make(map[mediaViewKey]client.MediaFrame)
	return out
}

func (m *mediaFrames) takeRetired() []mediaViewKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mediaViewKey, 0, len(m.retired))
	for key := range m.retired {
		out = append(out, key)
	}
	clear(m.retired)
	return out
}

func (g *game) retireMediaView(key mediaViewKey) {
	view := g.mediaViews[key]
	if view == nil {
		return
	}
	view.reset()
	view.rgba = nil
	if view.img != nil {
		view.img.Deallocate()
	}
	delete(g.mediaViews, key)
}

func stageMediaFrame(view *videoView, frame *video.Frame) {
	if frame == nil || frame.W <= 0 || frame.H <= 0 {
		return
	}
	if view.rgba == nil || view.rgba.Bounds().Dx() != frame.W || view.rgba.Bounds().Dy() != frame.H {
		view.rgba = image.NewRGBA(image.Rect(0, 0, frame.W, frame.H))
	}
	frame.RGBA(view.rgba)
	view.w, view.h, view.pending = frame.W, frame.H, true
}

func (g *game) stepMediaFrames() {
	if g.mediaViews == nil {
		g.mediaViews = make(map[mediaViewKey]*videoView)
	}
	for _, key := range g.mframes.takeRetired() {
		g.retireMediaView(key)
	}
	for key, frame := range g.mframes.take() {
		view := g.mediaViews[key]
		if view == nil {
			view = &videoView{}
			g.mediaViews[key] = view
		}
		stageMediaFrame(view, frame.Frame)
	}
}
