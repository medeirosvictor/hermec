package embeddedprobe

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/netpath"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/video"
)

// ScenarioConfig supplies three isolated routes: primary host, participant A,
// and standby plus participant B. Tests may use disposable providers; the CLI
// opens tsnet profiles before calling this function.
type ScenarioConfig struct {
	Networks    [3]netpath.Network
	PrimaryAddr string
	StandbyAddr string
	Port        int
	StandbyPort int
	LoopbackICE bool
	Timeout     time.Duration
}

type CheckResult struct {
	Passed   bool          `json:"passed"`
	Duration time.Duration `json:"duration"`
	Detail   string        `json:"detail,omitempty"`
}

type ScenarioReport struct {
	StartedAt time.Time              `json:"started_at"`
	ServerID  string                 `json:"server_id"`
	Addresses []string               `json:"addresses"`
	Checks    map[string]CheckResult `json:"checks"`
}

func (r *ScenarioReport) check(name string, started time.Time, err error) {
	result := CheckResult{Passed: err == nil, Duration: time.Since(started)}
	if err != nil {
		result.Detail = err.Error()
	}
	r.Checks[name] = result
}

// RunScenario proves authenticated signaling, bidirectional audio, independent
// camera and screen delivery, warm readiness and in-call takeover.
func RunScenario(parent context.Context, cfg ScenarioConfig) (report ScenarioReport, retErr error) {
	report = ScenarioReport{StartedAt: time.Now(), Checks: map[string]CheckResult{}}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()
	for i, n := range cfg.Networks {
		if n == nil {
			return report, fmt.Errorf("embedded probe: network %d is required", i)
		}
	}
	if cfg.PrimaryAddr == "" {
		cfg.PrimaryAddr = "127.0.0.1"
	}
	if cfg.StandbyAddr == "" {
		cfg.StandbyAddr = cfg.PrimaryAddr
	}
	if cfg.Port == 0 {
		return report, errors.New("embedded probe: primary port is required")
	}
	if cfg.StandbyPort == 0 {
		cfg.StandbyPort = cfg.Port + 1
	}

	standbyKey, err := identity.Generate()
	if err != nil {
		return report, err
	}
	group, founder, err := scenarioChain(standbyKey)
	if err != nil {
		return report, err
	}
	standbyChain, err := chain.FromEntries(group.Entries())
	if err != nil {
		return report, err
	}
	primaryURL := fmt.Sprintf("ws://%s/", net.JoinHostPort(cfg.PrimaryAddr, fmt.Sprint(cfg.Port)))
	standbyURL := fmt.Sprintf("ws://%s/", net.JoinHostPort(cfg.StandbyAddr, fmt.Sprint(cfg.StandbyPort)))
	report.ServerID = group.ID().String()
	report.Addresses = []string{primaryURL, standbyURL}

	primary := server.NewHost(server.HostConfig{Server: server.Config{
		Addr: net.JoinHostPort(cfg.PrimaryAddr, fmt.Sprint(cfg.Port)), Network: cfg.Networks[0],
		HostKey: founder, Chain: group, HostName: "probe-primary", Advertise: []string{primaryURL},
		Discoverable: false, AllowLoopbackICE: cfg.LoopbackICE,
	}})
	if err := primary.Start(); err != nil {
		return report, fmt.Errorf("primary host: %w", err)
	}
	primaryUp := true
	defer func() {
		if primaryUp {
			_ = primary.Shutdown(context.Background())
		}
	}()
	started := time.Now()
	err = waitFor(ctx, func() bool { return primary.Role() == server.RoleActive })
	report.check("primary_active", started, err)
	if err != nil {
		return report, err
	}

	standby := server.NewHost(server.HostConfig{Server: server.Config{
		Addr: net.JoinHostPort(cfg.StandbyAddr, fmt.Sprint(cfg.StandbyPort)), Network: cfg.Networks[2],
		HostKey: standbyKey, Chain: standbyChain, HostName: "probe-standby", Advertise: []string{standbyURL},
		Discoverable: false, AllowLoopbackICE: cfg.LoopbackICE,
	}, Known: func() []string { return []string{primaryURL} }})
	if err := standby.Start(); err != nil {
		return report, fmt.Errorf("standby host: %w", err)
	}
	defer standby.Shutdown(context.Background())
	started = time.Now()
	err = waitFor(ctx, func() bool { return standby.Role() == server.RoleStandby })
	report.check("standby_signaling", started, err)
	if err != nil {
		return report, err
	}

	aID, _ := identity.Generate()
	bID, _ := identity.Generate()
	a, err := client.DialWithNetwork(ctx, primaryURL, aID, "probe-a", "", client.DialOpts{}, cfg.Networks[1])
	if err != nil {
		return report, err
	}
	defer a.Close()
	b, err := client.DialWithNetwork(ctx, primaryURL, bID, "probe-b", "", client.DialOpts{Pin: a.ServerID()}, cfg.Networks[2])
	if err != nil {
		return report, err
	}
	defer b.Close()
	aw, err := client.DialWithNetwork(ctx, standbyURL, aID, "probe-a", "", client.DialOpts{Pin: a.ServerID(), Warm: true}, cfg.Networks[1])
	if err != nil {
		return report, err
	}
	defer aw.Close()
	bw, err := client.DialWithNetwork(ctx, standbyURL, bID, "probe-b", "", client.DialOpts{Pin: a.ServerID(), Warm: true}, cfg.Networks[2])
	if err != nil {
		return report, err
	}
	defer bw.Close()
	if cfg.LoopbackICE {
		a.SetLoopbackICE(true)
		b.SetLoopbackICE(true)
		aw.SetLoopbackICE(true)
		bw.SetLoopbackICE(true)
	}
	started = time.Now()
	mediaCtx, mediaCancel := context.WithTimeout(ctx, 5*time.Second)
	if err = aw.OpenWarmPeer(mediaCtx); err == nil {
		err = bw.OpenWarmPeer(mediaCtx)
	}
	if err == nil {
		err = waitFor(mediaCtx, func() bool { return aw.WarmPeerReady() && bw.WarmPeerReady() })
	}
	mediaCancel()
	if err != nil {
		err = fmt.Errorf("warm media peer not ready: %w", err)
	}
	report.check("standby_media_ready", started, err)
	if err != nil {
		return report, err
	}

	aAudio, bAudio := newProbeAudio(0x11), newProbeAudio(0x22)
	aSink, bSink := newProbeSink(bID), newProbeSink(aID)
	aMedia, bMedia := newProbeMediaSink(), newProbeMediaSink()
	a.SetMediaSink(aMedia)
	b.SetMediaSink(bMedia)
	aw.SetMediaSink(aMedia)
	bw.SetMediaSink(bMedia)
	if err = a.JoinVoice(ctx, "voice", aAudio, aSink); err != nil {
		return report, err
	}
	if err = b.JoinVoice(ctx, "voice", bAudio, bSink); err != nil {
		return report, err
	}
	defer a.LeaveVoice(context.Background())
	defer b.LeaveVoice(context.Background())
	if err = a.StartCamera(&colorFrames{y: 70}); err != nil {
		return report, err
	}
	if err = b.StartCamera(&colorFrames{y: 90}); err != nil {
		return report, err
	}
	if err = a.StartShare(&colorFrames{y: 110}, 64, 64, 10, 100); err != nil {
		return report, err
	}
	if err = b.StartShare(&colorFrames{y: 130}, 64, 64, 10, 100); err != nil {
		return report, err
	}
	if err = a.Watch(b.Fingerprint()); err != nil {
		return report, err
	}
	if err = b.Watch(a.Fingerprint()); err != nil {
		return report, err
	}
	started = time.Now()
	err = waitFor(ctx, func() bool {
		return aSink.seen() && bSink.seen() && aMedia.seen(proto.MediaCamera, b.Fingerprint()) &&
			bMedia.seen(proto.MediaCamera, a.Fingerprint()) && aMedia.seen(proto.MediaScreen, b.Fingerprint()) &&
			bMedia.seen(proto.MediaScreen, a.Fingerprint())
	})
	report.check("bidirectional_audio_camera_screen", started, err)
	if err != nil {
		return report, err
	}

	if err = a.SetMuted(true); err != nil {
		return report, err
	}
	beforeA, beforeB := aMedia.count(), bMedia.count()
	started = time.Now()
	if err = primary.Shutdown(ctx); err != nil {
		return report, err
	}
	primaryUp = false
	err = retry(ctx, func() error { return aw.Takeover(ctx) })
	if err == nil {
		err = waitFor(ctx, func() bool { return !bw.Warm() })
	}
	if err == nil {
		err = aw.AdoptCall(ctx, a)
	}
	if err == nil {
		err = bw.AdoptCall(ctx, b)
	}
	if err == nil {
		err = waitFor(ctx, func() bool { return aMedia.count() > beforeA && bMedia.count() > beforeB })
	}
	if err == nil {
		err = waitMutedEvent(ctx, bw.Events(), aw.Fingerprint())
	}
	report.check("takeover_retains_call_media", started, err)
	if err != nil {
		return report, err
	}
	return report, nil
}

