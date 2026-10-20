package integration

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

const (
	shareW, shareH = 320, 240
	shareFPS       = 15
	shareKbps      = 800
)

// synthSource paints a moving pattern so encoded frames differ.
type synthSource struct{ n atomic.Int64 }

func (s *synthSource) Next(dst *video.Frame) error {
	n := int(s.n.Add(1))
	for y := 0; y < dst.H; y++ {
		row := dst.Y[y*dst.W : (y+1)*dst.W]
		for x := range row {
			row[x] = byte(x + y + n*7)
		}
	}
	for i := range dst.U {
		dst.U[i] = byte(128 + n)
	}
	for i := range dst.V {
		dst.V[i] = byte(128 - n)
	}
	return nil
}

// recVideo records decoded frames per sharer.
type recVideo struct {
	mu    sync.Mutex
	count map[string]int
	first map[string]time.Time
	size  map[string][2]int
	sizes map[string]map[[2]int]int // every decoded size seen per label
}

func newRecVideo() *recVideo {
	return &recVideo{count: map[string]int{}, first: map[string]time.Time{}, size: map[string][2]int{},
		sizes: map[string]map[[2]int]int{}}
}

func (r *recVideo) sizesOf(fp string) map[[2]int]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[[2]int]int{}
	for k, v := range r.sizes[fp] {
		out[k] = v
	}
	return out
}

func (r *recVideo) WriteVideoFrame(fp string, f *video.Frame) {
	if f == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count[fp] == 0 {
		r.first[fp] = time.Now()
		r.size[fp] = [2]int{f.W, f.H}
	}
	if r.sizes[fp] == nil {
		r.sizes[fp] = map[[2]int]int{}
	}
	r.sizes[fp][[2]int{f.W, f.H}]++
	r.count[fp]++
}

func (r *recVideo) n(fp string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count[fp]
}

func (r *recVideo) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := 0
	for _, v := range r.count {
		t += v
	}
	return t
}

func (v *voiceClient) sharing(fp string) bool {
	st, ok := v.lastState(voiceCh)
	if !ok {
		return false
	}
	for _, m := range st.Members {
		if m.Fingerprint == fp && m.Sharing {
			return true
		}
	}
	return false
}

func TestShareWatchEndToEnd(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	c := newVoiceClient(t, url, "carol", 0xC3)
	bv, cv := newRecVideo(), newRecVideo()
	b.c.SetVideoSink(bv)
	c.c.SetVideoSink(cv)
	a.join(t)
	b.join(t)
	c.join(t)

	if err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatalf("StartShare: %v", err)
	}
	eventually(t, 10*time.Second, "B sees A sharing", func() bool { return b.sharing(a.fp) })
	if err := b.c.Watch(a.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "B receives video", func() bool { return bv.n(a.fp) >= 10 })

	bv.mu.Lock()
	sz := bv.size[a.fp]
	bv.mu.Unlock()
	if sz != [2]int{shareW, shareH} {
		t.Errorf("first decoded frame is %v, want %dx%d", sz, shareW, shareH)
	}
	if got := cv.total(); got != 0 {
		t.Errorf("C (not watching) received %d frames", got)
	}
	_, recv, fps := b.c.VideoStats()
	if recv <= 0 || fps <= 0 {
		t.Errorf("VideoStats at B: recv=%.1f fps=%.1f", recv, fps)
	}
	send, _, _ := a.c.VideoStats()
	if send <= 0 {
		t.Errorf("VideoStats at A: send=%.1f", send)
	}
	// Audio still flows alongside video.
	eventually(t, 10*time.Second, "audio A->B", func() bool { return b.sink.count(a.fp) >= 10 })
}

func TestLateWatcherGetsKeyframe(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	bv := newRecVideo()
	b.c.SetVideoSink(bv)
	a.join(t)
	b.join(t)

	if err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "B sees A sharing", func() bool { return b.sharing(a.fp) })
	time.Sleep(3 * time.Second)
	start := time.Now()
	if err := b.c.Watch(a.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "first frame", func() bool { return bv.n(a.fp) >= 1 })
	bv.mu.Lock()
	lat := bv.first[a.fp].Sub(start)
	bv.mu.Unlock()
	if lat > time.Second {
		t.Errorf("first frame took %v after watch, want < 1s", lat)
	}
}

func TestStopShareEndsWatch(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	bv := newRecVideo()
	b.c.SetVideoSink(bv)
	a.join(t)
	b.join(t)

	if err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "B sees A sharing", func() bool { return b.sharing(a.fp) })
	if err := b.c.Watch(a.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "video", func() bool { return bv.n(a.fp) >= 5 })

	if err := a.c.StopShare(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "A not sharing", func() bool {
		st, ok := b.lastState(voiceCh)
		if !ok {
			return false
		}
		for _, m := range st.Members {
			if m.Fingerprint == a.fp {
				return !m.Sharing
			}
		}
		return false
	})
	time.Sleep(500 * time.Millisecond)
	before := bv.n(a.fp)
	time.Sleep(700 * time.Millisecond)
	if after := bv.n(a.fp); after != before {
		t.Errorf("video kept arriving after StopShare (%d -> %d)", before, after)
	}

	// Sharing again works on the same call and the viewer can rejoin the stream.
	if err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "B sees A sharing again", func() bool { return b.sharing(a.fp) })
	if err := b.c.Watch(a.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "video resumes", func() bool { return bv.n(a.fp) >= before+5 })
	if errs := b.errStrings(); len(errs) > 0 {
		t.Errorf("B saw error events: %v", errs)
	}
}

