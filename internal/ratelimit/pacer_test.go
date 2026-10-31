package ratelimit

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestReadPacer(t *testing.T) {
	var p Pacer
	start := time.Now()
	for i := 0; i < 5000; i++ {
		p.Failed(os.NewSyscallError("wsarecvfrom", syscall.Errno(10040)))
		p.Failed(os.NewSyscallError("wsarecvfrom", syscall.Errno(10054)))
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("errors that consumed a datagram slept for %v", d)
	}
	if p.run != 0 {
		t.Fatalf("run = %d after consuming errors, want 0", p.run)
	}
	for i := 0; i < errBackoffAfter-1; i++ {
		p.Failed(os.ErrDeadlineExceeded) // any other error counts
	}
	start = time.Now()
	for i := 0; i < 50; i++ {
		p.Failed(os.ErrInvalid)
	}
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Fatalf("a long error run did not back off (%v for 50 errors)", d)
	}
	p.Reset()
	if p.run != 0 {
		t.Fatal("reset did not clear the run")
	}
}
