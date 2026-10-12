package state

import "time"

// maxBackoff is the reconnect delay ceiling.
const maxBackoff = 30 * time.Second

// NextBackoff is the reconnect delay before attempt number attempt (0-based):
// 1s doubling per attempt, capped at 30s. Negative attempts count as 0.
func NextBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= 5 {
		return maxBackoff
	}
	return time.Second << attempt
}
