package client

import (
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

// MediaFrame is one decoded frame from a stable modern media slot.
type MediaFrame struct {
	Kind        proto.MediaKind
	Fingerprint string
	Slot        int
	Gen         uint32
	Frame       *video.Frame
}

// MediaSink receives decoded modern camera and screen frames. Calls from
// different slots may overlap; the sink owns each Frame.
type MediaSink interface{ WriteMediaFrame(MediaFrame) }

type mediaSourceState struct {
	fingerprint string
	seq         uint16
	gen         uint32
	version     uint64
}

func (c *Client) SetMediaSink(sink MediaSink) {
	c.mediaSinkMu.Lock()
	c.mediaSinkValue = sink
	c.mediaSinkMu.Unlock()
}

func (c *Client) mediaSink() MediaSink {
	c.mediaSinkMu.RLock()
	defer c.mediaSinkMu.RUnlock()
	return c.mediaSinkValue
}

func (c *Client) onMediaSource(source proto.MediaSource) {
	key := slotKey{Kind: source.Kind, Index: source.Slot}
	if _, ok := parseSlotTrackID(slotTrackID(key)); !ok {
		return
	}
	if key.Kind == proto.MediaScreen {
		if source.Gen == 0 || !c.onVideoSource(proto.VideoSource{
			Fingerprint: source.Fingerprint, Seq: source.Seq, Gen: source.Gen,
		}) {
			return
		}
	}
	c.mediaMu.Lock()
	if c.mediaSources == nil {
		c.mediaSources = make(map[slotKey]mediaSourceState)
	}
	state := c.mediaSources[key]
	state.fingerprint, state.seq, state.gen = source.Fingerprint, source.Seq, source.Gen
	state.version++
	c.mediaSources[key] = state
	c.mediaMu.Unlock()
}

func (c *Client) mediaSource(key slotKey) mediaSourceState {
	c.mediaMu.RLock()
	defer c.mediaMu.RUnlock()
	return c.mediaSources[key]
}

type mediaReceiver struct {
	c   *Client
	key slotKey
	pli func()

	gate    srcGate
	decoder *video.Decoder
	state   mediaSourceState
	latest  chan queuedMediaFrame
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

type queuedMediaFrame struct {
	frame   MediaFrame
	version uint64
}

func drainLatest(latest <-chan queuedMediaFrame) {
	for {
		select {
		case <-latest:
		default:
			return
		}
	}
}

func newMediaReceiver(c *Client, key slotKey, pli func()) (*mediaReceiver, error) {
	decoder, err := video.NewDecoder()
	if err != nil {
		return nil, err
	}
	r := &mediaReceiver{c: c, key: key, pli: pli, decoder: decoder, latest: make(chan queuedMediaFrame, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go r.deliver()
	return r, nil
}

func (r *mediaReceiver) deliver() {
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case queued := <-r.latest:
			state := r.c.mediaSource(r.key)
			if state.version != queued.version || state.fingerprint != queued.frame.Fingerprint {
				continue
			}
			if sink := r.c.mediaSink(); sink != nil {
				sink.WriteMediaFrame(queued.frame)
			} else if r.key.Kind == proto.MediaScreen {
				if sink := r.c.videoSink(); sink != nil {
					sink.WriteVideoFrame(queued.frame.Fingerprint, queued.frame.Frame)
				}
			}
		}
	}
}

func (r *mediaReceiver) reset(state mediaSourceState) bool {
	r.state = state
	r.gate.stop()
	drainLatest(r.latest)
	r.decoder.Close()
	decoder, err := video.NewDecoder()
	if err != nil {
		return false
	}
	r.decoder = decoder
	if state.fingerprint != "" {
		r.gate.start(state.seq)
		if r.pli != nil {
			r.pli()
		}
	}
	return true
}

func (r *mediaReceiver) push(packet *rtp.Packet) {
	state := r.c.mediaSource(r.key)
	if state.version != r.state.version && !r.reset(state) {
		return
	}
	if state.fingerprint == "" {
		return
	}
	frames, lost, needKey := r.gate.push(packet)
	if (lost || needKey) && r.pli != nil {
		r.pli()
	}
	for _, data := range frames {
		frame, err := r.decoder.Decode(data)
		if err != nil || frame == nil {
			if err != nil && r.pli != nil {
				r.pli()
			}
			continue
		}
		r.c.vstats.recvFrames.Add(1)
		out := queuedMediaFrame{frame: MediaFrame{Kind: r.key.Kind, Fingerprint: state.fingerprint, Slot: r.key.Index, Gen: state.gen, Frame: frame}, version: state.version}
		select {
		case r.latest <- out:
		default:
			select {
			case <-r.latest:
			default:
			}
			select {
			case r.latest <- out:
			default:
			}
		}
	}
}

func (r *mediaReceiver) close() {
	r.once.Do(func() {
		close(r.stop)
		<-r.done
		r.decoder.Close()
	})
}

func (cl *call) readMediaVideo(pc *webrtc.PeerConnection, remote *webrtc.TrackRemote, key slotKey) {
	defer cl.wg.Done()
	var limiter pliLimiter
	receiver, err := newMediaReceiver(cl.c, key, func() {
		if limiter.allow(time.Now(), false) {
			_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())}})
		}
	})
	if err != nil {
		cl.c.emitNB(Event{Err: err})
		return
	}
	defer receiver.close()
	for {
		packet, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		cl.c.vstats.recvBytes.Add(uint64(len(packet.Payload)))
		receiver.push(packet)
	}
}
