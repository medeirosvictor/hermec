// Package server implements the Hermec websocket server: connection
// lifecycle and challenge-response authentication.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/ice/v4"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// Config configures a Server. Programmatic embedders should start from DefaultConfig().
type Config struct {
	// Addr is the listen address; use "127.0.0.1:0" in tests.
	Addr string
	// ServerID is the machine-local ID of the chain this config belongs to (as
	// chain.ID.String()); the app stores it in hosted.toml. Unused by Server.
	ServerID string
	// Password, when non-empty, must be presented by every client.
	Password string
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
	// NoVP8 drops only the VP8 codec from the MediaEngine (share messages stay
	// supported). For tests only; not exposed in the TOML file.
	NoVP8 bool
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
	cfg      Config
	chain    *chain.Chain
	group    groupState
	chainMu  sync.Mutex // serializes chain_append: append, refresh, broadcast, kick
	upgrader websocket.Upgrader

	mu      sync.Mutex
	conns   map[*conn]struct{}
	closing bool
	wg      sync.WaitGroup // per-connection goroutines and the accept loop

	lastChatTS time.Time  // newest chat timestamp issued; guarded by chMu
	chMu       sync.Mutex // guards chans, voice, authed, conn.voiceCh, lastChatTS; see channels.go, voice.go
	chans      channelSet
	voice      voiceSet
	authed     map[*conn]struct{} // authenticated conns; voice_state goes to all
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

	http *http.Server
	ln   net.Listener
	udp  net.PacketConn // discovery responder; nil when not discoverable
	mux  ice.UDPMux     // single-port media mux; nil in legacy UDP-range mode
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
	s.ipConns = make(map[string]int)
	s.fails = make(map[string]*authFail)
	s.refreshGroup()
	s.initChannels()
	return s
}

// Start listens on cfg.Addr and serves in the background.
func (s *Server) Start() error {
	if s.hist == nil {
		h, err := OpenHistory(s.cfg.HistoryPath)
		if err != nil {
			return fmt.Errorf("server: open history: %w", err)
		}
		s.hist = h
		// New chat timestamps must sort after every stored line, even across
		// a restart within one clock tick or after the clock was set back.
		s.chMu.Lock()
		// A stored time far ahead of the clock is bogus and would pin it.
		if n := h.Newest(); n.After(s.lastChatTS) {
			if n.After(s.clock().Add(maxStoredSkew)) {
				s.logf("server: history: newest stored line is dated %s, over %s ahead of now; ignoring it for new timestamps", n.Format(time.RFC3339), maxStoredSkew)
			} else {
				s.lastChatTS = n
			}
		}
		s.chMu.Unlock()
		s.applyHistoryLimits()
		if n := h.Skipped(); n > 0 {
			s.logf("server: history: skipped %d malformed lines in %s", n, s.cfg.HistoryPath)
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
	var err error
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
	ln, err := net.Listen("tcp", s.cfg.Addr)
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
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		if err := s.startUDP(discoveryBindHost(tcp.IP), tcp.Port); err != nil {
			ln.Close()
			s.ln = nil
			return err
		}
	}
	s.http = &http.Server{
		Handler:           http.HandlerFunc(s.handleWS),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.wg.Add(2)
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
	s.mu.Lock()
	s.closing = true
	open := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		open = append(open, c)
	}
	s.mu.Unlock()
	s.stopOnce.Do(func() { close(s.stopHist) })

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
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
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
