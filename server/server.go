// Package server implements the Hermec websocket server: connection
// lifecycle and challenge-response authentication.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/ice/v4"
	"github.com/pion/transport/v5"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/netpath"
)

// Config configures a Server. Programmatic embedders should start from DefaultConfig().
type Config struct {
	// Addr is the listen address; use "127.0.0.1:0" in tests.
	Addr string
	// Network routes this server's listeners, host links and WebRTC sockets.
	// Nil preserves normal system networking. Programmatic private-network
	// providers must fail closed rather than falling back to system sockets.
	Network netpath.Network
	// ServerID is the machine-local ID of the chain this config belongs to (as
	// chain.ID.String()); the app stores it in hosted.toml. Unused by Server.
	ServerID string
	// PasswordKey, when set, is the 32-byte admission key clients prove they
	// know without sending a password.
	PasswordKey []byte
	// AllowedKeys are permitted fingerprints. Empty means an open server.
	AllowedKeys []string
	// Roles maps fingerprints to roles and roles to permissions.
	Roles roles.Config
	// Channels are the server's static channel names.
	Channels []string
	// VoiceChannels are the server's static voice channel names; they must
	// not collide with Channels.
	VoiceChannels []string
	// TLSCert and TLSKey are PEM file paths. Set both to serve wss://;
	// leave both empty for plain ws://.
	TLSCert, TLSKey string
	// PublicMode marks an internet-facing dedicated server. The command refuses
	// to start it without TLS and admission unless an explicit unsafe
	// development override is supplied. It is machine-local, not chain state.
	PublicMode bool
	// PublicDNS is the public DNS name covered by the trusted TLS certificate.
	// It is required for public dedicated listeners and is machine-local.
	PublicDNS string
	// PublicIP, if set, is advertised as the server's ICE host address
	// (NAT 1:1) for voice on internet-facing hosts. Empty = local addresses.
	PublicIP string
	// UDPPortMin and UDPPortMax, when both non-zero, restrict the UDP ports
	// used for voice media (for firewalls). Min must be <= Max.
	UDPPortMin, UDPPortMax uint16
	// MediaPort is the single UDP port all WebRTC media uses (through an ICE
	// UDP mux). 0 means the same port number as the listener. Ignored when
	// UDPPortMin/UDPPortMax are set (legacy ephemeral range).
	MediaPort int
	// AllowLoopbackICE restricts ICE to loopback (no LAN candidates, no mDNS).
	// This breaks remote peers, so it is for tests only; not exposed in the
	// TOML file.
	AllowLoopbackICE bool
	// MsgRate and MsgBurst are the post-auth inbound websocket message
	// limit per connection (messages/sec sustained, bucket size). Either
	// being 0 disables post-auth limiting. DefaultConfig sets 30 and 60.
	MsgRate, MsgBurst int
	// MaxVoice caps participants per voice channel; 0 means DefaultMaxVoice.
	MaxVoice int
	// maxOfferBytes overrides proto.MaxMessageSize as the largest server rtc_offer
	// the SFU will send (tests only).
	maxOfferBytes int
	// Discoverable, when true, binds a UDP discovery responder on the same
	// port number as the listener. DefaultConfig sets it true; the zero
	// value (programmatic Config literals) is off.
	Discoverable bool
	// ServerName is advertised in discovery replies (max 64 runes). Empty
	// means the listener's host:port.
	ServerName string
	// MaxClients caps authenticated sessions; 0 means DefaultMaxClients. A
	// client over the cap gets error "server_full".
	MaxClients int
	// MaxPerIP caps concurrent connections per remote IP; 0 means
	// DefaultMaxPerIP. TOML: max_per_ip.
	MaxPerIP int
	// HistoryDays and HistoryMB cap the stored chat history (nil = default;
	// see HistoryLimits). Group fields: the chain config carries them.
	HistoryDays, HistoryMB *int
	// HistoryPath is the chat history file; empty keeps history in memory only.
	HistoryPath string
	// Logf receives every server log line; nil means log.Printf.
	Logf func(format string, args ...any)
	// HostKey is the host's own key. Nil makes New generate an ephemeral one.
	HostKey *identity.Identity
	// Chain is the server chain whose state supplies the group fields (name,
	// channels, limits, allowed keys, roles). Nil makes New build an in-memory
	// chain from this Config's own group fields, founded by HostKey.
	Chain *chain.Chain
	// Mode is the host's starting mode. The zero value is ModeActive.
	Mode Mode
	// Prepared makes this a passive dedicated destination until an explicit
	// committed-transition activation releases it.
	Prepared bool
	// DedicatedTransitionPath is the private, durable cutover journal. A
	// configured prepared destination loads a committed record as active; an
	// active source records a fence here before it sends a cutover certificate.
	DedicatedTransitionPath string
	// HostName and Advertise describe this host to its peers and clients: a
	// display name and the addresses it can be dialed at.
	HostName  string
	Advertise []string
	// KnownHosts are the other hosts of the server this machine has learned,
	// kept in the local config file (hosted.toml).
	KnownHosts []string
}

