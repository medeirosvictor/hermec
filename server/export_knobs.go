package server

// Test only. This is the one place the server package exposes timing knobs to
// tests in other packages (client, ui, integration, cmd), which cannot use a
// _test.go file. In-package tests use the same functions.

import (
	"time"

	"github.com/medeirosvictor/hermec/server/history"
)

// SetHeartbeatForTest shortens (or lengthens) the heartbeat interval and grace
// and returns a function that restores them. Call it before starting servers:
// the timing is read without synchronization.
func SetHeartbeatForTest(interval, grace time.Duration) (restore func()) {
	oldI, oldG := heartbeatInterval, heartbeatGrace
	heartbeatInterval, heartbeatGrace = interval, grace
	return func() { heartbeatInterval, heartbeatGrace = oldI, oldG }
}

// SetLoadForTest replaces the load (voice members, clients) the yield rule
// compares.
func (s *Server) SetLoadForTest(f func() (voice, clients int)) { s.load = f }

// HistoryForTest returns the server's chat history store.
func (s *Server) HistoryForTest() *history.Store { return s.hist }
