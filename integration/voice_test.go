package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

const voiceCh = "lobby"

func startVoiceServer(t *testing.T) string {
	t.Helper()
	s := server.New(server.Config{
		Addr:             "127.0.0.1:0",
		Roles:            roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:         []string{"general"},
		VoiceChannels:    []string{voiceCh},
		AllowLoopbackICE: true,
	})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return "ws://" + s.Addr() + "/"
}

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

// recSink records, per sender fingerprint, how many frames arrived and which
// markers they carried.
type recSink struct {
	mu      sync.Mutex
	byFP    map[string]int
	markers map[byte]int
}

func newRecSink() *recSink { return &recSink{byFP: map[string]int{}, markers: map[byte]int{}} }

func (r *recSink) WriteOpusFrame(fp string, frame []byte) {
	r.mu.Lock()
	r.byFP[fp]++
	if len(frame) > 0 {
		r.markers[frame[0]]++
	}
	r.mu.Unlock()
}

func (r *recSink) count(fp string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byFP[fp]
}

func (r *recSink) marker(m byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.markers[m]
}

func (r *recSink) senders() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byFP)
}

func (r *recSink) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.byFP {
		n += v
	}
	return n
}

type voiceClient struct {
	c    *client.Client
	fp   string
	src  *markerSource
	sink *recSink

	mu     sync.Mutex
	states []proto.VoiceState
	errs   []error
	chats  []string
}

func (v *voiceClient) lastState(ch string) (proto.VoiceState, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := len(v.states) - 1; i >= 0; i-- {
		if v.states[i].Channel == ch {
			return v.states[i], true
		}
	}
	return proto.VoiceState{}, false
}

func (v *voiceClient) errStrings() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []string
	for _, e := range v.errs {
		out = append(out, e.Error())
	}
	return out
}

// newVoiceClient dials url (optionally via a proxy URL) and drains events.
func newVoiceClient(t *testing.T, url, name string, marker byte) *voiceClient {
	t.Helper()
	id := newID(t)
	c, err := client.Dial(testCtx(t), url, id, name, "")
	if err != nil {
		t.Fatalf("Dial %s: %v", name, err)
	}
	c.SetLoopbackICE(true)
	v := &voiceClient{c: c, fp: c.Fingerprint(), src: &markerSource{marker: marker}, sink: newRecSink()}
	t.Cleanup(func() { c.Close() })
	go func() {
		for ev := range c.Events() {
			v.mu.Lock()
			if ev.Voice != nil {
				v.states = append(v.states, *ev.Voice)
			}
			if ev.Chat != nil {
				v.chats = append(v.chats, ev.Chat.Text)
			}
			if ev.Err != nil {
				v.errs = append(v.errs, ev.Err)
			}
			v.mu.Unlock()
		}
	}()
	return v
}

func (v *voiceClient) join(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := v.c.JoinVoice(ctx, voiceCh, v.src, v.sink); err != nil {
		t.Fatalf("JoinVoice(%s): %v", v.fp[:8], err)
	}
}

func eventually(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", d, what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestVoiceTwoClientsMedia(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	a.join(t)
	b.join(t)

	eventually(t, 15*time.Second, "A's frames at B", func() bool { return b.sink.count(a.fp) >= 10 })
	eventually(t, 15*time.Second, "B's frames at A", func() bool { return a.sink.count(b.fp) >= 10 })

	// Frames are keyed by the sender's fingerprint and carry the sender's marker.
	if got := b.sink.marker(0xA1); got < 10 {
		t.Errorf("B saw %d frames with A's marker", got)
	}
	if got := b.sink.marker(0xB2); got != 0 {
		t.Errorf("B received its own marker %d times", got)
	}
	// A never hears itself.
	if got := a.sink.count(a.fp); got != 0 {
		t.Errorf("A's sink got %d frames keyed to A", got)
	}
	if got := a.sink.marker(0xA1); got != 0 {
		t.Errorf("A's sink got its own marker %d times", got)
	}
	if n := b.sink.senders(); n != 1 {
		t.Errorf("B's sink keyed by %d senders, want 1", n)
	}
	if a.c.VoiceChannel() != voiceCh {
		t.Errorf("VoiceChannel = %q", a.c.VoiceChannel())
	}
}

