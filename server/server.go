// Package server implements the Hermec websocket server: connection
// lifecycle and challenge-response authentication.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// Config configures a Server.
type Config struct {
	// Addr is the listen address; use "127.0.0.1:0" in tests.
	Addr string
	// Password, when non-empty, must be presented by every client.
	Password string
	// AllowedKeys are permitted fingerprints. Empty means an open server.
	AllowedKeys []string
	// Roles maps fingerprints to roles and roles to permissions.
	Roles roles.Config
	// Channels are the server's static channel names.
	Channels []string
	// TLSCert and TLSKey are PEM file paths. Set both to serve wss://;
	// leave both empty for plain ws://.
	TLSCert, TLSKey string
}

const (
	authTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
	sendBuffer   = 64
)

// Server accepts websocket connections and authenticates them.
type Server struct {
	cfg      Config
	allowed  map[string]struct{}
	upgrader websocket.Upgrader

	mu      sync.Mutex
	conns   map[*conn]struct{}
	closing bool
	wg      sync.WaitGroup // per-connection goroutines and the accept loop

	chMu  sync.Mutex // guards chans; see channels.go
	chans channelSet

	http *http.Server
	ln   net.Listener
}

// New returns a Server for cfg. Call Start to begin serving.
func New(cfg Config) *Server {
	s := &Server{
		cfg:   cfg,
		conns: make(map[*conn]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
		},
	}
	s.initChannels()
	if len(cfg.AllowedKeys) > 0 {
		s.allowed = make(map[string]struct{}, len(cfg.AllowedKeys))
		for _, fp := range cfg.AllowedKeys {
			s.allowed[fp] = struct{}{}
		}
	}
	return s
}

// Start listens on cfg.Addr and serves in the background.
func (s *Server) Start() error {
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
	s.http = &http.Server{Handler: http.HandlerFunc(s.handleWS)}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.http.Serve(ln) // always returns on Shutdown
	}()
	return nil
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

	var err error
	if s.http != nil {
		err = s.http.Shutdown(ctx)
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
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade has already replied to the client
	}
	ws.SetReadLimit(proto.MaxMessageSize)

	c := newConn(s, ws)
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
