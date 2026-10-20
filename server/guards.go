package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// DefaultMaxClients is the authenticated-session cap when none is set.
	DefaultMaxClients = 256
	// DefaultMaxPerIP is the concurrent-connection cap per remote IP.
	DefaultMaxPerIP = 16
	// maxPreAuth caps connections that have upgraded but not yet authenticated.
	maxPreAuth = 64

	// authFreeFailures is how many failures per IP are answered at once.
	authFreeFailures = 5
	authWindow       = time.Minute
	authBaseDelay    = time.Second
	authMaxDelay     = time.Minute
)

// authFail tracks failed authentications from one remote IP.
type authFail struct {
	n     int       // failures in the current run
	// until is when the block imposed by the latest failure ends. The run
	// (failure count) is forgotten once a further quiet minute has passed
	// after until, i.e. authWindow after the IP was last refused or failed.
	until time.Time
}

// authBlocked reports whether new connections from ip are refused because of
// recent failed authentications. Lock order: Server.mu, then failMu.
func (s *Server) authBlocked(ip string) bool {
	now := s.clock()
	s.failMu.Lock()
	defer s.failMu.Unlock()
	f := s.fails[ip]
	return f != nil && now.Before(f.until)
}

// guards holds connection-limit and auth-backoff state.
type guards struct {
	ipConns map[string]int // open connections (any state) per remote IP; guarded by Server.mu
	preAuth int            // upgraded connections not yet authenticated; guarded by Server.mu
	clients int            // authenticated sessions; guarded by Server.mu

	failMu sync.Mutex
	fails  map[string]*authFail
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) maxClients() int {
	if s.cfg.MaxClients > 0 {
		return s.cfg.MaxClients
	}
	return DefaultMaxClients
}

func (s *Server) maxPerIP() int {
	if s.cfg.MaxPerIP > 0 {
		return s.cfg.MaxPerIP
	}
	return DefaultMaxPerIP
}

// admit reserves a per-IP and a pre-auth slot for a new connection.
func (s *Server) admit(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authBlocked(ip) {
		return false
	}
	if s.closing || s.preAuth >= maxPreAuth || s.ipConns[ip] >= s.maxPerIP() {
		return false
	}
	s.ipConns[ip]++
	s.preAuth++
	return true
}

func (s *Server) dropPreAuth() {
	s.mu.Lock()
	s.preAuth--
	s.mu.Unlock()
}

func (s *Server) dropIP(ip string) {
	s.mu.Lock()
	if s.ipConns[ip]--; s.ipConns[ip] <= 0 {
		delete(s.ipConns, ip)
	}
	s.mu.Unlock()
}

// reserveClient takes one authenticated-session slot; false when the server is full.
func (s *Server) reserveClient() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients >= s.maxClients() {
		return false
	}
	s.clients++
	return true
}

func (s *Server) releaseClient() {
	s.mu.Lock()
	s.clients--
	s.mu.Unlock()
}

// noteAuthFailure records a failed authentication from ip and returns how
// long to wait before replying: nothing for the first five failures in a run,
// then 1 s doubling up to 60 s. A run ends after a quiet minute.
func (s *Server) noteAuthFailure(ip string) time.Duration {
	now := s.clock()
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if len(s.fails) > 4096 {
		for k, f := range s.fails {
			if now.Sub(f.until) > authWindow {
				delete(s.fails, k)
			}
		}
	}
	f := s.fails[ip]
	if f == nil || now.Sub(f.until) > authWindow {
		f = &authFail{}
		s.fails[ip] = f
	}
	var d time.Duration
	if f.n >= authFreeFailures {
		d = authMaxDelay
		if k := f.n - authFreeFailures; k < 6 {
			d = authBaseDelay << k
		}
		if d > authMaxDelay {
			d = authMaxDelay
		}
	}
	if f.n < 1000 {
		f.n++
	}
	f.until = now.Add(d)
	return d
}

// noteAuthSuccess forgets ip's failures.
func (s *Server) noteAuthSuccess(ip string) {
	s.failMu.Lock()
	delete(s.fails, ip)
	s.failMu.Unlock()
}