func TestVoiceMute(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	a.join(t)
	b.join(t)
	eventually(t, 15*time.Second, "A's frames at B", func() bool { return b.sink.count(a.fp) >= 10 })

	if err := a.c.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	// Bound: in-flight frames drain within 500ms, then silence.
	time.Sleep(500 * time.Millisecond)
	before := b.sink.count(a.fp)
	readsBefore := a.src.seq.Load()
	time.Sleep(700 * time.Millisecond)
	if after := b.sink.count(a.fp); after != before {
		t.Fatalf("muted A still delivered %d frames", after-before)
	}
	// Muting pauses source reads, not just transmission.
	if reads := a.src.seq.Load(); reads != readsBefore {
		t.Fatalf("source still read %d frames while muted", reads-readsBefore)
	}
	// The mute flag is visible to others.
	eventually(t, 5*time.Second, "B sees A muted", func() bool {
		st, ok := b.lastState(voiceCh)
		if !ok {
			return false
		}
		for _, m := range st.Members {
			if m.Fingerprint == a.fp && m.Muted {
				return true
			}
		}
		return false
	})

	if err := a.c.SetMuted(false); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "frames resume after unmute", func() bool { return b.sink.count(a.fp) >= before+10 })
}

func TestVoiceForceClosedPeerPruned(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	c := newVoiceClient(t, url, "carol", 0xC3)
	a.join(t)
	b.join(t)
	c.join(t)
	eventually(t, 20*time.Second, "full mesh audio", func() bool {
		return a.sink.count(b.fp) >= 5 && a.sink.count(c.fp) >= 5 &&
			b.sink.count(a.fp) >= 5 && b.sink.count(c.fp) >= 5 &&
			c.sink.count(a.fp) >= 5 && c.sink.count(b.fp) >= 5
	})

	// Force-close C: socket dropped, no voice_leave.
	_ = c.c.Close()
	eventually(t, 10*time.Second, "C pruned from voice_state", func() bool {
		st, ok := a.lastState(voiceCh)
		return ok && len(st.Members) == 2
	})

	// A and B keep flowing after C's removal (renegotiation happened).
	baseAB, baseBA := b.sink.count(a.fp), a.sink.count(b.fp)
	eventually(t, 15*time.Second, "A<->B flow after prune", func() bool {
		return b.sink.count(a.fp) >= baseAB+15 && a.sink.count(b.fp) >= baseBA+15
	})
	if errs := a.errStrings(); len(errs) > 0 {
		t.Errorf("A saw error events: %v", errs)
	}
}

func TestVoiceGoroutineHygiene(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)

	settle := func() int {
		prev := -1
		for i := 0; i < 40; i++ {
			time.Sleep(250 * time.Millisecond)
			n := runtime.NumGoroutine()
			if n == prev {
				return n
			}
			prev = n
		}
		return prev
	}
	baseline := settle()

	a.join(t)
	b.join(t)
	eventually(t, 15*time.Second, "media flowing", func() bool {
		return b.sink.count(a.fp) >= 10 && a.sink.count(b.fp) >= 10
	})
	during := runtime.NumGoroutine()

	ctx := testCtx(t)
	if err := a.c.LeaveVoice(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.c.LeaveVoice(ctx); err != nil {
		t.Fatal(err)
	}
	// Last leave destroys the server session; wait for it to be reflected.
	eventually(t, 10*time.Second, "voice channel empty", func() bool {
		st, ok := b.lastState(voiceCh)
		return ok && len(st.Members) == 0
	})
	var after int
	deadline := time.Now().Add(20 * time.Second)
	for {
		after = settle()
		if after <= baseline+3 || time.Now().After(deadline) {
			break
		}
	}
	t.Logf("goroutines: baseline=%d during=%d after=%d", baseline, during, after)
	if after > baseline+3 {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		t.Fatalf("goroutine leak: baseline=%d after=%d\n%s", baseline, after, buf)
	}
}