func scenarioChain(standby *identity.Identity) (*chain.Chain, *identity.Identity, error) {
	founder, err := identity.Generate()
	if err != nil {
		return nil, nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	genesis, err := chain.NewEntry(founder, nil, chain.OpGenesis, chain.GenesisBody{Name: "probe", Founder: founder.PublicKey(), Salt: salt}, time.Now())
	if err != nil {
		return nil, nil, err
	}
	c, err := chain.New(genesis)
	if err != nil {
		return nil, nil, err
	}
	config := chain.ConfigBody{Name: "probe", Channels: []string{"general"}, VoiceChannels: []string{"voice"}, MaxVoice: 10, MaxClients: 20, MaxPerIP: 20,
		Roles: map[string][]string{"user": {"join_channel", "send_chat"}}, DefaultRoles: []string{"user"}}
	head := c.Head()
	e, err := chain.NewEntry(founder, &head, chain.OpConfig, config, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if err := c.Append(e); err != nil {
		return nil, nil, err
	}
	head = c.Head()
	e, err = chain.NewEntry(founder, &head, chain.OpHostAdd, chain.HostAddBody{PubKey: founder.PublicKey(), Priority: 1}, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if err := c.Append(e); err != nil {
		return nil, nil, err
	}
	head = c.Head()
	e, err = chain.NewEntry(founder, &head, chain.OpHostAdd, chain.HostAddBody{PubKey: standby.PublicKey(), Priority: 2}, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if err := c.Append(e); err != nil {
		return nil, nil, err
	}
	return c, founder, nil
}

func waitFor(ctx context.Context, ok func() bool) error {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		if ok() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
func waitMutedEvent(ctx context.Context, events <-chan client.Event, fp string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return errors.New("embedded probe: event stream closed")
			}
			if ev.Voice != nil {
				for _, member := range ev.Voice.Members {
					if member.Fingerprint == fp && member.Muted {
						return nil
					}
				}
			}
		}
	}
}

func retry(ctx context.Context, fn func() error) error {
	var last error
	for {
		if err := fn(); err == nil {
			return nil
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: last error: %v", ctx.Err(), last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type probeAudio struct {
	stop   chan struct{}
	marker byte
}

func newProbeAudio(marker byte) *probeAudio {
	return &probeAudio{stop: make(chan struct{}), marker: marker}
}
func (s *probeAudio) ReadOpusFrame() ([]byte, error) {
	select {
	case <-s.stop:
		return nil, io.EOF
	case <-time.After(20 * time.Millisecond):
		return []byte{s.marker, 0, 0}, nil
	}
}

type probeSink struct {
	want string
	mu   sync.Mutex
	got  bool
}

func newProbeSink(id *identity.Identity) *probeSink {
	return &probeSink{want: identity.Fingerprint(id.PublicKey())}
}
func (s *probeSink) WriteOpusFrame(fp string, _ []byte) {
	if fp == s.want {
		s.mu.Lock()
		s.got = true
		s.mu.Unlock()
	}
}
func (s *probeSink) seen() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.got }

type colorFrames struct{ y byte }

func (s *colorFrames) Next(f *video.Frame) error {
	for i := range f.Y {
		f.Y[i] = s.y
	}
	for i := range f.U {
		f.U[i] = 128
	}
	for i := range f.V {
		f.V[i] = 128
	}
	return nil
}

type probeMediaSink struct {
	mu     sync.Mutex
	frames map[proto.MediaKind]map[string]int
}

func newProbeMediaSink() *probeMediaSink {
	return &probeMediaSink{frames: map[proto.MediaKind]map[string]int{}}
}
func (s *probeMediaSink) WriteMediaFrame(f client.MediaFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frames[f.Kind] == nil {
		s.frames[f.Kind] = map[string]int{}
	}
	s.frames[f.Kind][f.Fingerprint]++
}
func (s *probeMediaSink) seen(kind proto.MediaKind, fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames[kind][fp] > 0
}
func (s *probeMediaSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, byFP := range s.frames {
		for _, c := range byFP {
			n += c
		}
	}
	return n
}
