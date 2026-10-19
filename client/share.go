package client

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

// FrameSource supplies the screen to share. Next fills dst (already sized to
// the w x h given to StartShare) with the most recently captured picture and
// returns at once: it must not wait for a new capture. ErrNoFrame means
// nothing new has been captured since the last call; any other error ends the
// share (an Err event is emitted). Next is only ever called from one goroutine.
type FrameSource interface {
	Next(dst *video.Frame) error
}

// ErrNoFrame is returned by FrameSource.Next when there is no new picture.
var ErrNoFrame = errors.New("client: no new frame")

// VideoSink receives decoded frames of the sharer being watched. It is called
// from one goroutine per watched stream with the newest decoded frame only
// (older undelivered frames are dropped); it may take its time but a slow sink
// just loses frames. The sink owns f.
type VideoSink interface {
	WriteVideoFrame(fp string, f *video.Frame)
}

// ErrAlreadySharing is returned by StartShare while a share is active.
var ErrAlreadySharing = errors.New("client: already sharing")

const (
	videoClock    = 90000
	maxPacketLate = 512         // packets the sample builder waits for before skipping
	pliInterval   = time.Second // at most one viewer PLI per second
	statsMinSpan  = 250 * time.Millisecond
)

// frameEncoder is video.Encoder, abstracted so tests can slow it down.
type frameEncoder interface {
	Encode(f *video.Frame, forceKey bool) (pkt []byte, key bool, err error)
	Close()
}

// shareLoop is the sending pipeline: a grab goroutine polls the source at fps
// into a single "latest" slot, an encode goroutine takes the newest slot at
// fps. Nothing is queued: if the encoder is busy the slot is overwritten and
// the older frame counts as dropped. Three frame buffers circulate (being
// grabbed, in the slot, being encoded).
type shareLoop struct {
	src  FrameSource
	enc  frameEncoder
	w, h int
	fps  int
	// write sends one encoded frame. pkt is only valid during the call
	// (the encoder reuses it); WriteSample packetizes synchronously.
	write func(pkt []byte, d time.Duration) error
	// onFail is called (once, from the grab goroutine) when the source fails.
	onFail func(error)

	mu    sync.Mutex
	slot  *video.Frame // newest unencoded frame
	cur   *video.Frame // last frame handed to the encoder (kept for forced keyframes)
	free  []*video.Frame
	fresh bool

	forceKey   atomic.Bool
	dropped    atomic.Uint64
	maxPending atomic.Int64

	quit     chan struct{}
	quitOnce sync.Once
	wg       sync.WaitGroup
}

func newShareLoop(src FrameSource, enc frameEncoder, w, h, fps int, write func([]byte, time.Duration) error) *shareLoop {
	sl := &shareLoop{src: src, enc: enc, w: w, h: h, fps: fps, write: write, quit: make(chan struct{})}
	sl.forceKey.Store(true)
	return sl
}

func (sl *shareLoop) start() {
	sl.wg.Add(2)
	go sl.grabLoop()
	go sl.encodeLoop()
}

// stop ends both goroutines, waits for them and releases the encoder.
func (sl *shareLoop) stop() {
	sl.quitOnce.Do(func() { close(sl.quit) })
	sl.wg.Wait()
	sl.enc.Close()
}

// requestKey makes the next encoded frame a keyframe.
func (sl *shareLoop) requestKey() { sl.forceKey.Store(true) }

// onSenderRTCP handles RTCP read from the video sender: a PLI or FIR forces a
// keyframe.
func (sl *shareLoop) onSenderRTCP(pkts []rtcp.Packet) {
	for _, p := range pkts {
		switch p.(type) {
		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			sl.requestKey()
		}
	}
}

func (sl *shareLoop) take() *video.Frame { // mu held
	if n := len(sl.free); n > 0 {
		f := sl.free[n-1]
		sl.free = sl.free[:n-1]
		return f
	}
	return video.NewFrame(sl.w, sl.h)
}