func TestVoiceJoinTimeoutKeepsChatUsable(t *testing.T) {
	url := startVoiceServer(t)
	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, url, "bob", 0xB2)
	if err := a.c.Join(testCtx(t), "general"); err != nil {
		t.Fatal(err)
	}
	if err := b.c.Join(testCtx(t), "general"); err != nil {
		t.Fatal(err)
	}

	// Far too short for ICE + DTLS: the negotiation cannot finish.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := a.c.JoinVoice(ctx, voiceCh, a.src, a.sink)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("JoinVoice err = %v, want DeadlineExceeded", err)
	}
	if a.c.VoiceChannel() != "" {
		t.Fatal("call not torn down")
	}
	// Server drops A from the call (client sent voice_leave).
	eventually(t, 5*time.Second, "A absent from voice_state", func() bool {
		st, ok := b.lastState(voiceCh)
		if !ok {
			return false
		}
		for _, m := range st.Members {
			if m.Fingerprint == a.fp {
				return false
			}
		}
		return true
	})
	// Chat still works both ways.
	if err := a.c.SendChat(testCtx(t), "general", "still here"); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "chat at B", func() bool { return b.hasChat("still here") })
	// And a proper join afterwards succeeds on the same client.
	a.join(t)
	b.join(t)
	eventually(t, 15*time.Second, "media after retry", func() bool { return b.sink.count(a.fp) >= 10 })
}

func (v *voiceClient) hasChat(text string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, c := range v.chats {
		if c == text {
			return true
		}
	}
	return false
}

// ---- proxy for forcing negotiation failures ----

// wsProxy forwards a client's websocket to upstream, letting a test mangle,
// hold or inject messages in either direction.
type wsProxy struct {
	t        *testing.T
	upstream string
	srv      *httptest.Server

	// hooks; return (replacement, forward). Called from the pump goroutines.
	fromClient func(env proto.Envelope, raw []byte) ([]byte, bool)
	fromServer func(env proto.Envelope, raw []byte, inject func([]byte)) ([]byte, bool)
}

func newWSProxy(t *testing.T, upstream string) *wsProxy {
	p := &wsProxy{t: t, upstream: upstream}
	up := websocket.Upgrader{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sws, _, err := websocket.DefaultDialer.Dial(p.upstream, nil)
		if err != nil {
			cws.Close()
			return
		}
		var cmu, smu sync.Mutex
		toClient := func(b []byte) { cmu.Lock(); _ = cws.WriteMessage(websocket.TextMessage, b); cmu.Unlock() }
		toServer := func(b []byte) { smu.Lock(); _ = sws.WriteMessage(websocket.TextMessage, b); smu.Unlock() }
		done := make(chan struct{}, 2)
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				_, raw, err := cws.ReadMessage()
				if err != nil {
					return
				}
				if env, err := proto.Decode(raw); err == nil && p.fromClient != nil {
					out, fwd := p.fromClient(env, raw)
					if !fwd {
						continue
					}
					raw = out
				}
				toServer(raw)
			}
		}()
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				_, raw, err := sws.ReadMessage()
				if err != nil {
					return
				}
				if env, err := proto.Decode(raw); err == nil && p.fromServer != nil {
					out, fwd := p.fromServer(env, raw, toClient)
					if !fwd {
						continue
					}
					raw = out
				}
				toClient(raw)
			}
		}()
		<-done
		cws.Close()
		sws.Close()
		<-done
	}))
	t.Cleanup(func() { p.srv.CloseClientConnections(); p.srv.Close() })
	return p
}

func (p *wsProxy) url() string { return "ws" + strings.TrimPrefix(p.srv.URL, "http") }

