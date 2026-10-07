package server

import (
	"testing"
	"time"
)

func sessionCount(s *Server) int {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	return len(s.sessions)
}

func sessionFor(s *Server, ch string) *voiceSession {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	return s.sessions[ch]
}

func waitDone(t *testing.T, vs *voiceSession) {
	t.Helper()
	select {
	case <-vs.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session actor did not exit")
	}
}

// Create/destroy symmetry of the media session, driven by membership only
// (no PeerConnections, no network).
func TestVoiceSessionLifecycle(t *testing.T) {
	s := New(voiceCfg())
	mk := func(fp string) *conn {
		c := newConn(s, nil)
		c.fingerprint, c.name = fp, fp
		s.chMu.Lock()
		s.authed[c] = struct{}{}
		s.chMu.Unlock()
		return c
	}
	a, b := mk("a"), mk("b")

	if code := s.voiceJoin(a, "lounge"); code != "" {
		t.Fatal(code)
	}
	if code := s.voiceJoin(b, "lounge"); code != "" {
		t.Fatal(code)
	}
	vs := sessionFor(s, "lounge")
	if vs == nil || sessionCount(s) != 1 {
		t.Fatalf("want one session for lounge, got %d", sessionCount(s))
	}

	s.voiceLeave(a)
	if sessionCount(s) != 1 {
		t.Fatal("session destroyed while still occupied")
	}
	// Move = leave + join: lounge empties, war-room gets its own session.
	if code := s.voiceJoin(b, "war-room"); code != "" {
		t.Fatal(code)
	}
	waitDone(t, vs)
	vs2 := sessionFor(s, "war-room")
	if vs2 == nil || sessionCount(s) != 1 {
		t.Fatalf("sessions = %d, want only war-room", sessionCount(s))
	}

	s.leaveAll(b) // disconnect path
	waitDone(t, vs2)
	if sessionCount(s) != 0 {
		t.Fatal("sessions leaked")
	}
	s.wg.Wait() // every session goroutine joined
}
