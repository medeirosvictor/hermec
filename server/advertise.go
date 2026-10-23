package server

import (
	"bytes"
	"slices"
)

// advertise is the address list this host announces: the one set with
// SetAdvertise, else the config's.
func (s *Server) advertise() []string {
	s.advMu.RLock()
	defer s.advMu.RUnlock()
	if s.advSet {
		return s.adv
	}
	return s.cfg.Advertise
}

// SetAdvertise replaces the announced addresses; a caller that learns them
// after Start (a slow lookup) uses it.
func (s *Server) SetAdvertise(addrs []string) {
	s.advMu.Lock()
	s.adv, s.advSet = slices.Clone(addrs), true
	s.advMu.Unlock()
}

// SetPasswordKeyHook sets fn, called with the new key whenever the key learned
// from the active host differs from the one held before.
func (s *Server) SetPasswordKeyHook(fn func(key []byte)) {
	s.advMu.Lock()
	s.keyHook = fn
	s.advMu.Unlock()
}

// keyChanged calls the key hook when old and new differ.
func (s *Server) keyChanged(old, key []byte) {
	if bytes.Equal(old, key) {
		return
	}
	s.advMu.RLock()
	fn := s.keyHook
	s.advMu.RUnlock()
	if fn != nil {
		fn(slices.Clone(key))
	}
}
