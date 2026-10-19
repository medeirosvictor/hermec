package integration

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
}

func newRecVideo() *recVideo {
	return &recVideo{count: map[string]int{}, first: map[string]time.Time{}, size: map[string][2]int{}}
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