// A refused Watch is reported to its caller, leaves the previous stream (and
// its labels) in force, and an immediately following Watch of a real sharer
// ends up on that sharer with correct labels.
func TestWatchRefusedThenSwitch(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	c := newVoiceClient(t, url, "carol", 0xC3)
	cv := newRecVideo()
	c.c.SetVideoSink(cv)
	a.join(t)
	b.join(t)
	c.join(t)
	sizeA, sizeB := [2]int{320, 240}, [2]int{256, 192}
	if err := a.c.StartShare(&synthSource{}, sizeA[0], sizeA[1], shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	if err := b.c.StartShare(&synthSource{}, sizeB[0], sizeB[1], shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "C sees both sharing", func() bool { return c.sharing(a.fp) && c.sharing(b.fp) })

	if err := c.c.Watch(a.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "frames from A", func() bool { return cv.n(a.fp) >= 3 })

	var se *client.ServerError
	if err := c.c.Watch("nobody-is-sharing-this"); !errors.As(err, &se) || se.Code != proto.CodeNotSharing {
		t.Fatalf("refused Watch returned %v, want a not_sharing ServerError", err)
	}
	base := cv.n(a.fp)
	eventually(t, 10*time.Second, "A frames continue after the refused watch", func() bool { return cv.n(a.fp) >= base+3 })

	if err := c.c.Watch(b.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "frames labelled B", func() bool { return cv.n(b.fp) >= 3 })
	if got := cv.sizesOf(b.fp); len(got) != 1 || got[sizeB] == 0 {
		t.Errorf("frames labelled B have sizes %v, want only %v", got, sizeB)
	}
	if got := cv.sizesOf(a.fp); len(got) != 1 || got[sizeA] == 0 {
		t.Errorf("frames labelled A have sizes %v, want only %v", got, sizeA)
	}
}

func TestWatchSwitchRelabelsAndStops(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	c := newVoiceClient(t, url, "carol", 0xC3)
	d := newVoiceClient(t, url, "dave", 0xD4)
	cv := newRecVideo()
	c.c.SetVideoSink(cv)
	a.join(t)
	b.join(t)
	c.join(t)
	d.join(t)

	sizeA, sizeB := [2]int{320, 240}, [2]int{256, 192}
	if err := a.c.StartShare(&synthSource{}, sizeA[0], sizeA[1], shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	if err := b.c.StartShare(&synthSource{}, sizeB[0], sizeB[1], shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "C sees both sharing", func() bool { return c.sharing(a.fp) && c.sharing(b.fp) })

	if err := c.c.Watch(a.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "frames from A", func() bool { return cv.n(a.fp) >= 5 })

	// Dave flips between the sharers around Carol's switch. Every flip makes
	// the server ask the new source for a keyframe (rate limited to about two
	// per second per sharer), so A keeps emitting extra keyframes while
	// Carol's old packets are in flight: the condition under which A frames
	// used to be decoded and labelled B. It raises the odds of hitting the
	// window; the deterministic check of the boundary logic is the client's
	// srcGate unit tests.
	eventually(t, 10*time.Second, "D sees both sharing", func() bool { return d.sharing(a.fp) && d.sharing(b.fp) })
	stopFlip := make(chan struct{})
	flipDone := make(chan struct{})
	go func() {
		defer close(flipDone)
		for i := 0; ; i++ {
			fp := a.fp
			if i%2 == 1 {
				fp = b.fp
			}
			_ = d.c.Watch(fp)
			select {
			case <-stopFlip:
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}()
	defer func() { close(stopFlip); <-flipDone }()

	start := time.Now()
	if err := c.c.Watch(b.fp); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "frames labelled B", func() bool { return cv.n(b.fp) >= 3 })
	cv.mu.Lock()
	lat := cv.first[b.fp].Sub(start)
	cv.mu.Unlock()
	if lat > 3*time.Second { // loose: this test is about labels, latency is only a sanity bound
		t.Errorf("first B frame %v after switching, want about 1s", lat)
	}
	if got := cv.sizesOf(b.fp); len(got) != 1 || got[sizeB] == 0 {
		t.Errorf("frames labelled B have sizes %v, want only %v", got, sizeB)
	}
	if got := cv.sizesOf(a.fp); len(got) != 1 || got[sizeA] == 0 {
		t.Errorf("frames labelled A have sizes %v, want only %v", got, sizeA)
	}

	if err := c.c.Watch(""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	total := cv.total()
	time.Sleep(900 * time.Millisecond)
	if after := cv.total(); after != total {
		t.Errorf("%d frames delivered after Watch(\"\")", after-total)
	}
}
