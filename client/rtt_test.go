package client

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
)

func TestEWMAFirstSample(t *testing.T) {
	var e rttEWMA
	if e.ok {
		t.Fatal("zero value must not be ok")
	}
	e.observe(100 * time.Millisecond)
	if !e.ok || e.v != 100*time.Millisecond {
		t.Fatalf("got %v ok=%v", e.v, e.ok)
	}
}

func TestEWMASmooths(t *testing.T) {
	var e rttEWMA
	e.observe(100 * time.Millisecond)
	e.observe(200 * time.Millisecond)
	if e.v != 120*time.Millisecond {
		t.Fatalf("v = %v, want 120ms", e.v)
	}
}

func TestClientRTTAgainstServer(t *testing.T) {
	url := startServer(t, server.Config{})
	id, _ := identity.Generate()
	c, err := Dial(testCtx(t), url, id, "dave", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if rtt, ok := c.RTT(); ok {
			if rtt >= 100*time.Millisecond {
				t.Fatalf("rtt = %v, want < 100ms", rtt)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no RTT within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPongHandlerIgnoresGarbage(t *testing.T) {
	tr := newRTTTracker()
	for _, p := range []string{"", "short", "123456789"} {
		tr.pong(p, time.Now())
	}
	if _, ok := tr.get(); ok {
		t.Fatal("garbage pong must not produce a sample")
	}
}

func TestRTTTrackerPongAndTimeout(t *testing.T) {
	tr := newRTTTracker()
	t0 := time.Unix(1000, 0)
	p := tr.sent(t0)
	if len(p) != 8 || int64(binary.BigEndian.Uint64([]byte(p))) != t0.UnixNano() {
		t.Fatalf("bad payload %q", p)
	}
	tr.check(t0.Add(5*time.Second), pongTimeout) // not yet
	if _, ok := tr.get(); ok {
		t.Fatal("no sample before timeout")
	}
	tr.check(t0.Add(6*time.Second), pongTimeout)
	if v, ok := tr.get(); !ok || v != pongTimeout {
		t.Fatalf("got %v ok=%v, want 6s", v, ok)
	}
	// A pong that answers an outstanding ping clears it.
	t1 := t0.Add(10 * time.Second)
	p = tr.sent(t1)
	tr.pong(p, t1.Add(50*time.Millisecond))
	tr.check(t1.Add(20*time.Second), pongTimeout) // nothing outstanding
	want := time.Duration(0.8*float64(pongTimeout) + 0.2*float64(50*time.Millisecond))
	if v, _ := tr.get(); v != want {
		t.Fatalf("v = %v, want %v", v, want)
	}
}

func TestPingerStopsOnClose(t *testing.T) {
	url := startServer(t, server.Config{})
	id, _ := identity.Generate()
	c, err := Dial(testCtx(t), url, id, "erin", "")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case <-c.pingDone:
	default:
		t.Fatal("pinger still running after Close")
	}
}
