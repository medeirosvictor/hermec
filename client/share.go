package client

import (
	"errors"
	"fmt"
	"log"
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
var ErrNoFrame = video.ErrNoFrame

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

// videoSourceWait is how long a viewer waits for the server's video_source
// after Watch before falling back to starting on the first packet.
const videoSourceWait = 3 * time.Second

// srcGate sits in front of a videoRx and enforces the source boundary the
// server announced with video_source: packets before the boundary belong to
// the previous source and are discarded, and no frame is released before the
// first keyframe at or after the boundary, so the decoder never sees a stream
// it has no reference for.
type srcGate struct {
	rx       *videoRx
	active   bool
	boundary uint16
	keyed    bool
}

func (g *srcGate) start(seq uint16) {
	g.rx, g.active, g.boundary, g.keyed = newVideoRx(), true, seq, false
}

func (g *srcGate) stop() { g.rx, g.active = nil, false }

// seqBefore reports whether a precedes b in 16-bit serial-number order
// (RFC 1982).
func seqBefore(a, b uint16) bool { return a != b && int16(a-b) < 0 }

// isVP8Key reports whether a VP8 frame is a keyframe (bit 0 of the frame tag
// is clear).
func isVP8Key(d []byte) bool { return len(d) > 0 && d[0]&1 == 0 }

// push returns the frames to decode. needKey is true when delta frames were
// held back for lack of a keyframe.
func (g *srcGate) push(pkt *rtp.Packet) (frames [][]byte, lost, needKey bool) {
	if !g.active || seqBefore(pkt.SequenceNumber, g.boundary) {
		return nil, false, false
	}
	all, lost := g.rx.push(pkt)
	for _, d := range all {
		if !g.keyed {
			if !isVP8Key(d) {
				needKey = true
				continue
			}
			g.keyed = true
		}
		frames = append(frames, d)
	}
	return frames, lost, needKey
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

// videoPublisher is the sending pipeline for one lane: a grab goroutine polls the source at fps
// into a single "latest" slot, an encode goroutine takes the newest slot at
// fps. Nothing is queued: if the encoder is busy the slot is overwritten and
// the older frame counts as dropped. Three frame buffers circulate (being
// grabbed, in the slot, being encoded).
type videoPublisher struct {
	kind proto.MediaKind
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
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type shareLoop = videoPublisher

func newShareLoop(src FrameSource, enc frameEncoder, w, h, fps int, write func([]byte, time.Duration) error) *videoPublisher {
	return newVideoPublisher(proto.MediaScreen, src, enc, w, h, fps, write)
}

func (sl *videoPublisher) start() {
	sl.wg.Add(2)
	go sl.grabLoop()
	go sl.encodeLoop()
}

// stop ends both goroutines, waits for them and releases the encoder.
func (sl *videoPublisher) stop() {
	sl.stopOnce.Do(func() {
		close(sl.quit)
		sl.wg.Wait()
		sl.enc.Close()
	})
}

// requestKey makes the next encoded frame a keyframe.
func (sl *videoPublisher) requestKey() { sl.forceKey.Store(true) }

// onSenderRTCP handles RTCP read from the video sender: a PLI or FIR forces a
// keyframe.
func (sl *videoPublisher) onSenderRTCP(pkts []rtcp.Packet) {
	for _, p := range pkts {
		switch p.(type) {
		case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
			sl.requestKey()
		}
	}
}

func (sl *videoPublisher) take() *video.Frame { // mu held
	if n := len(sl.free); n > 0 {
		f := sl.free[n-1]
		sl.free = sl.free[:n-1]
		return f
	}
	return video.NewFrame(sl.w, sl.h)
}

func (sl *videoPublisher) grabLoop() {
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

func (sl *videoPublisher) encodeLoop() {
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
	sl := newVideoPublisher(proto.MediaScreen, src, enc, w, h, fps, func(pkt []byte, d time.Duration) error {
		c.vstats.sentBytes.Add(uint64(len(pkt)))
		c.vstats.sentFrames.Add(1)
		return cl.vtracks[proto.MediaScreen].WriteSample(media.Sample{Data: pkt, Duration: d})
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
	if cl.publishers[proto.MediaScreen] != nil {
		cl.smu.Unlock()
		enc.Close()
		return ErrAlreadySharing
	}
	if cl.publishers == nil {
		cl.publishers = make(map[proto.MediaKind]*videoPublisher)
	}
	cl.publishers[proto.MediaScreen] = sl
	sl.start() // under smu: teardown's takeShare then always finds a started loop to stop
	cl.smu.Unlock()

	if err := c.sendPublisherStart(proto.MediaScreen); err != nil {
		if cl.dropPublisher(proto.MediaScreen, sl) {
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
		if sl := cl.takePublisherIf(proto.MediaScreen, nil); sl != nil {
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
	sl := cl.takePublisherIf(proto.MediaScreen, nil)
	if sl == nil {
		return
	}
	go sl.stop()
	_ = cl.c.sendPublisherStop(proto.MediaScreen)
	cl.c.emitNB(Event{Err: fmt.Errorf("%w: video negotiation failed", ErrShareUnsupported)})
}

// VideoEncodeErrors returns how many frames failed to encode so far.
func (c *Client) VideoEncodeErrors() uint64 { return c.vstats.encodeErrors.Load() }

// ShareKeyframe makes the running share's next frame a keyframe (no-op when
// not sharing). The UI asks for one after swapping the captured screen.
func (c *Client) ShareKeyframe() {
	cl := c.getCall()
	if cl == nil {
		return
	}
	cl.smu.Lock()
	defer cl.smu.Unlock()
	if publisher := cl.publishers[proto.MediaScreen]; publisher != nil {
		publisher.requestKey()
	}
}

// StopShare stops sharing and tells the server. It is a no-op when not sharing.
func (c *Client) StopShare() error {
	cl := c.getCall()
	if cl == nil {
		return nil
	}
	return c.stopShare(cl, nil)
}

// stopShare stops cl's share (only sl if non-nil) and tells the server.
func (c *Client) stopShare(cl *call, only *videoPublisher) error {
	sl := cl.takePublisherIf(proto.MediaScreen, only)
	if sl == nil {
		return nil
	}
	sl.stop()
	return c.sendPublisherStop(proto.MediaScreen)
}

func (c *Client) sendPublisherStart(kind proto.MediaKind) error {
	if c.SupportsMediaLanes() {
		return c.send(proto.TypeMediaStart, proto.MediaStart{Kind: kind})
	}
	return c.send(proto.TypeShareStart, proto.ShareStart{})
}

func (c *Client) sendPublisherStop(kind proto.MediaKind) error {
	if c.SupportsMediaLanes() {
		return c.send(proto.TypeMediaStop, proto.MediaStop{Kind: kind})
	}
	return c.send(proto.TypeShareStop, proto.ShareStop{})
}

// Watch asks the server for sharer fp's video; "" stops watching. Decoded
// frames arrive at the VideoSink. Watch("") returns at once. Otherwise Watch
// waits (up to videoSourceWait) for the server's answer, so calls never
// overlap: it returns nil when the server announced the source (video_source),
// the server's error if it refused (not_sharing; the previous watch is then
// still in force and is restored here, the error is also delivered as an Err
// event), or ErrWatchTimeout if no answer came (the new target is kept and the
// first-packet fallback applies). Watch lasts until the sharer stops or you
// stop it: after a share ends it must be called again. Concurrent callers are
// serialized.
func (c *Client) Watch(fp string) error {
	if c.SupportsMediaLanes() {
		err := c.SubscribeScreen(fp)
		var serverErr *ServerError
		if errors.As(err, &serverErr) && serverErr.Code == proto.CodeNotPublishing {
			return &ServerError{Code: proto.CodeNotSharing, Message: serverErr.Message, Gen: serverErr.Gen}
		}
		return err
	}
	return c.watchScreen(fp, false)
}

// SubscribeScreen selects the modern screen lane. On a legacy server it
// preserves Watch's compatibility behavior.
func (c *Client) SubscribeScreen(fp string) error {
	return c.watchScreen(fp, c.SupportsMediaLanes())
}

func (c *Client) watchScreen(fp string, modern bool) error {
	if c.getCall() == nil {
		return ErrNotInVoice
	}
	c.watchCallMu.Lock()
	defer c.watchCallMu.Unlock()
	wire, wait := c.beginWatch(fp)
	var err error
	if modern {
		err = c.send(proto.TypeMediaSubscribe, proto.MediaSubscribe{Kind: proto.MediaScreen, Fingerprint: fp, Gen: wire})
	} else {
		err = c.send(proto.TypeWatch, proto.Watch{Fingerprint: fp, Gen: wire})
	}
	if err != nil {
		c.watchFailed(wire, err) // never sent: nothing changed server side
		c.endWatch(wait)
		return err
	}
	if wait == nil {
		return nil
	}
	timer := time.NewTimer(videoSourceWait)
	defer timer.Stop()
	select {
	case err := <-wait:
		return err
	case <-timer.C:
		c.endWatch(wait)
		select { // an answer that raced the timer wins
		case err := <-wait:
			return err
		default:
		}
		return ErrWatchTimeout
	case <-c.done:
		c.endWatch(wait)
		return ErrClosed
	}
}

// ErrWatchTimeout is returned by Watch when the server did not answer in time.
var ErrWatchTimeout = errors.New("client: no answer to watch")

// beginWatch switches the local watch target and returns the wire gen to send
// and, when an answer is to be awaited, the channel it will arrive on. Calls
// are serialized by Watch, so at most one watch is ever pending.
func (c *Client) beginWatch(fp string) (wire uint32, wait chan error) {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	// Re-watching the sharer we already have a boundary for changes nothing;
	// the wire gen is unchanged, so the server stays silent too.
	if fp == "" || fp != c.watchFP || !c.srcHave {
		prev := c.watchStateLocked()
		c.prevWatch = &prev
		c.watchFP = fp
		c.watchGen++
		c.wireCtr++ // gens are never reused, even across a refused watch
		c.watchWire = c.wireCtr
		c.watchAt = time.Now()
		c.srcHave = false
		if fp != "" {
			wait = make(chan error, 1)
		}
		c.watchWait = wait
	}
	return c.watchWire, wait
}

// endWatch stops waiting on wait (no answer will be acted on any more).
func (c *Client) endWatch(wait chan error) {
	c.watchMu.Lock()
	if wait != nil && c.watchWait == wait {
		c.watchWait = nil
	}
	c.watchMu.Unlock()
}

// watchAnsweredLocked completes the pending watch, if any, with err. Caller holds watchMu.
func (c *Client) watchAnsweredLocked(err error) {
	if c.watchWait != nil {
		c.watchWait <- err // buffered, one answer per wait
		c.watchWait = nil
	}
}

// watchFailed handles the refusal (not_sharing, not_joined) of the watch with
// the given gen, or a failure to send it: the server keeps streaming the
// previous source, so we go back to it and frames stay labelled with it. The
// reader is told (new gen/id) to restart on the restored boundary. The error
// is acted on only if gen is the pending watch's own: a late answer to an
// earlier request (after a timeout) or an error without a gen is ignored.
// watchWire goes back to the gen the server knows (the previous source's),
// while wireCtr keeps counting, so gens stay unique.
func (c *Client) watchFailed(gen uint32, err error) {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	p := c.prevWatch
	if p == nil || c.watchWait == nil || gen == 0 || gen != c.watchWire {
		return
	}
	c.prevWatch = nil
	c.watchFP, c.watchWire = p.fp, p.wire
	c.srcHave, c.srcSeq = p.have, p.seq
	c.watchGen++
	c.srcID++
	c.watchAt = time.Now()
	c.watchAnsweredLocked(err)
}

// onVideoSource records the server's source boundary. Messages for a sharer
// we are not (or no longer) watching are stale and ignored; "" means the
// server detached our video.
func (c *Client) onVideoSource(m proto.VideoSource) bool {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	if m.Gen != 0 && m.Gen != c.watchWire {
		return false
	}
	if m.Fingerprint == "" {
		c.srcHave = false
		return true
	}
	if m.Fingerprint != c.watchFP {
		return false // not the sharer / not the watch request we are waiting on
	}
	c.srcHave, c.srcSeq = true, m.Seq
	c.srcID++
	c.prevWatch = nil
	c.watchAnsweredLocked(nil)
	return true
}

// watchState is a consistent snapshot of the watch target and source boundary.
type watchState struct {
	fp   string
	gen  uint64 // changes on every Watch call
	have bool   // the server announced the source (video_source)
	seq  uint16 // first sequence number of the source
	id   uint64 // changes on every announcement
	at   time.Time
	wire uint32 // gen sent to the server with the Watch
}

func (c *Client) watchStateLocked() watchState {
	return watchState{c.watchFP, c.watchGen, c.srcHave, c.srcSeq, c.srcID, c.watchAt, c.watchWire}
}

// watchSnapshot returns the watch state. The server keeps one video
// transceiver per viewer and swaps its source, so the track's own stream id
// only names the first sharer; the target and boundary come from Watch and
// the server's video_source.
func (c *Client) watchSnapshot() watchState {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	return c.watchStateLocked()
}

// watchTarget returns the sharer we asked to watch and a counter that changes
// on every Watch call.
func (c *Client) watchTarget() (string, uint64) {
	w := c.watchSnapshot()
	return w.fp, w.gen
}

// WatchGen returns the gen of the watch request currently in force (what a
// refused watch's error.gen is compared with); 0 before any Watch.
func (c *Client) WatchGen() uint32 {
	c.watchMu.Lock()
	defer c.watchMu.Unlock()
	return c.watchWire
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

// closePublishers is the teardown path: it detaches every publisher and
// forbids any later publisher start on this call.
func (cl *call) closePublishers() []*videoPublisher {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	cl.sclosed = true
	all := make([]*videoPublisher, 0, len(cl.publishers))
	for kind, publisher := range cl.publishers {
		all = append(all, publisher)
		delete(cl.publishers, kind)
	}
	return all
}

func (cl *call) takePublisherIf(kind proto.MediaKind, only *videoPublisher) *videoPublisher {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	publisher := cl.publishers[kind]
	if publisher == nil || (only != nil && publisher != only) {
		return nil
	}
	delete(cl.publishers, kind)
	return publisher
}

func (cl *call) dropPublisher(kind proto.MediaKind, publisher *videoPublisher) bool {
	return cl.takePublisherIf(kind, publisher) != nil
}

func (cl *call) curPublisher(kind proto.MediaKind) *videoPublisher {
	cl.smu.Lock()
	defer cl.smu.Unlock()
	return cl.publishers[kind]
}

// keyAfterNegotiation forces a keyframe once an answer has been applied: the
// first keyframe may have been written before the sender was bound.
func (cl *call) keyAfterNegotiation() {
	for _, kind := range []proto.MediaKind{proto.MediaCamera, proto.MediaScreen} {
		if publisher := cl.curPublisher(kind); publisher != nil {
			publisher.requestKey()
		}
	}
}

// ensureVideo makes sure the PeerConnection carries our video sender and, if
// it had to add one, renegotiates. Actor only.
func (cl *call) ensureVideo() {
	cl.vidWanted = true
	if cl.pc == nil || cl.vsenders[proto.MediaScreen] != nil {
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
	tr, err := pc.AddTransceiverFromTrack(cl.vtracks[proto.MediaScreen], webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return err
	}
	snd := tr.Sender()
	if cl.vsenders == nil {
		cl.vsenders = make(map[proto.MediaKind]*webrtc.RTPSender)
	}
	cl.vsenders[proto.MediaScreen] = snd
	cl.startVideoRTCP(snd, proto.MediaScreen)
	return nil
}

func (cl *call) startVideoRTCP(snd *webrtc.RTPSender, kind proto.MediaKind) {
	cl.tmu.Lock()
	if cl.tclosed {
		cl.tmu.Unlock()
		return
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
			if publisher := cl.curPublisher(kind); publisher != nil {
				publisher.onSenderRTCP(pkts)
			}
		}
	}()
}

// pliLimiter spaces a viewer's keyframe requests at least pliInterval apart
// unless forced.
type pliLimiter struct{ last time.Time }

func (l *pliLimiter) allow(now time.Time, force bool) bool {
	if !force && now.Sub(l.last) < pliInterval {
		return false
	}
	l.last = now
	return true
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

	var lim pliLimiter
	pli := func(force bool) {
		if !lim.allow(time.Now(), force) {
			return
		}
		_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())}})
	}
	// The first keyframe may have been sent before this track was bound.
	pli(true)

	// start (re)opens the gate at a source boundary: fresh sample builder and
	// decoder, so nothing of an earlier stream is ever decoded as this one,
	// and the new source is asked for a keyframe.
	var g srcGate
	start := func(seq uint16) bool {
		g.start(seq)
		lf.clear()
		dec.Close()
		nd, err := video.NewDecoder()
		if err != nil {
			cl.c.emitNB(Event{Err: err})
			return false
		}
		dec = nd
		pli(true)
		return true
	}
	w := cl.c.watchSnapshot()
	fp, gen := w.fp, w.gen
	var lastID uint64
	warned := false
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		cl.c.vstats.recvBytes.Add(uint64(len(pkt.Payload)))
		w = cl.c.watchSnapshot()
		if w.gen != gen {
			// Watch changed the target: deliver nothing until the server
			// announces where the new source starts.
			fp, gen = w.fp, w.gen
			g.stop()
			lf.clear()
		}
		if fp == "" {
			continue // not watching anyone
		}
		if w.have && w.id != lastID {
			lastID = w.id
			if !start(w.seq) {
				return
			}
		} else if !g.active {
			if w.have || time.Since(w.at) < videoSourceWait {
				continue // boundary unknown (or already handled): this packet cannot be placed
			}
			// No video_source from this server: start on the first packet.
			if !warned {
				warned = true
				log.Printf("client: no video_source from the server %s after watch; starting on the first packet", videoSourceWait)
			}
			if !start(pkt.SequenceNumber) {
				return
			}
		}
		frames, lost, needKey := g.push(pkt)
		// Also keep asking while the gate is open but nothing has come out of
		// it: partial keyframes (first packets sent before the track was
		// bound) produce neither frames nor loss reports. pli is rate limited.
		if lost || needKey || (g.active && !g.keyed) {
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
