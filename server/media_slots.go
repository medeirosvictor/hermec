package server

import (
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

type videoSlot struct {
	key    slotKey
	sender *webrtc.RTPSender
	track  *webrtc.TrackLocalStaticRTP
	out    *outStream
	fwd    *forwarder
	rw     *rewrite
	source string
	gen    uint32
}

func (p *peer) mediaForwarder(kind proto.MediaKind) *forwarder {
	for _, f := range p.fwds {
		if f.video && f.kind == kind && !f.isDead() {
			return f
		}
	}
	return nil
}

func (p *peer) slotForSource(kind proto.MediaKind, fp string) *videoSlot {
	for key, slot := range p.slots {
		if key.Kind == kind && slot.source == fp {
			return slot
		}
	}
	return nil
}

func (p *peer) freeCameraSlot() *videoSlot {
	for i := 0; i < proto.MaxCameraSlots; i++ {
		if slot := p.slots[slotKey{Kind: proto.MediaCamera, Index: i}]; slot != nil && slot.source == "" {
			return slot
		}
	}
	return nil
}

func (vs *voiceSession) startModernMedia(p *peer, kind proto.MediaKind) {
	if p.activeMedia == nil {
		p.activeMedia = make(map[proto.MediaKind]bool)
	}
	if p.activeMedia[kind] {
		return
	}
	p.activeMedia[kind] = true
	if kind == proto.MediaCamera {
		vs.cameraOrder = append(vs.cameraOrder, p)
	}
	for _, viewer := range vs.peers {
		if viewer != p && viewer.c.hasCapability(proto.CapabilityMediaLanesV1) {
			vs.syncModernViewer(viewer)
		}
	}
}

func (vs *voiceSession) stopModernMedia(p *peer, kind proto.MediaKind) {
	if !p.activeMedia[kind] {
		return
	}
	delete(p.activeMedia, kind)
	if kind == proto.MediaCamera {
		for i, candidate := range vs.cameraOrder {
			if candidate == p {
				vs.cameraOrder = append(vs.cameraOrder[:i], vs.cameraOrder[i+1:]...)
				break
			}
		}
	}
	for _, viewer := range vs.peers {
		if viewer == p || !viewer.c.hasCapability(proto.CapabilityMediaLanesV1) {
			continue
		}
		if slot := viewer.slotForSource(kind, p.c.fingerprint); slot != nil {
			vs.attachSlot(viewer, slot, nil, slot.gen)
		}
		if kind == proto.MediaScreen && viewer.screenTarget == p {
			viewer.screenTarget = nil
		}
	}
}

func (vs *voiceSession) removeModernPublisher(p *peer) {
	vs.stopModernMedia(p, proto.MediaCamera)
	vs.stopModernMedia(p, proto.MediaScreen)
}

func (vs *voiceSession) syncModernViewer(viewer *peer) {
	if viewer.pc == nil || len(viewer.slots) == 0 {
		return
	}
	for _, slot := range viewer.slots {
		if slot.source == "" || slot.fwd == nil {
			continue
		}
		publisher := slot.fwd.pub
		if publisher == viewer || !publisher.activeMedia[slot.key.Kind] || publisher.mediaForwarder(slot.key.Kind) != slot.fwd {
			vs.attachSlot(viewer, slot, nil, slot.gen)
		}
	}
	for _, publisher := range vs.cameraOrder {
		if publisher == viewer || !publisher.activeMedia[proto.MediaCamera] || viewer.slotForSource(proto.MediaCamera, publisher.c.fingerprint) != nil {
			continue
		}
		fwd := publisher.mediaForwarder(proto.MediaCamera)
		if fwd == nil {
			continue
		}
		if slot := viewer.freeCameraSlot(); slot != nil {
			vs.attachSlot(viewer, slot, fwd, 0)
		}
	}
	if slot := viewer.slots[slotKey{Kind: proto.MediaScreen, Index: 0}]; slot != nil && viewer.screenTarget != nil {
		publisher := viewer.screenTarget
		if publisher != viewer && publisher.activeMedia[proto.MediaScreen] {
			if fwd := publisher.mediaForwarder(proto.MediaScreen); fwd != nil && slot.fwd != fwd {
				vs.attachSlot(viewer, slot, fwd, viewer.screenGen)
			}
		}
	}
}

func (vs *voiceSession) setModernSubscription(viewer, publisher *peer, kind proto.MediaKind, gen uint32) {
	slot := viewer.slots[slotKey{Kind: kind, Index: 0}]
	viewer.screenTarget, viewer.screenGen = publisher, gen
	if publisher == nil {
		if slot != nil {
			vs.attachSlot(viewer, slot, nil, gen)
		}
		return
	}
	if slot != nil {
		vs.attachSlot(viewer, slot, publisher.mediaForwarder(kind), gen)
	}
}

func (vs *voiceSession) attachSlot(viewer *peer, slot *videoSlot, fwd *forwarder, gen uint32) {
	if slot.fwd == fwd {
		changedGen := slot.gen != gen
		slot.gen = gen
		if fwd != nil && slot.rw != nil {
			vs.sendMediaSource(viewer, slot, slot.rw.bseq)
		} else if changedGen {
			vs.sendMediaSource(viewer, slot, 0)
		}
		return
	}
	if slot.fwd != nil {
		slot.fwd.removeSink(viewer)
	}
	if slot.rw != nil {
		slot.rw.retire()
		slot.rw = nil
	}
	slot.fwd, slot.source, slot.gen = nil, "", gen
	if fwd == nil {
		vs.sendMediaSource(viewer, slot, 0)
		return
	}
	if slot.out == nil {
		slot.out = &outStream{}
	}
	slot.fwd = fwd
	slot.source = fwd.pub.c.fingerprint
	slot.rw = slot.out.attach(nil)
	vs.sendMediaSource(viewer, slot, slot.rw.bseq)
	fwd.addSink(viewer, slot.track, viewer.pc, slot.rw)
	vs.requestForwarderKeyframe(fwd, true)
}

func (vs *voiceSession) sendMediaSource(viewer *peer, slot *videoSlot, seq uint16) {
	raw, err := proto.Encode(proto.TypeMediaSource, proto.MediaSource{
		Kind: slot.key.Kind, Slot: slot.key.Index, Fingerprint: slot.source, Seq: seq, Gen: slot.gen,
	})
	if err == nil && !viewer.c.enqueue(raw) {
		dropSlow([]*conn{viewer.c})
	}
}

func (vs *voiceSession) requestForwarderKeyframe(fwd *forwarder, force bool) {
	if fwd == nil || fwd.isDead() || fwd.pub.pc == nil {
		return
	}
	now := time.Now()
	min := relayPLIInterval
	if force {
		min = forcedPLIInterval
	}
	if now.Sub(fwd.pub.lastPLI) < min {
		return
	}
	fwd.pub.lastPLI = now
	_ = fwd.pub.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(fwd.remote.SSRC())}})
}

func (vs *voiceSession) readMediaRTCP(sender *webrtc.RTPSender, viewer *peer, key slotKey) {
	defer vs.wg.Done()
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, packet := range pkts {
			switch packet.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				vs.post(func() {
					if slot := viewer.slots[key]; slot != nil {
						vs.requestForwarderKeyframe(slot.fwd, false)
					}
				})
			}
		}
	}
}
