package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// While a data write holds writeMu the pinger must neither wait nor write, and
// must still sample the outstanding ping's timeout on the tick it falls due.
func TestPingerNeverWaitsOnWriteMu(t *testing.T) {
	var pings atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.SetPingHandler(func(string) error { pings.Add(1); return nil })
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	c := &Client{ws: ws, rtt: newRTTTracker()}
	t0 := time.Now()
	c.rtt.sent(t0) // ping N-1, never answered

	c.writeMu.Lock() // a stalled data write
	tick := 2 * time.Second
	for i := 1; i <= 5; i++ {
		at := t0.Add(time.Duration(i) * tick)
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.pingOnce(pongTimeout, func() time.Time { return at })
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			c.writeMu.Unlock()
			t.Fatalf("tick %d: pinger blocked on writeMu", i)
		}
		v, ok := c.rtt.get()
		if due := time.Duration(i)*tick >= pongTimeout; due != (ok && v == pongTimeout) {
			c.writeMu.Unlock()
			t.Fatalf("tick %d: due=%v but sample ok=%v v=%v", i, due, ok, v)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := pings.Load(); n != 0 {
		c.writeMu.Unlock()
		t.Fatalf("%d ping(s) written while writeMu was held", n)
	}
	if !c.rtt.outstandingZero() {
		c.writeMu.Unlock()
		t.Fatal("a ping was registered while the lock was held")
	}

	c.writeMu.Unlock() // pings resume
	c.pingOnce(pongTimeout, func() time.Time { return t0.Add(12 * time.Second) })
	deadline := time.Now().Add(2 * time.Second)
	for pings.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no ping after the lock was released")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rttTracker) outstandingZero() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outstanding.IsZero()
}