// logf writes a server log line through cfg.Logf, or log.Printf.
func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

const (
	authTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
	sendBuffer   = 64
)

// Server accepts websocket connections and authenticates them.
type Server struct {
	cfg     Config
	chain   *chain.Chain
	group   groupState
	chainMu sync.Mutex // serializes chain_append: append, refresh, broadcast, kick
	// dedicatedMu serializes a dedicated readiness capture/commit with every
	// source mutation that can change its chain, history, or serving mode. If
	// both locks are needed, acquire dedicatedMu before chainMu.
	dedicatedMu sync.Mutex
	upgrader    websocket.Upgrader

	pwMu       sync.RWMutex // guards pwMirrored and pwMirror
	pwMirrored bool         // the key below came from the active host and overrides cfg
	pwMirror   []byte

	advMu   sync.RWMutex // guards adv, advSet and keyHook
	adv     []string
	advSet  bool
	keyHook func([]byte)

	mu      sync.Mutex
	conns   map[*conn]struct{}
	closing bool
	// shutdown is set when Shutdown begins: the host no longer announces a
	// standby change, as its links close before its clients do.
	shutdown atomic.Bool
	wg       sync.WaitGroup // per-connection goroutines and the accept loop

	lastChatTS time.Time  // newest chat timestamp issued; guarded by chMu
	chMu       sync.Mutex // guards chans, voice, authed, conn.voiceCh, lastChatTS; see channels.go, voice.go
	chans      channelSet
	voice      voiceSet
	authed     map[*conn]struct{} // authenticated conns; voice_state goes to all
	warm       map[*conn]struct{} // warm (paused) sessions: chain frames and hb only; guarded by chMu
	modeState  serverMode
	takeover   takeoverState
	load       func() (voice, clients int)      // the host load for the yield rule; tests replace it
	promoted   atomic.Bool                      // takeover promoted this host; reset when it leaves active mode
	prepared   atomic.Bool                      // committed transition has not explicitly activated this destination
	warmSlots  int                              // warm sessions holding a slot of the warm cap; guarded by mu
	warmMu     sync.Mutex                       // guards warmPeers
	warmPeers  map[*conn]*webrtc.PeerConnection // data-channel-only PCs of warm clients; survive promotion until voice_join adopts them
	links      map[*conn]struct{}               // host links being served; guarded by chMu
	standby    atomic.Pointer[proto.HostInfo]   // the host picked as standby, nil for none
	// sessions holds the media session of each occupied voice channel; guarded by chMu.
	sessions map[string]*voiceSession

	// clock is the time source for rate limiting and history trims; tests replace it.
	clock func() time.Time

	// hist is the chat history store; nil until Start opens it (New runs
	// refreshGroup first), so every user must tolerate nil.
	hist     *History
	stopHist chan struct{} // closed by Shutdown to stop the trimmer
	stopOnce sync.Once

	limitMu            sync.Mutex // guards the last applied history limits
	limitSet           bool
	limitDays, limitMB int

	guards

	http    *http.Server
	ln      net.Listener
	udp     net.PacketConn // discovery responder; nil when not discoverable
	mux     ice.UDPMux     // single-port media mux; nil in legacy UDP-range mode
	pionNet transport.Net  // validated provider network; nil for system networking

	// readinessLock is held by an active source for its whole serving lifetime.
	// It serializes offline attestation reset with live prepared links.
	readinessLock io.Closer
}

