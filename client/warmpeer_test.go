package client

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

const warmVoiceCh = "lobby"

// markerSource emits 20ms frames [marker, marker, seq...]; the marker
// identifies the sender end to end.
type markerSource struct {
	marker byte
	seq    atomic.Uint32
}

func (m *markerSource) ReadOpusFrame() ([]byte, error) {
	time.Sleep(20 * time.Millisecond)
	n := m.seq.Add(1)
	return []byte{m.marker, m.marker, byte(n >> 8), byte(n)}, nil
}

// recSink counts the frames that arrived per leading marker byte.
type recSink struct {
	mu      sync.Mutex
	markers map[byte]int
	arrived map[byte][]time.Time
}

func newRecSink() *recSink {
	return &recSink{markers: map[byte]int{}, arrived: map[byte][]time.Time{}}
}

func (r *recSink) WriteOpusFrame(_ string, frame []byte) {
	r.mu.Lock()
	if len(frame) > 0 {
		r.markers[frame[0]]++
		r.arrived[frame[0]] = append(r.arrived[frame[0]], time.Now())
	}
	r.mu.Unlock()
}

// arrival is when the n-th (0-based) frame with marker m arrived.
func (r *recSink) arrival(m byte, n int) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.arrived[m][n]
}

func (r *recSink) marker(m byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.markers[m]
}

// callee is one participant: an active session on A in voice, plus a warm
// session with an open warm peer on B.
type callee struct {
	id     *identity.Identity
	active *Client
	warm   *Client
	src    *markerSource
	sink   *recSink
}

