package ui

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/audio"
	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

type lockedLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestAudioDeviceEnumerationCauseLoggedWithoutStatusLeak(t *testing.T) {
	g, s := shareGame(t)
	g.voiceCh = make(chan voiceResult, 1)
	secret := `C:\private\audio-device 127.0.0.1:9999`
	list := func() ([]string, []string, error) { return nil, nil, errors.New(secret) }
	_, _, notice, detail := resolveDevices("saved microphone", "", list)
	if detail == nil || notice == "" || strings.Contains(notice, secret) {
		t.Fatalf("fallback notice=%q detail=%v", notice, detail)
	}
	g.listAudioDevices = list
	g.set.InputDevice = "saved microphone"
	g.openAudio = func(string, string) (client.AudioSource, *audio.Meter, client.AudioSink, *audio.Meters, error) {
		return nil, nil, nil, nil, errors.New("test audio opener stopped join")
	}
	var logs bytes.Buffer
	g.fileLog = log.New(&logs, "", 0)
	g.joinCall("voice")
	pumpVoiceUntil(t, g, func() bool { return !g.vc.busy })
	if strings.Contains(s.st.Status, secret) || !strings.Contains(logs.String(), secret) {
		t.Fatalf("status=%q log=%q", s.st.Status, logs.String())
	}
}

func TestMuteFailureBelongsToCurrentRequestAndCall(t *testing.T) {
	g := newTestGame(t)
	g.muteCh, g.muteFailCh = make(chan muteReq, 1), make(chan muteFail, 4)
	s := newSession("one", "ws://one/", "one")
	s.c = &client.Client{}
	t.Cleanup(func() { s.c = nil })
	g.sess[s.key], g.curKey, g.callKey = s, s.key, s.key
	s.st.SetInCall("voice")
	g.toggleMute()
	first := <-g.muteCh
	g.toggleMute()
	second := <-g.muteCh
	g.muteFailCh <- muteFail{c: first.c, gen: first.gen, muted: true, err: errors.New("private path")}
	g.applyMuteFail()
	if g.vc.muted || s.st.Status != "" {
		t.Fatalf("older failure changed newer request: muted=%v status=%q", g.vc.muted, s.st.Status)
	}
	g.muteFailCh <- muteFail{c: second.c, gen: second.gen, muted: false, err: errors.New("private path")}
	g.applyMuteFail()
	if !g.vc.muted || strings.Contains(s.st.Status, "private path") {
		t.Fatalf("failed unmute did not stay private: muted=%v status=%q", g.vc.muted, s.st.Status)
	}
	g.callKey = ""
	s.st.Status = ""
	g.muteFailCh <- muteFail{c: second.c, gen: second.gen, muted: false, err: errors.New("old call")}
	g.applyMuteFail()
	if s.st.Status != "" {
		t.Fatalf("old call failure changed status: %q", s.st.Status)
	}
	g.callKey = s.key // leave and rejoin on the same client
	g.vc.start = time.Now()
	g.vc.muted = false
	g.muteFailCh <- muteFail{c: second.c, gen: second.gen, start: second.start, muted: false, err: errors.New("old call")}
	g.applyMuteFail()
	if g.vc.muted || s.st.Status != "" {
		t.Fatalf("prior call failure changed replacement call: muted=%v status=%q", g.vc.muted, s.st.Status)
	}
}

func TestNewestMuteFailureSurvivesFullQueue(t *testing.T) {
	g := newTestGame(t)
	s := newSession("one", "ws://one/", "one")
	s.c = &client.Client{}
	t.Cleanup(func() { s.c = nil })
	g.sess[s.key], g.curKey, g.callKey = s, s.key, s.key
	s.st.SetInCall("voice")
	g.vc.start = time.Now()
	g.muteSeq = 5
	g.muteFailCh = make(chan muteFail, 4)
	for gen := uint64(1); gen <= 4; gen++ {
		g.muteFailCh <- muteFail{c: s.c, gen: gen, start: g.vc.start, muted: true, err: errors.New("older failure")}
	}
	var logs bytes.Buffer
	g.fileLog = log.New(&logs, "", 0)
	g.postMuteFail(muteFail{c: s.c, gen: 5, start: g.vc.start, muted: false, err: errors.New("latest private failure")})
	for range 5 {
		g.applyMuteFail()
	}
	if !g.vc.muted || s.st.Status != "microphone stays muted; try again" {
		t.Fatalf("latest failed unmute was lost: muted=%v status=%q", g.vc.muted, s.st.Status)
	}
	if !strings.Contains(logs.String(), "latest private failure") || strings.Contains(s.st.Status, "latest private failure") {
		t.Fatalf("raw cause missing from log or leaked in status: status=%q log=%q", s.st.Status, logs.String())
	}
}

