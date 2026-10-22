package server

import (
	"testing"
)

func TestSetHostTimersForTestRestores(t *testing.T) {
	oc, op, oh, ocp := coldStartWindow, probeEvery, handbackIdle, coldProbeEvery
	restore := SetHostTimersForTest(1, 2, 3, 4)
	if coldStartWindow != 1 || probeEvery != 2 || handbackIdle != 3 || coldProbeEvery != 4 {
		t.Fatalf("timers = %v %v %v %v", coldStartWindow, probeEvery, handbackIdle, coldProbeEvery)
	}
	restore()
	if coldStartWindow != oc || probeEvery != op || handbackIdle != oh || coldProbeEvery != ocp {
		t.Fatal("timers not restored")
	}
}
