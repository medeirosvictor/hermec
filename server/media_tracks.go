package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

const (
	cameraPublishTrackID = "camera-publish"
	screenPublishTrackID = "screen-publish"
)

type slotKey struct {
	Kind  proto.MediaKind
	Index int
}

func slotTrackID(key slotKey) string {
	return fmt.Sprintf("%s-slot-%d", key.Kind, key.Index)
}

func parseSlotTrackID(id string) (slotKey, bool) {
	for _, kind := range []proto.MediaKind{proto.MediaCamera, proto.MediaScreen} {
		prefix := string(kind) + "-slot-"
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		i, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
		if err != nil || i < 0 || (kind == proto.MediaCamera && i >= proto.MaxCameraSlots) || (kind == proto.MediaScreen && i != 0) {
			return slotKey{}, false
		}
		return slotKey{Kind: kind, Index: i}, true
	}
	return slotKey{}, false
}

type offeredMedia struct {
	kind      string
	mid       string
	direction string
	trackID   string
}

type modernOfferLayout struct {
	valid   bool
	audio   bool
	publish map[string]proto.MediaKind
	slots   map[string]slotKey
}

func parseOfferedMedia(raw string) []offeredMedia {
	var media []offeredMedia
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "m=") {
			fields := strings.Fields(strings.TrimPrefix(line, "m="))
			if len(fields) != 0 {
				media = append(media, offeredMedia{kind: fields[0], direction: "sendrecv"})
			}
			continue
		}
		if len(media) == 0 {
			continue
		}
		m := &media[len(media)-1]
		switch {
		case strings.HasPrefix(line, "a=mid:"):
			m.mid = strings.TrimPrefix(line, "a=mid:")
		case line == "a=sendonly":
			m.direction = "sendonly"
		case line == "a=recvonly":
			m.direction = "recvonly"
		case line == "a=inactive":
			m.direction = "inactive"
		case strings.HasPrefix(line, "a=msid:"):
			fields := strings.Fields(strings.TrimPrefix(line, "a=msid:"))
			if len(fields) > 1 {
				m.trackID = fields[1]
			}
		}
	}
	return media
}

func classifyModernOffer(media []offeredMedia) modernOfferLayout {
	layout := modernOfferLayout{publish: make(map[string]proto.MediaKind), slots: make(map[string]slotKey)}
	var video []offeredMedia
	for _, m := range media {
		if m.kind == "audio" && (m.direction == "sendrecv" || m.direction == "sendonly") {
			layout.audio = true
		}
		if m.kind == "video" {
			video = append(video, m)
		}
	}
	if !layout.audio || len(video) != 2+proto.MaxCameraSlots+1 {
		return layout
	}
	if video[0].direction != "sendonly" || video[0].trackID != cameraPublishTrackID ||
		video[1].direction != "sendonly" || video[1].trackID != screenPublishTrackID {
		return layout
	}
	for _, m := range video[2:] {
		if m.direction != "recvonly" {
			return layout
		}
	}
	layout.valid = true
	layout.publish[video[0].mid] = proto.MediaCamera
	layout.publish[video[1].mid] = proto.MediaScreen
	for i, m := range video[2:] {
		key := slotKey{Kind: proto.MediaCamera, Index: i}
		if i == proto.MaxCameraSlots {
			key = slotKey{Kind: proto.MediaScreen, Index: 0}
		}
		layout.slots[m.mid] = key
	}
	return layout
}

func (vs *voiceSession) configureModernAnswer(p *peer, pc *webrtc.PeerConnection, layout modernOfferLayout) error {
	p.videoReceivers = make(map[*webrtc.RTPReceiver]proto.MediaKind)
	if p.slots == nil {
		p.slots = make(map[slotKey]*videoSlot)
	}
	if !layout.valid {
		return nil
	}
	for _, tr := range pc.GetTransceivers() {
		if tr.Kind() != webrtc.RTPCodecTypeVideo {
			continue
		}
		if kind, ok := layout.publish[tr.Mid()]; ok {
			p.videoReceivers[tr.Receiver()] = kind
			continue
		}
		key, ok := layout.slots[tr.Mid()]
		if !ok {
			continue
		}
		if _, exists := p.slots[key]; exists {
			continue
		}
		track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}},
		}, slotTrackID(key), "hermec-media")
		if err != nil {
			return err
		}
		sender, err := pc.AddTrack(track)
		if err != nil {
			return err
		}
		if tr.Sender() != sender {
			return fmt.Errorf("media slot %s bound to unexpected transceiver", slotTrackID(key))
		}
		p.slots[key] = &videoSlot{key: key, sender: sender, track: track}
		vs.wg.Add(1)
		go vs.readMediaRTCP(sender, p, key)
	}
	return nil
}
