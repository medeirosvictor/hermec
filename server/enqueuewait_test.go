package server

import (
	"testing"
	"time"
)

// fillSend fills c's outbound buffer, with no writer draining it.
func fillSend(c *conn) {
	for len(c.send) < cap(c.send) {
		c.send <- []byte("{}")
	}
}

// A link whose queue stays full is dropped after the wait, not waited on
// forever: a stuck standby must not stall the active host.
func TestEnqueueWaitTimesOutOnStuckPeer(t *testing.T) {
	old := enqueueWaitTimeout
	enqueueWaitTimeout = 100 * time.Millisecond
	defer func() { enqueueWaitTimeout = old }()
	c := newConn(nil, nil)
	fillSend(c)

	start := time.Now()
	if c.enqueueWait([]byte("{}")) {
		t.Fatal("enqueueWait succeeded on a full queue")
	}
	if d := time.Since(start); d < 90*time.Millisecond || d > 2*time.Second {
		t.Fatalf("gave up after %v, want about 100ms", d)
	}
}

// enqueueWait proceeds as soon as the writer frees enough room, and leaves the
// headroom for live frames while it waits.
func TestEnqueueWaitProceedsWhenWriterDrains(t *testing.T) {
	c := newConn(nil, nil)
	fillSend(c)
	got := make(chan bool, 1)
	go func() { got <- c.enqueueWait([]byte("{}")) }()

	select {
	case <-got:
		t.Fatal("enqueueWait returned with a full queue")
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i < syncHeadroom; i++ { // the writer takes messages
		<-c.send
		signal(c.drained)
	}
	select {
	case ok := <-got:
		if !ok {
			t.Fatal("enqueueWait failed although room was freed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("enqueueWait did not wake when the writer drained")
	}
}

func TestEnqueueWaitFailsOnClosedConn(t *testing.T) {
	c := newConn(nil, nil)
	fillSend(c)
	got := make(chan bool, 1)
	go func() { got <- c.enqueueWait([]byte("{}")) }()
	time.Sleep(20 * time.Millisecond)
	c.closeSend()
	select {
	case ok := <-got:
		if ok {
			t.Fatal("enqueueWait succeeded on a closed conn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("enqueueWait did not notice the close")
	}
}
