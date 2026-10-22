package server

import "time"

// SetHeartbeatForTest is for tests only. It shortens (or lengthens) the heartbeat interval and grace
// for tests in other packages and returns a function that restores them. Call
// it before starting servers: the timing is read without synchronization.
func SetHeartbeatForTest(interval, grace time.Duration) (restore func()) {
	oldI, oldG := heartbeatInterval, heartbeatGrace
	heartbeatInterval, heartbeatGrace = interval, grace
	return func() { heartbeatInterval, heartbeatGrace = oldI, oldG }
}

// SetHostTimersForTest is for tests only. It shortens the Host orchestrator's
// cold-start window, active-host probe period, handback idle time and cold
// probe period, and returns a function that restores them. Call it before
// starting hosts: the timing is read without synchronization.
func SetHostTimersForTest(coldStart, probe, handback, coldProbe time.Duration) (restore func()) {
	oc, op, oh, ocp := coldStartWindow, probeEvery, handbackIdle, coldProbeEvery
	coldStartWindow, probeEvery, handbackIdle, coldProbeEvery = coldStart, probe, handback, coldProbe
	return func() { coldStartWindow, probeEvery, handbackIdle, coldProbeEvery = oc, op, oh, ocp }
}