// TestVoicePostJoinFailureIsObservable: after JoinVoice returned, a call that
// can no longer be negotiated must end visibly (Err event, VoiceChannel()=="")
// and leave the client usable. The proxy swallows the server's answers and
// injects media-reset errors until the consecutive-reset cap is exceeded.
func TestVoicePostJoinFailureIsObservable(t *testing.T) {
	url := startVoiceServer(t)
	p := newWSProxy(t, url)
	var mu sync.Mutex
	var inject func([]byte)
	var dropAnswers bool
	p.fromServer = func(env proto.Envelope, raw []byte, inj func([]byte)) ([]byte, bool) {
		mu.Lock()
		defer mu.Unlock()
		inject = inj
		if dropAnswers && env.Type == proto.TypeRTCAnswer {
			return nil, false
		}
		return raw, true
	}

	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, p.url(), "bob", 0xB2)
	if err := a.c.Join(testCtx(t), "general"); err != nil {
		t.Fatal(err)
	}
	if err := b.c.Join(testCtx(t), "general"); err != nil {
		t.Fatal(err)
	}
	a.join(t)
	b.join(t)
	eventually(t, 15*time.Second, "media", func() bool { return b.sink.count(a.fp) >= 5 })

	mu.Lock()
	dropAnswers = true
	send := inject
	mu.Unlock()
	fake, _ := proto.Encode(proto.TypeError, proto.ErrorMsg{Code: "bad_request", Message: "media reset; send a new rtc_offer"})
	for i := 0; i < 12; i++ {
		send(fake)
	}

	eventually(t, 15*time.Second, "call-ended event", func() bool {
		for _, e := range b.errStrings() {
			if strings.Contains(e, "voice call ended") {
				return true
			}
		}
		return false
	})
	if got := b.c.VoiceChannel(); got != "" {
		t.Fatalf("VoiceChannel after call death = %q", got)
	}
	// Still usable: chat works and a fresh join succeeds.
	if err := b.c.SendChat(testCtx(t), "general", "alive"); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "chat at A", func() bool { return a.hasChat("alive") })
	mu.Lock()
	dropAnswers = false
	mu.Unlock()
	before := b.sink.count(a.fp)
	b.join(t)
	eventually(t, 20*time.Second, "media after rejoin", func() bool { return b.sink.count(a.fp) >= before+10 })
}

// TestVoiceMediaResetRecovery corrupts B's first answer to a server offer.
// The server resets B's media and says "send a new rtc_offer"; the client
// must discard its PeerConnection, re-offer, and audio must flow again.
func TestVoiceMediaResetRecovery(t *testing.T) {
	url := startVoiceServer(t)
	var corrupted, resetErrs, offersFromB atomic.Int32
	p := newWSProxy(t, url)
	p.fromClient = func(env proto.Envelope, raw []byte) ([]byte, bool) {
		switch env.Type {
		case proto.TypeRTCOffer:
			offersFromB.Add(1)
		case proto.TypeRTCAnswer:
			if corrupted.CompareAndSwap(0, 1) {
				out, _ := proto.Encode(proto.TypeRTCAnswer, proto.RTCAnswer{SDP: "v=0\r\ngarbage"})
				return out, true
			}
		}
		return raw, true
	}
	p.fromServer = func(env proto.Envelope, raw []byte, _ func([]byte)) ([]byte, bool) {
		if env.Type == proto.TypeError {
			var em proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &em)
			if strings.Contains(em.Message, "send a new rtc_offer") {
				resetErrs.Add(1)
			}
		}
		return raw, true
	}

	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, p.url(), "bob", 0xB2)
	a.join(t)
	b.join(t)

	eventually(t, 25*time.Second, "A's frames at B after reset", func() bool { return b.sink.count(a.fp) >= 10 })
	eventually(t, 25*time.Second, "B's frames at A after reset", func() bool { return a.sink.count(b.fp) >= 10 })
	if corrupted.Load() != 1 {
		t.Fatalf("answer was never corrupted (%d)", corrupted.Load())
	}
	if resetErrs.Load() < 1 {
		t.Errorf("server never sent a media-reset error")
	}
	if offersFromB.Load() < 2 {
		t.Errorf("client sent %d offers, want >= 2 (re-offer after reset)", offersFromB.Load())
	}
	for _, e := range b.errStrings() {
		if strings.Contains(e, "rtc_offer") {
			t.Errorf("reset error leaked to the event stream: %s", e)
		}
	}
}

