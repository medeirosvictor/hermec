package integration

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
)

type mediaKey struct {
	kind proto.MediaKind
	fp   string
}

type recMedia struct {
	mu    sync.Mutex
	count map[mediaKey]int
	size  map[mediaKey][2]int
}

func newRecMedia() *recMedia {
	return &recMedia{count: make(map[mediaKey]int), size: make(map[mediaKey][2]int)}
}

func (r *recMedia) WriteMediaFrame(frame client.MediaFrame) {
	if frame.Frame == nil {
		return
	}
	k := mediaKey{kind: frame.Kind, fp: frame.Fingerprint}
	r.mu.Lock()
	r.count[k]++
	r.size[k] = [2]int{frame.Frame.W, frame.Frame.H}
	r.mu.Unlock()
}

func (r *recMedia) n(kind proto.MediaKind, fp string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count[mediaKey{kind: kind, fp: fp}]
}

func (r *recMedia) frameSize(kind proto.MediaKind, fp string) [2]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size[mediaKey{kind: kind, fp: fp}]
}

func (v *voiceClient) publishing(fp string, kinds ...proto.MediaKind) bool {
	st, ok := v.lastState(voiceCh)
	if !ok {
		return false
	}
	for _, member := range st.Members {
		if member.Fingerprint != fp {
			continue
		}
		for _, kind := range kinds {
			if !slices.Contains(member.Media, kind) {
				return false
			}
		}
		return true
	}
	return false
}

func TestE2ETwoCamerasAndScreenFlowSimultaneously(t *testing.T) {
	url := startVoiceServer(t)
	alice := newVoiceClient(t, url, "alice", 0xA1)
	bob := newVoiceClient(t, url, "bob", 0xB2)
	viewer := newVoiceClient(t, url, "viewer", 0xC3)
	received := newRecMedia()
	viewer.c.SetMediaSink(received)
	alice.join(t)
	bob.join(t)
	viewer.join(t)

	if err := alice.c.StartCamera(&synthSource{}); err != nil {
		t.Fatalf("alice camera: %v", err)
	}
	if err := bob.c.StartCamera(&synthSource{}); err != nil {
		t.Fatalf("bob camera: %v", err)
	}
	if err := alice.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatalf("alice screen: %v", err)
	}
	eventually(t, 10*time.Second, "camera and screen publications", func() bool {
		return viewer.publishing(alice.fp, proto.MediaCamera, proto.MediaScreen) &&
			viewer.publishing(bob.fp, proto.MediaCamera)
	})
	if err := viewer.c.SubscribeScreen(alice.fp); err != nil {
		t.Fatalf("select alice screen: %v", err)
	}

	eventually(t, 20*time.Second, "two cameras and selected screen", func() bool {
		return received.n(proto.MediaCamera, alice.fp) >= 3 &&
			received.n(proto.MediaCamera, bob.fp) >= 3 &&
			received.n(proto.MediaScreen, alice.fp) >= 3
	})
	if got := received.frameSize(proto.MediaCamera, alice.fp); got != [2]int{640, 360} {
		t.Errorf("alice camera size = %v, want 640x360", got)
	}
	if got := received.frameSize(proto.MediaCamera, bob.fp); got != [2]int{640, 360} {
		t.Errorf("bob camera size = %v, want 640x360", got)
	}
	if got := received.frameSize(proto.MediaScreen, alice.fp); got != [2]int{shareW, shareH} {
		t.Errorf("alice screen size = %v, want %dx%d", got, shareW, shareH)
	}
}

func TestE2EMediaLanesSurviveTakeover(t *testing.T) {
	hs := startHostSet(t, 2)
	alice := followFailover(t, hs.urls[0], "alice", 0xA1)
	viewer := followFailover(t, hs.urls[0], "viewer", 0xB2)
	eventually(t, 10*time.Second, "warm sessions", func() bool {
		return alice.warmReady() && viewer.warmReady()
	})
	alice.joinVoice(t)
	viewer.joinVoice(t)
	eventually(t, 15*time.Second, "warm media peers", func() bool {
		return alice.warmReady() && viewer.warmReady()
	})

	received := newRecMedia()
	viewer.mu.Lock()
	viewer.cur.SetMediaSink(received)
	viewer.warm.SetMediaSink(received)
	viewerClient := viewer.cur
	viewer.mu.Unlock()
	alice.mu.Lock()
	aliceClient := alice.cur
	alice.mu.Unlock()
	if err := aliceClient.StartCamera(&synthSource{}); err != nil {
		t.Fatalf("camera before takeover: %v", err)
	}
	if err := aliceClient.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatalf("screen before takeover: %v", err)
	}
	eventually(t, 10*time.Second, "camera and screen publications before takeover", func() bool {
		return viewer.publishing(alice.fp, proto.MediaCamera, proto.MediaScreen)
	})
	if err := viewerClient.SubscribeScreen(alice.fp); err != nil {
		t.Fatalf("select screen before takeover: %v", err)
	}
	eventually(t, 20*time.Second, "media before takeover", func() bool {
		return received.n(proto.MediaCamera, alice.fp) >= 3 && received.n(proto.MediaScreen, alice.fp) >= 3
	})
	cameraBefore := received.n(proto.MediaCamera, alice.fp)
	screenBefore := received.n(proto.MediaScreen, alice.fp)

	hs.kill(0)
	eventually(t, 15*time.Second, "clients promoted without redial", func() bool {
		return alice.fullOn(hs.fps[1]) && viewer.fullOn(hs.fps[1])
	})
	if !viewer.publishing(alice.fp, proto.MediaCamera, proto.MediaScreen) {
		t.Fatal("promoted host did not announce alice's camera and screen")
	}
	bothWays(t, alice, viewer, 10)
	eventually(t, 20*time.Second, "camera and selected screen after takeover", func() bool {
		return received.n(proto.MediaCamera, alice.fp) >= cameraBefore+3 &&
			received.n(proto.MediaScreen, alice.fp) >= screenBefore+3
	})
	for _, f := range []*follower{alice, viewer} {
		if _, takeovers, redials, _ := f.counts(); takeovers != 1 || redials != 0 {
			t.Errorf("%s: takeovers = %d, redials = %d; want 1 and 0", f.name, takeovers, redials)
		}
	}
}