// New returns a Server for cfg. Call Start to begin serving.
func New(cfg Config) *Server {
	if cfg.HostKey == nil {
		k, err := identity.Generate()
		if err != nil {
			panic(fmt.Sprintf("server: generate host key: %v", err))
		}
		cfg.HostKey = k
	}
	if cfg.Chain == nil {
		cfg = defaultGroupConfig(cfg)
		c, err := newChain(cfg.HostKey, cfg)
		if err != nil {
			panic(fmt.Sprintf("server: build default chain: %v", err))
		}
		cfg.Chain = c
	}
	st := cfg.Chain.State()
	cfg = chainCfg(cfg, st)
	s := &Server{
		chain: cfg.Chain,
		cfg:   cfg,
		clock: time.Now,
		conns: make(map[*conn]struct{}),

		stopHist: make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Native clients send no Origin; any Origin means a browser page.
			CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
		},
	}
	if err := CheckHost(cfg.Chain, cfg.HostKey); err != nil {
		s.logf("server: WARNING: %v; clients will refuse this host", err)
	}
	s.load = s.measureLoad
	s.prepared.Store(cfg.Prepared || cfg.Mode == ModePrepared)
	if s.prepared.Load() {
		s.modeState.mode.Store(int32(ModePrepared))
	} else {
		s.modeState.mode.Store(int32(cfg.Mode))
	}
	s.ipConns = make(map[string]int)
	s.fails = make(map[string]*authFail)
	s.sent = make(map[string]*chainSend)
	s.refreshGroup()
	s.initChannels()
	return s
}

// Start listens on cfg.Addr and serves in the background.
func (s *Server) Start() (err error) {
	// Opening history also applies limits and may restore the durable dedicated
	// mode. If that startup work fails, leave this instance indistinguishable
	// from one that has not started, so a retry cannot skip journal validation.
	initialMode, initialPrepared := s.Mode(), s.Prepared()
	initialLastChatTS := s.lastChatTS
	initialLimitSet, initialLimitDays, initialLimitMB := s.limitSet, s.limitDays, s.limitMB
	var openedHistory *History
	defer func() {
		if err == nil || openedHistory == nil {
			return
		}
		_ = openedHistory.Close()
		if s.hist == openedHistory {
			s.hist = nil
		}
		s.chMu.Lock()
		s.lastChatTS = initialLastChatTS
		s.chMu.Unlock()
		s.limitMu.Lock()
		s.limitSet, s.limitDays, s.limitMB = initialLimitSet, initialLimitDays, initialLimitMB
		s.limitMu.Unlock()
		s.prepared.Store(initialPrepared)
		s.modeState.mode.Store(int32(initialMode))
	}()
	if s.cfg.DedicatedTransitionPath != "" && s.Mode() == ModeActive && !s.Prepared() && s.readinessLock == nil {
		s.readinessLock, err = AcquireDedicatedReadinessLock(s.cfg.DedicatedTransitionPath)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				s.releaseReadinessLock()
			}
		}()
	}
	if err := s.validateNetwork(); err != nil {
		return err
	}
	if s.hist == nil {
		h, err := OpenHistory(s.cfg.HistoryPath)
		if err != nil {
			return fmt.Errorf("server: open history: %w", err)
		}
		s.hist = h
		openedHistory = h
		// New chat timestamps must sort after every stored line, even across
		// a restart within one clock tick or after the clock was set back.
		s.chMu.Lock()
		// A stored time far ahead of the clock is bogus and would pin it.
		limit := s.clock().Add(maxStoredSkew)
		if n := h.NewestBefore(limit); n.After(s.lastChatTS) {
			s.lastChatTS = n
		}
		if n := h.Newest(); n.After(limit) {
			s.logf("server: history: newest stored line is dated %s, over %s ahead of now; ignoring it for new timestamps", n.Format(time.RFC3339), maxStoredSkew)
		}
		s.chMu.Unlock()
		s.applyHistoryLimits()
		if n := h.Skipped(); n > 0 {
			s.logf("server: history: skipped %d malformed lines in %s", n, s.cfg.HistoryPath)
		}
		if err := s.restoreDedicatedTransition(); err != nil {
			return err
		}
		s.trimHistory()
	}
	// With an ephemeral port (":0") the matching UDP port may already be
	// taken; try a fresh TCP port a few times before giving up.
	_, port, _ := net.SplitHostPort(s.cfg.Addr)
	attempts := 1
	if port == "0" {
		attempts = 10
	}
	for i := 0; i < attempts; i++ {
		if err = s.start(); !errors.Is(err, ErrDiscoveryBind) && !errors.Is(err, ErrMediaBind) {
			return err
		}
	}
	return err
}

