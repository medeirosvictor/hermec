package state

import (
	"math/rand"
	"time"
)

// JitteredBackoff is NextBackoff(attempt) spread by +-25% so clients that lost
// the same server together do not retry in lockstep. rnd returns [0,1]; nil
// uses math/rand.
func JitteredBackoff(attempt int, rnd func() float64) time.Duration {
	if rnd == nil {
		rnd = rand.Float64
	}
	f := 0.75 + 0.5*rnd()
	return time.Duration(float64(NextBackoff(attempt)) * f)
}
