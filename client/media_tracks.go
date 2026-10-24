package client

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

func newVideoTracks(streamID string) (map[proto.MediaKind]*webrtc.TrackLocalStaticSample, error) {
	tracks := make(map[proto.MediaKind]*webrtc.TrackLocalStaticSample, 2)
	for kind, id := range map[proto.MediaKind]string{
		proto.MediaCamera: cameraPublishTrackID,
		proto.MediaScreen: screenPublishTrackID,
	} {
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: videoClock,
		}, id, streamID)
		if err != nil {
			return nil, err
		}
		tracks[kind] = track
	}
	return tracks, nil
}

// addInitialMediaTracks installs the stable media shape before the first
// offer. Legacy calls keep the historical audio-only/on-demand screen shape.
func addInitialMediaTracks(pc *webrtc.PeerConnection, audio *webrtc.TrackLocalStaticSample, video map[proto.MediaKind]*webrtc.TrackLocalStaticSample, modern, legacyScreen bool) (map[proto.MediaKind]*webrtc.RTPSender, error) {
	if _, err := pc.AddTrack(audio); err != nil {
		return nil, err
	}
	senders := make(map[proto.MediaKind]*webrtc.RTPSender, 2)
	if !modern {
		if !legacyScreen {
			return senders, nil
		}
		tr, err := pc.AddTransceiverFromTrack(video[proto.MediaScreen], webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		if err != nil {
			return nil, err
		}
		senders[proto.MediaScreen] = tr.Sender()
		return senders, nil
	}
	for _, kind := range []proto.MediaKind{proto.MediaCamera, proto.MediaScreen} {
		tr, err := pc.AddTransceiverFromTrack(video[kind], webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		if err != nil {
			return nil, err
		}
		senders[kind] = tr.Sender()
	}
	for i := 0; i < proto.MaxCameraSlots+1; i++ {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			return nil, err
		}
	}
	return senders, nil
}