func (s *Server) start() error {
	if (s.cfg.TLSCert == "") != (s.cfg.TLSKey == "") {
		return errors.New("server: TLSCert and TLSKey must be set together")
	}
	ln, err := s.listen(context.Background(), "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}
	if s.cfg.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey)
		if err != nil {
			ln.Close()
			return fmt.Errorf("server: load tls keypair: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
	}
	s.ln = ln
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		s.ln = nil
		return fmt.Errorf("server: listener address: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		ln.Close()
		s.ln = nil
		return fmt.Errorf("server: listener port: %w", err)
	}
	if ip := net.ParseIP(host); ip != nil {
		host = discoveryBindHost(ip)
	}
	if err := s.startUDP(host, port); err != nil {
		ln.Close()
		s.ln = nil
		return err
	}
	s.http = &http.Server{
		Handler:           http.HandlerFunc(s.handleWS),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.wg.Add(3)
	go func() {
		defer s.wg.Done()
		s.heartbeatLoop()
	}()
	go func() {
		defer s.wg.Done()
		_ = s.http.Serve(ln) // always returns on Shutdown
	}()
	go func() {
		defer s.wg.Done()
		s.trimLoop()
	}()
	return nil
}

// applyHistoryLimits sets the store's caps from the live chain config and
// reports whether they differ from the ones in force before.
func (s *Server) applyHistoryLimits() (changed bool) {
	if s.hist == nil {
		return false
	}
	// One hold over read, compare and set: otherwise a trim that read the
	// state before a config append could write the old limits back after it.
	s.limitMu.Lock()
	defer s.limitMu.Unlock()
	days, mb := chainCfg(s.cfg, s.chain.State()).HistoryLimits()
	changed = !s.limitSet || days != s.limitDays || mb != s.limitMB
	s.limitSet, s.limitDays, s.limitMB = true, days, mb
	s.hist.SetLimits(days, mb)
	return changed
}

// trimHistory trims the store with the current limits; errors are only logged.
func (s *Server) trimHistory() {
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
	// Do not mutate a captured source history after it has been fenced, nor a
	// passive destination while it is proving its exact imported boundary.
	if s.Mode() != ModeActive {
		return
	}
	s.trimHistoryLocked()
}

// trimHistoryLocked is trimHistory while dedicatedMu is held.
func (s *Server) trimHistoryLocked() {
	s.applyHistoryLimits()
	if err := s.hist.Trim(s.clock()); err != nil {
		s.logf("server: history trim: %v", err)
	}
}

// trimLoop trims the history every trimInterval until Shutdown.
func (s *Server) trimLoop() {
	t := time.NewTicker(trimInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopHist:
			return
		case <-t.C:
			s.trimHistory()
		}
	}
}

// ID returns the authenticated server chain ID.
func (s *Server) ID() string { return s.chain.ID().String() }

// Addr returns the actual listen address (host:port) after Start.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Shutdown stops accepting, closes all connections, and waits for every
// server goroutine to exit or ctx to expire.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdown.Store(true)
	s.mu.Lock()
	s.closing = true
	open := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		open = append(open, c)
	}
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stopHist) })
	if s.hist != nil {
		defer s.hist.Close() // after the goroutines below have stopped
	}

	var err error
	if s.http != nil {
		err = s.http.Shutdown(ctx)
	}
	if s.udp != nil {
		s.udp.Close() // unblocks the discovery loop
	}
	if s.mux != nil {
		s.mux.Close()
	}
	// Hijacked websocket connections are not closed by http.Server.Shutdown.
	for _, c := range open {
		c.ws.Close()
	}

	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		s.releaseReadinessLock()
		return err
	case <-ctx.Done():
		// The service may still have live links. Keep the interprocess lock until
		// they are gone, then release it without requiring a second Shutdown.
		go func() {
			<-done
			s.releaseReadinessLock()
		}()
		return errors.Join(err, ctx.Err())
	}
}

func (s *Server) releaseReadinessLock() {
	if s.readinessLock != nil {
		_ = s.readinessLock.Close()
		s.readinessLock = nil
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser origins are not allowed", http.StatusForbidden)
		return
	}
	ip := remoteIP(r)
	if !s.admit(ip) {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	pre := true
	leavePreAuth := func() {
		if pre {
			pre = false
			s.dropPreAuth()
		}
	}
	defer func() { leavePreAuth(); s.dropIP(ip) }()

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade has already replied to the client
	}
	ws.SetReadLimit(proto.MaxMessageSize)

	c := newConn(s, ws)
	c.ip, c.leavePreAuth = ip, leavePreAuth
	if !s.register(c) {
		ws.Close()
		return
	}
	go c.writeLoop()
	c.readLoop() // runs on this handler goroutine until the conn ends
}

// register adds c to the registry and its goroutines to the wait group.
// It returns false if the server is shutting down.
func (s *Server) register(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[c] = struct{}{}
	s.wg.Add(2) // read loop + write loop
	return true
}

func (s *Server) unregister(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}
