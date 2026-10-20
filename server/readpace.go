package server

import (
	"errors"
	"syscall"
	"time"
)

// errBackoffAfter is how many consecutive read errors that did not consume a
// datagram are tolerated before the reader sleeps between attempts.
const errBackoffAfter = 1000

// consumedDatagram reports whether a UDP read error means the kernel handed
// the datagram over anyway: Windows WSAEMSGSIZE (10040, datagram larger than
// the buffer) and WSAECONNRESET (10054, an ICMP port-unreachable for an
// earlier send). Retrying at once is right, and sleeping would let the socket
// buffer fill and drop real packets.
func consumedDatagram(err error) bool {
	return errors.Is(err, syscall.Errno(10040)) || errors.Is(err, syscall.Errno(10054))
}

// readPacer keeps a UDP read loop that swallows errors from hot-spinning on a
// socket that is persistently broken, without ever delaying the reads of a
// working one. Single goroutine only.
type readPacer struct{ run int }

// reset forgets the error run (a good read or a timeout happened).
func (p *readPacer) reset() { p.run = 0 }

// failed records a swallowed read error, sleeping 1 ms once errBackoffAfter
// of them came in a row.
func (p *readPacer) failed(err error) {
	if consumedDatagram(err) {
		p.run = 0
		return
	}
	if p.run++; p.run >= errBackoffAfter {
		time.Sleep(time.Millisecond)
	}
}
