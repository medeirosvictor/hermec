package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A data write stalled while holding writeMu must not delay timeout sampling.
func TestTimeoutSampledWhileWriteMuHeld(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
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
	c.rtt.sent(t0) // a ping that never gets answered

	c.writeMu.Lock() // simulates a stalled data write
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.pingOnce(pongTimeout, func() time.Time { return t0.Add(pongTimeout) })
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if v, ok := c.rtt.get(); ok && v == pongTimeout {
			break
		}
		if time.Now().After(deadline) {
			c.writeMu.Unlock()
			t.Fatal("timeout not sampled while writeMu was held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("ping was sent despite the held write lock")
	default:
	}
	c.writeMu.Unlock()
	<-done
}