func newCallee(t *testing.T, p *pair, marker byte) *callee {
	t.Helper()
	id := mustIdent(t)
	a, err := Dial(testCtx(t), p.aURL, id, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.SetLoopbackICE(true)
	w, err := DialWith(testCtx(t), p.bURL, id, "a", "", DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w.SetLoopbackICE(true)
	go drain(a)
	go drain(w)
	co := &callee{id: id, active: a, warm: w, src: &markerSource{marker: marker}, sink: newRecSink()}
	if err := a.JoinVoice(testCtx(t), warmVoiceCh, co.src, co.sink); err != nil {
		t.Fatalf("JoinVoice: %v", err)
	}
	if err := w.OpenWarmPeer(testCtx(t)); err != nil {
		t.Fatalf("OpenWarmPeer: %v", err)
	}
	return co
}

func TestWarmPeerOpens(t *testing.T) {
	p := newPair(t)
	c, err := DialWith(testCtx(t), p.bURL, mustIdent(t), "a", "", DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetLoopbackICE(true)
	go drain(c)
	if c.WarmPeerReady() {
		t.Fatal("ready before OpenWarmPeer")
	}
	if err := c.OpenWarmPeer(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "warm peer ready", c.WarmPeerReady)
}

func TestAdoptCallResumesAudio(t *testing.T) {
	p := newPair(t)
	x, y := newCallee(t, p, 'X'), newCallee(t, p, 'Y')
	waitUntil(t, "Y hears X", func() bool { return y.sink.marker('X') > 5 })
	waitUntil(t, "warm peers ready", func() bool { return x.warm.WarmPeerReady() && y.warm.WarmPeerReady() })
	killHost(t, p.a)

	before := y.sink.marker('X') // A is dead: nothing arrives until B carries X again
	for _, co := range []*callee{x, y} {
		if err := co.warm.Takeover(testCtx(t)); err != nil {
			t.Fatalf("Takeover: %v", err)
		}
	}
	var wg sync.WaitGroup
	for _, co := range []*callee{x, y} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := co.warm.AdoptCall(testCtx(t), co.active); err != nil {
				t.Errorf("AdoptCall: %v", err)
			}
		}()
	}
	wg.Wait()
	waitUntil(t, "Y hears X again", func() bool { return y.sink.marker('X') > before })
	// The gap between X's last frame from A and its first from B.
	if gap := y.sink.arrival('X', before).Sub(y.sink.arrival('X', before-1)); gap >= 1500*time.Millisecond {
		t.Fatalf("audio gap across failover = %v, want < 1500ms", gap)
	}
	if x.warm.VoiceChannel() != warmVoiceCh || x.active.VoiceChannel() != "" {
		t.Fatalf("call not moved: new=%q old=%q", x.warm.VoiceChannel(), x.active.VoiceChannel())
	}
}

func TestAdoptCallKeepsMute(t *testing.T) {
	p := newPair(t)
	x := newCallee(t, p, 'X')
	if err := x.active.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "warm peer ready", x.warm.WarmPeerReady)
	killHost(t, p.a)
	if err := x.warm.Takeover(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if err := x.warm.AdoptCall(testCtx(t), x.active); err != nil {
		t.Fatal(err)
	}
	// A second member sees X's voice_state from B.
	o, err := Dial(testCtx(t), p.bURL, mustIdent(t), "o", "")
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	fp := x.warm.Fingerprint()
	if _, ok := waitEvent(o, 3*time.Second, func(e Event) bool { return mutedIn(e.Voice, fp) }); !ok {
		t.Fatal("X is not muted on B")
	}
}

func mutedIn(st *proto.VoiceState, fp string) bool {
	if st == nil {
		return false
	}
	for _, m := range st.Members {
		if m.Fingerprint == fp && m.Muted {
			return true
		}
	}
	return false
}

// A refused adoption leaves from's call alone.
func TestAdoptCallInVoiceKeepsFrom(t *testing.T) {
	p := newPair(t)
	x := newCallee(t, p, 'X')
	c, err := Dial(testCtx(t), p.aURL, mustIdent(t), "c", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetLoopbackICE(true)
	go drain(c)
	if err := c.JoinVoice(testCtx(t), warmVoiceCh, &markerSource{marker: 'C'}, newRecSink()); err != nil {
		t.Fatal(err)
	}
	if err := c.AdoptCall(testCtx(t), x.active); err != ErrInVoice {
		t.Fatalf("err = %v, want ErrInVoice", err)
	}
	if x.active.VoiceChannel() != warmVoiceCh {
		t.Fatal("from lost its call")
	}
}

// A media reset after adoption builds a fresh PeerConnection, not the
// consumed warm one.
func TestAdoptCallResetBuildsFreshPC(t *testing.T) {
	p := newPair(t)
	x := newCallee(t, p, 'X')
	waitUntil(t, "warm peer ready", x.warm.WarmPeerReady)
	killHost(t, p.a)
	if err := x.warm.Takeover(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if err := x.warm.AdoptCall(testCtx(t), x.active); err != nil {
		t.Fatal(err)
	}
	cl := x.warm.getCall()
	var before, after *webrtc.PeerConnection
	done := make(chan struct{})
	cl.post(func() {
		before = cl.pc
		cl.reset()
		after = cl.pc
		close(done)
	})
	<-done
	if before == nil || after == nil || before == after {
		t.Fatalf("reset kept the PC: before=%p after=%p", before, after)
	}
}

// Without a warm peer AdoptCall falls back to a plain join with the same
// source and sink.
func TestAdoptCallFallsBackToJoin(t *testing.T) {
	p := newPair(t)
	id := mustIdent(t)
	a, err := Dial(testCtx(t), p.aURL, id, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.SetLoopbackICE(true)
	go drain(a)
	src, sink := &markerSource{marker: 'X'}, newRecSink()
	if err := a.JoinVoice(testCtx(t), warmVoiceCh, src, sink); err != nil {
		t.Fatal(err)
	}
	w, err := DialWith(testCtx(t), p.bURL, id, "a", "", DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetLoopbackICE(true)
	go drain(w)
	killHost(t, p.a)
	if err := w.Takeover(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if err := w.AdoptCall(testCtx(t), a); err != nil {
		t.Fatal(err)
	}
	if w.VoiceChannel() != warmVoiceCh {
		t.Fatalf("VoiceChannel = %q", w.VoiceChannel())
	}
}