func (sl *shareLoop) grabLoop() {
	defer sl.wg.Done()
	t := time.NewTicker(time.Second / time.Duration(sl.fps))
	defer t.Stop()
	for {
		select {
		case <-sl.quit:
			return
		case <-t.C:
		}
		sl.mu.Lock()
		buf := sl.take()
		sl.mu.Unlock()
		err := sl.src.Next(buf)
		sl.mu.Lock()
		switch {
		case err != nil:
			sl.free = append(sl.free, buf)
		default:
			pending := int64(1)
			if sl.fresh {
				sl.dropped.Add(1)
				sl.free = append(sl.free, sl.slot)
			}
			sl.slot, sl.fresh = buf, true
			if pending > sl.maxPending.Load() {
				sl.maxPending.Store(pending)
			}
		}
		sl.mu.Unlock()
		if err != nil && !errors.Is(err, ErrNoFrame) {
			if sl.onFail != nil {
				sl.onFail(err)
			}
			return
		}
	}
}

func (sl *shareLoop) encodeLoop() {
	defer sl.wg.Done()
	t := time.NewTicker(time.Second / time.Duration(sl.fps))
	defer t.Stop()
	last := time.Time{}
	for {
		select {
		case <-sl.quit:
			return
		case <-t.C:
		}
		sl.mu.Lock()
		switch {
		case sl.fresh:
			if sl.cur != nil {
				sl.free = append(sl.free, sl.cur)
			}
			sl.cur, sl.slot, sl.fresh = sl.slot, nil, false
		case sl.cur != nil && sl.forceKey.Load():
			// Nothing new on screen but a keyframe was asked for (PLI, new
			// watcher): re-encode the last picture.
		default:
			sl.mu.Unlock()
			continue
		}
		f := sl.cur
		sl.mu.Unlock()

		pkt, _, err := sl.enc.Encode(f, sl.forceKey.Swap(false))
		if err != nil || len(pkt) == 0 {
			continue
		}
		now := time.Now()
		d := time.Second / time.Duration(sl.fps)
		if !last.IsZero() {
			d = min(max(now.Sub(last), time.Millisecond), time.Second)
		}
		last = now
		_ = sl.write(pkt, d)
	}
}

// ---- client API ----

// StartShare begins sharing the screen supplied by src as a w x h VP8 stream
// at fps frames per second and about kbps kilobits per second. The call must
// be active (JoinVoice). Viewers pick the stream up with Watch.
func (c *Client) StartShare(src FrameSource, w, h, fps, kbps int) error {
	cl := c.getCall()
	if cl == nil {
		return ErrNotInVoice
	}
	if src == nil {
		return errors.New("client: nil frame source")
	}
	enc, err := video.NewEncoder(w, h, fps, kbps)
	if err != nil {
		return err
	}
	sl := newShareLoop(src, enc, w, h, fps, func(pkt []byte, d time.Duration) error {
		c.vstats.sentBytes.Add(uint64(len(pkt)))
		c.vstats.sentFrames.Add(1)
		return cl.vtrack.WriteSample(media.Sample{Data: pkt, Duration: d})
	})
	sl.onFail = func(err error) {
		c.emitNB(Event{Err: fmt.Errorf("client: screen share ended: %w", err)})
		go func() { _ = c.StopShare() }()
	}
	cl.smu.Lock()
	if cl.share != nil {
		cl.smu.Unlock()
		enc.Close()
		return ErrAlreadySharing
	}
	cl.share = sl
	cl.smu.Unlock()

	if err := c.send(proto.TypeShareStart, proto.ShareStart{}); err != nil {
		cl.dropShare(sl)
		sl.stop()
		return err
	}
	cl.post(cl.ensureVideo) // adds the video sender (one renegotiation) the first time
	sl.start()
	return nil
}

