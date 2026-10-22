package server

// Test only. This is the one place the server package exposes timing knobs to
// tests in other packages (client, ui, integration, cmd), which cannot use a
// _test.go file. In-package tests use the same functions.

import "time"

// SetHeartbeatForTest shortens (or lengthens) the heartbeat interval and grace
// and returns a function that restores them. Call it before starting servers:
// the timing is read without synchronization.
func SetHeartbeatForTest(interval, grace time.Duration) (restore func()) {
	oldI, oldG := heartbeatInterval, heartbeatGrace
	heartbeatInterval, heartbeatGrace = interval, grace
	return func() { heartbeatInterval, heartbeatGrace = oldI, oldG }
}

// SetHostTimersForTest shortens the Host orchestrator's cold-start window,
// active-host probe period, handback idle time and cold probe period, and
// returns a function that restores them. Call it before starting hosts: the
// timing is read without synchronization.
func SetHostTimersForTest(coldStart, probe, handback, coldProbe time.Duration) (restore func()) {
	oc, op, oh, ocp := coldStartWindow, probeEvery, handbackIdle, coldProbeEvery
	coldStartWindow, probeEvery, handbackIdle, coldProbeEvery = coldStart, probe, handback, coldProbe
	return func() { coldStartWindow, probeEvery, handbackIdle, coldProbeEvery = oc, op, oh, ocp }
}
