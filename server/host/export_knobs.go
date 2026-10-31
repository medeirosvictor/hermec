package host

// Test only. Like server/export_knobs.go, this lets tests in other packages
// (integration) set the orchestrator's timing.

import "time"

// SetTimersForTest shortens the Host orchestrator's cold-start window,
// active-host probe period, handback idle time and cold probe period, and
// returns a function that restores them. Call it before starting hosts: the
// timing is read without synchronization.
func SetTimersForTest(coldStart, probe, handback, coldProbe time.Duration) (restore func()) {
	oc, op, oh, ocp := coldStartWindow, probeEvery, handbackIdle, coldProbeEvery
	coldStartWindow, probeEvery, handbackIdle, coldProbeEvery = coldStart, probe, handback, coldProbe
	return func() { coldStartWindow, probeEvery, handbackIdle, coldProbeEvery = oc, op, oh, ocp }
}
