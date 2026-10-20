package client

import (
	"testing"
	"time"
)

func TestLatePongAfterTimeoutIgnored(t *testing.T) {
	tr := newRTTTracker()
	t0 := time.Now()
	payload := tr.sent(t0)
	tr.check(t0.Add(pongTimeout), pongTimeout)
	if v, _ := tr.get(); v != pongTimeout {
		t.Fatalf("timeout sample = %v, want %v", v, pongTimeout)
	}
	tr.pong(payload, t0.Add(pongTimeout+time.Second)) // the same ping, answered too late
	if v, _ := tr.get(); v != pongTimeout {
		t.Fatalf("late pong changed rtt to %v; it was already counted", v)
	}
}

func TestPongAfterOtherTimeoutStillCounts(t *testing.T) {
	tr := newRTTTracker()
	t0 := time.Now()
	tr.sent(t0)
	tr.check(t0.Add(pongTimeout), pongTimeout)
	t1 := t0.Add(7 * time.Second)
	p := tr.sent(t1)
	tr.pong(p, t1.Add(10*time.Millisecond))
	if v, _ := tr.get(); v >= pongTimeout {
		t.Fatalf("fresh pong not counted: %v", v)
	}
}
