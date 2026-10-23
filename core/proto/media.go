package proto

// MediaKind identifies one independently controlled media lane.
type MediaKind string

const (
	MediaAudio  MediaKind = "audio"
	MediaCamera MediaKind = "camera"
	MediaScreen MediaKind = "screen"

	CapabilityMediaLanesV1 = "media_lanes_v1"
	MaxCameraSlots         = 10
)

// Priority is the lane's semantic importance; lower values are protected
// first. It is metadata for future congestion policy, not transport behavior.
func (kind MediaKind) Priority() int {
	switch kind {
	case MediaAudio:
		return 0
	case MediaScreen:
		return 1
	case MediaCamera:
		return 2
	default:
		return 3
	}
}

// MediaStart declares one of the sender's media lanes active.
type MediaStart struct {
	Kind MediaKind `json:"kind"`
}

// MediaStop declares one of the sender's media lanes inactive.
type MediaStop struct {
	Kind MediaKind `json:"kind"`
}

// MediaSubscribe selects the source of a subscription-controlled lane.
// An empty Fingerprint clears the subscription.
type MediaSubscribe struct {
	Kind        MediaKind `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	Gen         uint32    `json:"gen,omitempty"`
}

// MediaSource identifies the source and RTP boundary assigned to a receiving
// slot. An empty Fingerprint detaches the slot; Seq is then unused.
type MediaSource struct {
	Kind        MediaKind `json:"kind"`
	Fingerprint string    `json:"fingerprint"`
	Slot        int       `json:"slot"`
	Seq         uint16    `json:"seq"`
	Gen         uint32    `json:"gen,omitempty"`
}
