package integration

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/server"
)

// audioFlowsBothWays checks that, over the next d, at least minFrames of x's
// audio reach y and of y's reach x (20 ms frames: 50 per second).
func audioFlowsBothWays(t *testing.T, x, y *voiceClient, d time.Duration, minFrames int) {
	t.Helper()
	xy0, yx0 := y.sink.count(x.fp), x.sink.count(y.fp)
	time.Sleep(d)
	if got := y.sink.count(x.fp) - xy0; got < minFrames {
		t.Errorf("audio %s->%s: %d frames in %v, want >= %d", x.fp[:6], y.fp[:6], got, d, minFrames)
	}
	if got := x.sink.count(y.fp) - yx0; got < minFrames {
		t.Errorf("audio %s->%s: %d frames in %v, want >= %d", y.fp[:6], x.fp[:6], got, d, minFrames)
	}
}

func (v *voiceClient) hasErr(substr string) bool {
	for _, e := range v.errStrings() {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// A v0.5 client sharing on a host that cannot carry it (no VP8) must lose the share, not the call.
func TestShareOnUnsupportedHostKeepsCallUp(t *testing.T) {
	cases := map[string]func(*server.Config){
		"v0.5 host without VP8": func(c *server.Config) { c.NoVP8 = true; c.Logf = t.Logf },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			url := startVoiceServerWith(t, mod)
			a := newVoiceClient(t, url, "alice", 0xA1)
			b := newVoiceClient(t, url, "bob", 0xB2)
			a.join(t)
			b.join(t)
			audioFlowsBothWays(t, a, b, time.Second, 20)

			if err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
				t.Fatalf("StartShare: %v", err)
			}
			eventually(t, 15*time.Second, "the unsupported-share error", func() bool {
				return a.hasErr("does not support screen share")
			})
			eventually(t, 20*time.Second, "audio to resume both ways", func() bool {
				xy, yx := b.sink.count(a.fp), a.sink.count(b.fp)
				time.Sleep(400 * time.Millisecond)
				return b.sink.count(a.fp)-xy >= 15 && a.sink.count(b.fp)-yx >= 15
			})
			if got := a.c.VoiceChannel(); got != voiceCh {
				t.Fatalf("sharer left the call (VoiceChannel=%q); errors: %v", got, a.errStrings())
			}
			audioFlowsBothWays(t, a, b, 2*time.Second, 60)
			for _, e := range a.errStrings() {
				if strings.Contains(e, "voice call ended") || strings.Contains(e, "keeps failing") {
					t.Errorf("call died: %s", e)
				}
			}
			// The failed share is gone: sharing again is not "already sharing".
			err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps)
			if errors.Is(err, client.ErrAlreadySharing) {
				t.Error("the failed share is still registered")
			}
			_ = a.c.StopShare()
		})
	}
}

// A pre-v0.5 client (Opus only) sharing a call with a sharer and a viewer
// keeps two-way audio with both.
func TestAudioOnlyClientInCallWithShare(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	old := newVoiceClient(t, url, "oldbob", 0xB2)
	c := newVoiceClient(t, url, "carol", 0xC3)
	old.c.SetAudioOnly(true)
	cv := newRecVideo()
	c.c.SetVideoSink(cv)
	a.join(t)
	old.join(t)
	c.join(t)

	if err := a.c.StartShare(&synthSource{}, shareW, shareH, shareFPS, shareKbps); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "C sees A sharing", func() bool { return c.sharing(a.fp) })
	warmUp(t, c, cv, a)
	watchOK(t, c, a.fp)
	eventually(t, 15*time.Second, "C receives video", func() bool { return cv.n(a.fp) >= 10 })

	audioFlowsBothWays(t, a, old, 2*time.Second, 60)
	audioFlowsBothWays(t, c, old, 2*time.Second, 60)
	audioFlowsBothWays(t, a, c, 2*time.Second, 60)
	if got := old.c.VoiceChannel(); got != voiceCh {
		t.Fatalf("old client left the call; errors: %v", old.errStrings())
	}
	if errs := old.errStrings(); len(errs) > 0 {
		t.Errorf("old client saw errors: %v", errs)
	}
}
