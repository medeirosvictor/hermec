package client

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
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
// from an internal goroutine (one per watched stream, which survives a switch
// of the watched sharer) with the newest decoded frame only: older undelivered
// frames are dropped. It must not block for long. Calls may overlap briefly
// around a Watch change, so the sink must be safe for concurrent use. fp is
// the sharer being watched when the frame was decoded. The sink owns f.
type VideoSink interface {
	WriteVideoFrame(fp string, f *video.Frame)
}

// ErrAlreadySharing is returned by StartShare while a share is active.
var ErrAlreadySharing = errors.New("client: already sharing")

const (
	videoClock    = 90000
	maxPacketLate = 128                    // packets the sample builder buffers before skipping
	maxFrameDelay = 400 * time.Millisecond // RTP-time a frame waits for a missing packet
	pliInterval   = time.Second            // at most one viewer PLI per second
	statsMinSpan  = 250 * time.Millisecond
)

// newVideoSampleBuilder assembles VP8 frames. WithMaxTimeDelay matters:
// without it one unrepaired loss blocks every later frame until the buffer
// overflows.
func newVideoSampleBuilder() *samplebuilder.SampleBuilder {
	return samplebuilder.New(maxPacketLate, &codecs.VP8Packet{}, videoClock,
		samplebuilder.WithMaxTimeDelay(maxFrameDelay))
}

// videoRx turns the RTP packets of one remote VP8 stream into frames. It does
// not ask for keyframes on a sequence gap: NACK normally repairs those, and a
// PLI before the retransmission arrives costs a keyframe for nothing (on a
// lossy link that is near-constant keyframes). Loss only counts once the
// sample builder has given up on the packets.
type videoRx struct{ sb *samplebuilder.SampleBuilder }

func newVideoRx() *videoRx { return &videoRx{sb: newVideoSampleBuilder()} }

// push adds pkt (in any order, late retransmissions included) and returns the
// frames that are now complete. lost is true when packets before one of those
// frames were dropped unrepaired, so the decoder lost its reference.
func (r *videoRx) push(pkt *rtp.Packet) (frames [][]byte, lost bool) {
	r.sb.Push(pkt)
	for s := r.sb.Pop(); s != nil; s = r.sb.Pop() {
		if s.PrevDroppedPackets > 0 {
			lost = true
		}
		frames = append(frames, s.Data)
	}
	return frames, lost
}

// newEncoder builds the VP8 encoder (a variable so tests can substitute one).
var newEncoder = func(w, h, fps, kbps int) (frameEncoder, error) {
	e, err := video.NewEncoder(w, h, fps, kbps)
	if err != nil {
		return nil, err
	}
	return e, nil
}

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
	// onEncodeErr is called for each failed Encode.
	onEncodeErr func(error)

	mu    sync.Mutex
	slot  *video.Frame // newest unencoded frame
	cur   *video.Frame // last frame handed to the encoder (kept for forced keyframes)
	free  []*video.Frame
	fresh bool

	forceKey atomic.Bool
	dropped  atomic.Uint64

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
			if sl.fresh { // the encoder never took the previous one: overwrite it
				sl.dropped.Add(1)
				sl.free = append(sl.free, sl.slot)
			}
			sl.slot, sl.fresh = buf, true
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

		key := sl.forceKey.Swap(false)
		pkt, _, err := sl.enc.Encode(f, key)
		if err != nil {
			if key {
				sl.forceKey.Store(true) // the request must survive a failed encode
			}
			if sl.onEncodeErr != nil {
				sl.onEncodeErr(err)
			}
			continue
		}
		if len(pkt) == 0 {
			continue // rate control dropped it; the encoder carries a pending key over
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
	if w <= 0 || h <= 0 || fps < 1 || fps > 60 || kbps <= 0 {
		return fmt.Errorf("client: invalid share parameters %dx%d @%d fps, %d kbps", w, h, fps, kbps)
	}
	enc, err := newEncoder(w, h, fps, kbps)
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
		go func() { _ = c.stopShare(cl, sl) }() // only this share, not a later one
	}
	sl.onEncodeErr = func(err error) {
		if c.vstats.encodeErrors.Add(1) == 1 {
			c.emitNB(Event{Err: fmt.Errorf("client: screen share encode: %w", err)})
		}
	}
	cl.smu.Lock()
	if cl.sclosed { // the call is being torn down: nobody would ever stop this loop
		cl.smu.Unlock()
		enc.Close()
		return ErrNotInVoice
	}
	if cl.share != nil {
		cl.smu.Unlock()
		enc.Close()
		return ErrAlreadySharing
	}
	cl.share = sl
	sl.start() // under smu: teardown's takeShare then always finds a started loop to stop
	cl.smu.Unlock()

	if err := c.send(proto.TypeShareStart, proto.ShareStart{}); err != nil {
		if cl.dropShare(sl) {
			sl.stop()
		}
		return err
	}
	// The video sender is added once the server's voice_state shows us sharing
	// (onVoiceState): a pre-v0.5 host never confirms, so it never sees a video
	// m-line it cannot answer, and a bad_request for share_start ends the
	// share (shareUnsupported).
	return nil
}

// ErrShareUnsupported reports that the host cannot carry screen share. It is
// delivered as an Err event; the share it ended is already stopped.
var ErrShareUnsupported = errors.New("this server does not support screen share (host needs v0.5.0+)")

