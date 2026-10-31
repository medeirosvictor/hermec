package server

import (
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// floodConn models a UDP socket with a small receive buffer. A producer
// "sends" audio packets and (optionally) oversized datagrams at fixed rates;
// when the buffer is full the arrival is dropped, like the kernel does. An
// oversized datagram makes ReadFrom return WSAEMSGSIZE and is consumed.
type floodConn struct {
	net.PacketConn
	q    chan bool // true = audio, false = oversized
	stop chan struct{}
}

func newFloodConn(audioPerSec, bigPerSec int) *floodConn {
	f := &floodConn{q: make(chan bool, 8), stop: make(chan struct{})}
	produce := func(perSec int, audio bool) {
		if perSec == 0 {
			return
		}
		go func() {
			t := time.NewTicker(time.Second / time.Duration(perSec))
			defer t.Stop()
			for {
				select {
				case <-f.stop:
					return
				case <-t.C:
					select {
					case f.q <- audio:
					default: // receive buffer full: dropped
					}
				}
			}
		}()
	}
	produce(audioPerSec, true)
	produce(bigPerSec, false)
	return f
}

func (f *floodConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case audio := <-f.q:
		if !audio {
			return 0, nil, os.NewSyscallError("wsarecvfrom", syscall.Errno(10040))
		}
		return copy(p, "audio"), &net.UDPAddr{}, nil
	case <-f.stop:
		return 0, nil, net.ErrClosed
	}
}

func (f *floodConn) SetReadDeadline(time.Time) error { return nil }

func countAudio(t *testing.T, bigPerSec int, d time.Duration) int {
	t.Helper()
	f := newFloodConn(50, bigPerSec)
	c := &mediaConn{PacketConn: f}
	var got atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for {
			if _, _, err := c.ReadFrom(buf); err != nil {
				return
			}
			got.Add(1)
		}
	}()
	time.Sleep(d)
	close(f.stop)
	<-done
	return int(got.Load())
}

// A flood of oversized datagrams must not cost audio: the read error for a
// consumed datagram must never put the reader to sleep. The windows are long
// and the test is not parallel, so scheduler noise cannot push the flood count
// far below the baseline. The regression this guards against kept only ~12%.
// Do not add t.Parallel here.
func TestMediaFloodDoesNotCutAudio(t *testing.T) {
	base := countAudio(t, 0, 3*time.Second)
	flood := countAudio(t, 200, 3*time.Second)
	t.Logf("audio in 3 s: baseline %d, under flood %d", base, flood)
	if base < 120 {
		t.Fatalf("baseline too low (%d): test rig is broken", base)
	}
	if float64(flood) < 0.75*float64(base) {
		t.Fatalf("audio under flood = %d, baseline %d: lost more than 25%%", flood, base)
	}
}
