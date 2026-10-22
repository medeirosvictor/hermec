package server

import "time"

// SetHeartbeatForTest shortens (or lengthens) the heartbeat interval and grace
// for tests in other packages and returns a function that restores them. Call
// it before starting servers: the timing is read without synchronization.
func SetHeartbeatForTest(interval, grace time.Duration) (restore func()) {
	oldI, oldG := heartbeatInterval, heartbeatGrace
	heartbeatInterval, heartbeatGrace = interval, grace
	return func() { heartbeatInterval, heartbeatGrace = oldI, oldG }
}