const msgShareStartUnsupported = "unsupported message type " + proto.TypeShareStart

// shareUnsupported ends our share without telling the server (a v0.4 host
// would only answer share_stop with another bad_request) and reports
// ErrShareUnsupported. Read loop only; it never blocks on the share loops.
func (c *Client) shareUnsupported() {
	if cl := c.getCall(); cl != nil {
		if sl := cl.takeShareIf(nil); sl != nil {
			go sl.stop()
		}
		cl.post(func() { cl.vidWanted = false })
	}
	c.emit(Event{Err: ErrShareUnsupported})
}

// dropVideo gives up on sending video after negotiation kept failing: the
// share ends (the server is told) and later PeerConnections of this call are
// built audio-only, so voice survives a host that cannot carry video. Actor only.
func (cl *call) dropVideo() {
	cl.vidWanted = false
	sl := cl.takeShareIf(nil)
	if sl == nil {
		return
	}
	go sl.stop()
	_ = cl.c.send(proto.TypeShareStop, proto.ShareStop{})
	cl.c.emitNB(Event{Err: fmt.Errorf("%w: video negotiation failed", ErrShareUnsupported)})
}

// VideoEncodeErrors returns how many frames failed to encode so far.
func (c *Client) VideoEncodeErrors() uint64 { return c.vstats.encodeErrors.Load() }

// StopShare stops sharing and tells the server. It is a no-op when not sharing.
func (c *Client) StopShare() error {
	cl := c.getCall()
	if cl == nil {
		return nil
	}
	return c.stopShare(cl, nil)
}

// stopShare stops cl's share (only sl if non-nil) and tells the server.
func (c *Client) stopShare(cl *call, only *shareLoop) error {
	sl := cl.takeShareIf(only)
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
	c.watchMu.Lock()
	c.watchFP = fp
	c.watchGen++
	c.watchMu.Unlock()
	return c.send(proto.TypeWatch, proto.Watch{Fingerprint: fp})
}

// watchTarget returns the sharer we asked to watch and a counter that changes
// on every Watch call. The server keeps one video transceiver per viewer and
// swaps its source, so the track's own stream id only names the first sharer.
func (c *Client) watchTarget() (string, uint64) {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	return c.watchFP, c.watchGen
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
	encodeErrors           atomic.Uint64

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

// closeShare is the teardown path: it takes the current share and forbids
// any later StartShare on this call.
func (cl *call) closeShare() *shareLoop {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	cl.sclosed = true
	sl := cl.share
	cl.share = nil
	return sl
}

// takeShareIf detaches the current share if it is only (or any, when only is nil).
func (cl *call) takeShareIf(only *shareLoop) *shareLoop {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	sl := cl.share
	if sl == nil || (only != nil && sl != only) {
		return nil
	}
	cl.share = nil
	return sl
}

// dropShare detaches sl if it is still the current share.
func (cl *call) dropShare(sl *shareLoop) bool {
	return cl.takeShareIf(sl) != nil
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
	fp   string
	gen  uint64
	wake chan struct{}
}

func (l *latestFrame) put(f *video.Frame, fp string, gen uint64) {
	l.mu.Lock()
	l.f, l.fp, l.gen = f, fp, gen
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func (l *latestFrame) take() (*video.Frame, string, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, fp, gen := l.f, l.fp, l.gen
	l.f = nil
	return f, fp, gen
}

func (l *latestFrame) clear() {
	l.mu.Lock()
	l.f = nil
	l.mu.Unlock()
}

// readVideo receives one remote VP8 track: depacketize, decode, hand the
// newest frame to the sink on its own goroutine. Runs under cl.wg; ends when
// the track does.
func (cl *call) readVideo(pc *webrtc.PeerConnection, remote *webrtc.TrackRemote) {
	defer cl.wg.Done()
	dec, err := video.NewDecoder()
	if err != nil {
		cl.c.emitNB(Event{Err: err})
		return
	}
	defer func() { dec.Close() }()

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
				if f, fp, gen := lf.take(); f != nil {
					// A frame decoded before a Watch change must not outlive it.
					if _, cur := cl.c.watchTarget(); cur != gen {
						continue
					}
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

	rx := newVideoRx()
	fp, gen := cl.c.watchTarget()
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		cl.c.vstats.recvBytes.Add(uint64(len(pkt.Payload)))
		if nfp, ngen := cl.c.watchTarget(); ngen != gen {
			// The server swapped the source under this one track: start over
			// so nothing of the old stream is decoded as the new one, and ask
			// the new source for a keyframe.
			fp, gen = nfp, ngen
			rx = newVideoRx()
			lf.clear()
			dec.Close()
			nd, err := video.NewDecoder()
			if err != nil {
				cl.c.emitNB(Event{Err: err})
				return
			}
			dec = nd
			pli(true)
		}
		if fp == "" {
			continue // not watching anyone
		}
		frames, lost := rx.push(pkt)
		if lost {
			pli(false) // NACK did not repair it: only a keyframe resyncs the decoder
		}
		for _, data := range frames {
			if cl.c.videoSink() == nil {
				continue
			}
			f, err := dec.Decode(data)
			if err != nil {
				pli(false)
				continue
			}
			if f == nil {
				continue
			}
			cl.c.vstats.recvFrames.Add(1)
			lf.put(f, fp, gen)
		}
	}
}