// StopShare stops sharing and tells the server. It is a no-op when not sharing.
func (c *Client) StopShare() error {
	cl := c.getCall()
	if cl == nil {
		return nil
	}
	sl := cl.takeShare()
	if sl == nil {
		return nil
	}
	sl.stop()
	return c.send(proto.TypeShareStop, proto.ShareStop{})
}

// Watch asks the server for sharer fp's video; "" stops watching. Decoded
// frames arrive at the VideoSink. A failure (the member is not sharing) comes
// back as an Err event. Watch lasts until the sharer stops or you stop it:
// after a share ends it must be called again.
func (c *Client) Watch(fp string) error {
	if c.getCall() == nil {
		return ErrNotInVoice
	}
	return c.send(proto.TypeWatch, proto.Watch{Fingerprint: fp})
}

// SetVideoSink sets the receiver of decoded video (nil discards video).
func (c *Client) SetVideoSink(s VideoSink) {
	c.vsinkMu.Lock()
	c.vsink = s
	c.vsinkMu.Unlock()
}

func (c *Client) videoSink() VideoSink {
	c.vsinkMu.RLock()
	defer c.vsinkMu.RUnlock()
	return c.vsink
}

// videoStats are cumulative counters plus the window of the last VideoStats call.
type videoStats struct {
	sentBytes, recvBytes   atomic.Uint64
	sentFrames, recvFrames atomic.Uint64

	mu                             sync.Mutex
	at                             time.Time
	lastSent, lastRecv             uint64
	lastSentFrames, lastRecvFrames uint64
	send, recv, fps                float64
}

// VideoStats reports video throughput since the previous call (cached when
// called more often than every 250 ms): sendKbps of encoded video we send,
// recvKbps of video we receive, and fps, which is the decoded frame rate when
// watching and otherwise the encoded frame rate when sharing.
func (c *Client) VideoStats() (sendKbps, recvKbps, fps float64) {
	s := &c.vstats
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.at.IsZero() {
		s.at = c.started
	}
	span := now.Sub(s.at)
	if span >= statsMinSpan {
		sent, recv := s.sentBytes.Load(), s.recvBytes.Load()
		sf, rf := s.sentFrames.Load(), s.recvFrames.Load()
		secs := span.Seconds()
		s.send = float64(sent-s.lastSent) * 8 / 1000 / secs
		s.recv = float64(recv-s.lastRecv) * 8 / 1000 / secs
		if rf > s.lastRecvFrames {
			s.fps = float64(rf-s.lastRecvFrames) / secs
		} else {
			s.fps = float64(sf-s.lastSentFrames) / secs
		}
		s.at, s.lastSent, s.lastRecv, s.lastSentFrames, s.lastRecvFrames = now, sent, recv, sf, rf
	}
	return s.send, s.recv, s.fps
}

// ---- call side (actor and goroutines) ----

func (cl *call) takeShare() *shareLoop {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	sl := cl.share
	cl.share = nil
	return sl
}

func (cl *call) dropShare(sl *shareLoop) {
	cl.smu.Lock()
	if cl.share == sl {
		cl.share = nil
	}
	cl.smu.Unlock()
}

func (cl *call) curShare() *shareLoop {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	return cl.share
}

// keyAfterNegotiation forces a keyframe once an answer has been applied: the
// first keyframe may have been written before the sender was bound.
func (cl *call) keyAfterNegotiation() {
	if sl := cl.curShare(); sl != nil {
		sl.requestKey()
	}
}

// ensureVideo makes sure the PeerConnection carries our video sender and, if
// it had to add one, renegotiates. Actor only.
func (cl *call) ensureVideo() {
	cl.vidWanted = true
	if cl.pc == nil || cl.vsnd != nil {
		return // no PC yet: newPC adds the sender to the offer it is about to make
	}
	if err := cl.addVideoSender(cl.pc); err != nil {
		cl.fail(fmt.Errorf("client: add video sender: %w", err))
		return
	}
	cl.offerSoon()
}