func TestFriendFailureVisibleOnAffectedChatSession(t *testing.T) {
	g := friendGame(t)
	var logs lockedLogBuffer
	g.fileLog = log.New(&logs, "", 0)
	_, s, id := grantedFriend(t, g, "")
	s.st.Status = ""
	g.friendFailed(id, "cannot bind %s", "127.0.0.1:secret")
	if s.st.Status == "" || strings.Contains(s.st.Status, "127.0.0.1:secret") {
		t.Fatalf("affected session status = %q", s.st.Status)
	}
	if !strings.Contains(logs.String(), "127.0.0.1:secret") || g.friendBackoff[id].IsZero() {
		t.Fatalf("missing detailed log or backoff: %q", logs.String())
	}
}

func TestMediaAndVoiceFailuresHideRawDetails(t *testing.T) {
	secret := `C:\private\device 127.0.0.1:9999`
	if got := cameraNotice(errors.New(secret)); strings.Contains(got, secret) {
		t.Fatalf("camera notice leaked details: %q", got)
	}
	if got := state.VoiceJoinNotice(errors.New(secret)); strings.Contains(got, secret) {
		t.Fatalf("voice notice leaked details: %q", got)
	}
	g, s := shareGame(t)
	g.shareOps = (&recorder{}).ops(twoMonitors(), true)
	g.shareOps.start = func(*client.Client, client.FrameSource, int, int, int, int) error { return errors.New(secret) }
	var logs bytes.Buffer
	g.fileLog = log.New(&logs, "", 0)
	g.startShare(twoMonitors().mons[0])
	settle(t, g)
	if strings.Contains(s.st.Status, secret) || !strings.Contains(logs.String(), secret) || !g.inCall() {
		t.Fatalf("share failure: status=%q log=%q call=%v", s.st.Status, logs.String(), g.inCall())
	}
	g.applyVoice(voiceResult{key: s.key, s: s, c: s.c, leave: true, err: errors.New(secret)})
	if strings.Contains(s.st.Status, secret) {
		t.Fatalf("voice leave notice leaked details: %q", s.st.Status)
	}
}

func TestChannelPaneWheelAndVisibleHits(t *testing.T) {
	g := mainGame(t)
	s := g.cur()
	channels := make([]proto.ChannelInfo, 40)
	for i := range channels {
		channels[i] = proto.ChannelInfo{Name: fmt.Sprintf("text-%02d", i)}
	}
	s.st.SetConnected(channels, "fp")
	gm := g.geom()
	x, y := gm.chanR.x0+pad, gm.paneY0+gm.lh/2
	if !g.wheelChannels(s, gm, x, y, -2) || s.ms.channelScroll != 6 {
		t.Fatalf("sidebar wheel: offset=%d", s.ms.channelScroll)
	}
	g.buildHits()
	if h, ok := g.hits.At(x, y); !ok || h.Kind != state.HitChannel || h.ID != 6 {
		t.Fatalf("first visible hit = %+v %v", h, ok)
	}
	before := s.ms.channelScroll
	if g.wheelChannels(s, gm, gm.chatR.x0+pad, y, -1) || s.ms.channelScroll != before {
		t.Fatal("sidebar consumed chat wheel")
	}
	g.wheelChannels(s, gm, x, y, -1e6)
	if s.ms.channelScroll != state.ClampScroll(len(s.st.Rows()), g.channelVisible(gm), 1<<30) {
		t.Fatalf("sidebar did not clamp: %d", s.ms.channelScroll)
	}
	s.st.Active = 0
	g.afterChannelChange()
	if s.ms.channelScroll != 0 {
		t.Fatalf("keyboard selection hidden: scroll=%d", s.ms.channelScroll)
	}
}

func TestScrolledLiveTargetMatchesVisibleOccupant(t *testing.T) {
	g := mainGame(t)
	s := g.cur()
	channels := make([]proto.ChannelInfo, 0, 41)
	for i := 0; i < 40; i++ {
		channels = append(channels, proto.ChannelInfo{Name: fmt.Sprintf("text-%02d", i)})
	}
	channels = append(channels, proto.ChannelInfo{Name: "lounge", Type: "voice"})
	s.st.SetConnected(channels, "fp")
	s.st.Voice["lounge"] = []proto.VoiceMember{{Fingerprint: "presenter", Name: "presenter", Media: []proto.MediaKind{proto.MediaScreen}}}
	gm := g.geom()
	rows := s.st.Rows()
	visible := g.channelVisible(gm)
	s.ms.channelScroll = len(rows) - visible
	g.buildHits()
	rowY := gm.paneY0 + float64(visible-1)*gm.lh
	y := rowY + gm.lh/2
	live := g.liveRect(gm, rowY, false)
	h, ok := g.hits.At((live.x0+live.x1)/2, y)
	if !ok || h.Kind != state.HitLive || h.Key != "presenter" {
		t.Fatalf("scrolled LIVE hit = %+v %v", h, ok)
	}
}