// TestVoiceGlareAnswerFirst forces glare deterministically with the proxy.
// The proxy holds the server's first renegotiation offer (S) to B, so the
// server considers it outstanding. A fake media-reset error then makes B
// re-offer; the server rejects that with "offer pending - answer first".
// Only then is S released: B must answer it first (abandoning its own pending
// offer), re-offer, and audio must flow.
//
// Honesty note: this scenario is contrived. The client never produces glare
// organically (it only offers initially or after a reset, when the server has
// dropped its media). Here the server offer S belongs to B's FIRST PC, which
// B discarded when it processed the fake reset, so B answers S from a fresh
// PC. The server cannot apply that answer, so the EXPECTED outcome is a
// server-side media reset followed by one clean re-offer from B. What the
// test proves is the client's ordering discipline, asserted from the proxy's
// log: B's second offer is rejected as glare, B answers S before offering
// again, and does not offer twice at once.
func TestVoiceGlareAnswerFirst(t *testing.T) {
	url := startVoiceServer(t)
	p := newWSProxy(t, url)

	var mu sync.Mutex
	var heldRaw []byte
	released := false
	var toClient func([]byte)
	held := make(chan struct{})
	var glareErrs, offersFromB, answersFromB atomic.Int32

	var logMu sync.Mutex
	var bLog []string // B's offers/answers in send order
	p.fromClient = func(env proto.Envelope, raw []byte) ([]byte, bool) {
		switch env.Type {
		case proto.TypeRTCOffer:
			offersFromB.Add(1)
			logMu.Lock()
			bLog = append(bLog, "offer")
			logMu.Unlock()
		case proto.TypeRTCAnswer:
			answersFromB.Add(1)
			logMu.Lock()
			bLog = append(bLog, "answer")
			logMu.Unlock()
		}
		return raw, true
	}
	p.fromServer = func(env proto.Envelope, raw []byte, inject func([]byte)) ([]byte, bool) {
		mu.Lock()
		defer mu.Unlock()
		toClient = inject
		switch env.Type {
		case proto.TypeRTCOffer:
			if heldRaw == nil {
				heldRaw = raw
				close(held)
				return nil, false
			}
		case proto.TypeError:
			var em proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &em)
			if strings.Contains(em.Message, "offer pending") {
				glareErrs.Add(1)
				if heldRaw != nil && !released {
					released = true
					inject(raw)
					inject(heldRaw) // release S right after the rejection
					return nil, false
				}
			}
		}
		return raw, true
	}

	a := newVoiceClient(t, url, "alice", 0xA1)
	b := newVoiceClient(t, p.url(), "bob", 0xB2)
	a.join(t)
	b.join(t)

	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("server never sent a renegotiation offer to B")
	}
	mu.Lock()
	send := toClient
	mu.Unlock()
	fake, _ := proto.Encode(proto.TypeError, proto.ErrorMsg{Code: "bad_request", Message: "media reset; send a new rtc_offer"})
	send(fake)

	eventually(t, 30*time.Second, "A's frames at B after glare", func() bool { return b.sink.count(a.fp) >= 10 })
	eventually(t, 30*time.Second, "B's frames at A after glare", func() bool { return a.sink.count(b.fp) >= 10 })
	if glareErrs.Load() < 1 {
		t.Errorf("server never reported glare")
	}
	if offersFromB.Load() < 2 {
		t.Errorf("B sent %d offers, want >= 2", offersFromB.Load())
	}
	if answersFromB.Load() < 1 {
		t.Errorf("B never answered the released server offer")
	}
	// Ordering: offer(initial), offer(after fake reset; glare-rejected),
	// answer(to released S) strictly BEFORE the next offer.
	logMu.Lock()
	seq := append([]string(nil), bLog...)
	logMu.Unlock()
	if len(seq) < 4 || seq[0] != "offer" || seq[1] != "offer" || seq[2] != "answer" || seq[3] != "offer" {
		t.Errorf("B's offer/answer order = %v, want offer, offer, answer, offer...", seq)
	}
}