// offerSoon offers now if the signalling state allows, otherwise when the
// current exchange ends (the answer handler checks offerDirty). Actor only.
func (cl *call) offerSoon() {
	if cl.pc == nil || !cl.answered || cl.offerOut || cl.resetting {
		cl.offerDirty = true
		return
	}
	cl.startOffer()
}

func (cl *call) addVideoSender(pc *webrtc.PeerConnection) error {
	tr, err := pc.AddTransceiverFromTrack(cl.vtrack, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return err
	}
	snd := tr.Sender()
	cl.vsnd = snd
	cl.tmu.Lock()
	if cl.tclosed {
		cl.tmu.Unlock()
		return nil
	}
	cl.wg.Add(1)
	cl.tmu.Unlock()
	go func() { // sender RTCP: PLI/FIR force a keyframe; also keeps interceptors running
		defer cl.wg.Done()
		for {
			pkts, _, err := snd.ReadRTCP()
			if err != nil {
				return
			}
			if sl := cl.curShare(); sl != nil {
				sl.onSenderRTCP(pkts)
			}
		}
	}()
	return nil
}

// latestFrame hands the newest decoded frame to a sink goroutine, replacing
// any frame it has not picked up yet.
type latestFrame struct {
	mu   sync.Mutex
	f    *video.Frame
	wake chan struct{}
}

func (l *latestFrame) put(f *video.Frame) {
	l.mu.Lock()
	l.f = f
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *latestFrame) take() *video.Frame {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.f
	l.f = nil
	return f
}

// readVideo receives one remote VP8 track: depacketize, decode, hand the
// newest frame to the sink on its own goroutine. Runs under cl.wg; ends when
// the track does.
func (cl *call) readVideo(pc *webrtc.PeerConnection, remote *webrtc.TrackRemote) {
	defer cl.wg.Done()
	fp := remote.StreamID()
	dec, err := video.NewDecoder()
	if err != nil {
		cl.c.emitNB(Event{Err: err})
		return
	}
	defer dec.Close()

	lf := &latestFrame{wake: make(chan struct{}, 1)}
	done := make(chan struct{})
	delivered := make(chan struct{})
	go func() {
		defer close(delivered)
		for {
			select {
			case <-done:
				return
			case <-lf.wake:
				if f := lf.take(); f != nil {
					if sink := cl.c.videoSink(); sink != nil {
						sink.WriteVideoFrame(fp, f)
					}
				}
			}
		}
	}()
	defer func() { close(done); <-delivered }()

	var lastPLI time.Time
	pli := func(force bool) {
		now := time.Now()
		if !force && now.Sub(lastPLI) < pliInterval {
			return
		}
		lastPLI = now
		_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())}})
	}
	// The first keyframe may have been sent before this track was bound.
	pli(true)

	sb := samplebuilder.New(maxPacketLate, &codecs.VP8Packet{}, videoClock)
	var lastSeq uint16
	haveSeq := false
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		cl.c.vstats.recvBytes.Add(uint64(len(pkt.Payload)))
		if haveSeq {
			if d := int16(pkt.SequenceNumber - lastSeq); d > 1 {
				pli(false) // a gap: NACK may repair it, the PLI is the fallback
			} else if d <= 0 {
				sb.Push(pkt)
				continue // late retransmission: keep the high-water mark
			}
		}
		lastSeq, haveSeq = pkt.SequenceNumber, true
		sb.Push(pkt)
		for s := sb.Pop(); s != nil; s = sb.Pop() {
			if cl.c.videoSink() == nil {
				continue
			}
			f, err := dec.Decode(s.Data)
			if err != nil {
				pli(false)
				continue
			}
			if f == nil {
				continue
			}
			cl.c.vstats.recvFrames.Add(1)
			lf.put(f)
		}
	}
}
