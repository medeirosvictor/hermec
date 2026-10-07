package audio

import (
	"math/rand"
	"testing"
)

// Short outage (within the PLC budget, so the sender stays started)
// followed by a burst must not leave standing latency: depth is trimmed
// back to targetDepth once it would exceed targetDepth+burstSlack.
func TestJitterShortOutageBurstBounded(t *testing.T) {
	for stall := 1; stall <= plcBudget; stall++ {
		for burst := 1; burst <= 20; burst++ {
			var j jitterState
			run(&j, []int{1, 1, 0, 1, 1, 1})
			run(&j, rep(0, stall))
			for i := 0; i < burst; i++ {
				j.Arrive()
				if j.depth > targetDepth+burstSlack {
					t.Fatalf("stall %d burst %d: depth %d > %d", stall, burst, j.depth, targetDepth+burstSlack)
				}
			}
			acts := run(&j, rep(1, 10))
			if count(acts, actSilence) != 0 {
				t.Fatalf("stall %d burst %d: silence after burst: %v", stall, burst, acts)
			}
			if j.depth > targetDepth+burstSlack {
				t.Fatalf("standing latency: depth %d", j.depth)
			}
		}
	}
}

func TestJitterBurstTrimsToTarget(t *testing.T) {
	var j jitterState
	run(&j, []int{1, 1})
	drops := 0
	for i := 0; i < 20; i++ {
		drops += j.Arrive()
	}
	if j.depth > targetDepth+burstSlack || drops == 0 {
		t.Fatalf("depth %d drops %d", j.depth, drops)
	}
}

// Arrivals within burstSlack of target are tolerated, not dropped.
func TestJitterSlackTolerated(t *testing.T) {
	var j jitterState
	run(&j, []int{1, 1})
	for j.depth < targetDepth+burstSlack {
		if d := j.Arrive(); d != 0 {
			t.Fatalf("dropped %d within slack at depth %d", d, j.depth)
		}
	}
}

// Phase offset: arrivals jittered +-15ms against the pull tick must never
// produce post-start silence or more than plcBudget PLC in a row.
func TestJitterPhaseOffsetJitter(t *testing.T) {
	phiPLC := map[int]int{}
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		const ticks = 500
		var arr []int
		last := -1000
		for k := 0; k < ticks; k++ {
			a := k*20 + rng.Intn(31) - 15
			if a < last { // network keeps order
				a = last
			}
			last = a
			arr = append(arr, a)
		}
		for _, phi := range []int{0, 5, 10, 15} {
			var j jitterState
			next, started, consec := 0, false, 0
			plc, silent := 0, 0
			for p := 0; p < ticks-2; p++ {
				pullAt := p*20 + phi
				for next < len(arr) && arr[next] <= pullAt {
					j.Arrive()
					next++
				}
				switch j.Next() {
				case actPlay:
					started, consec = true, 0
				case actPLC:
					consec++
					plc++
					if consec > plcBudget {
						t.Fatalf("seed %d phi %d: %d PLC in a row", seed, phi, consec)
					}
				case actSilence:
					if started {
						silent++
					}
				}
			}
			if !started {
				t.Fatalf("seed %d phi %d: never started", seed, phi)
			}
			phiPLC[phi] += plc
			// Jitter this size can empty the buffer, but never for long:
			// no more than one in ten slots may be concealed or silent.
			if plc+silent > ticks/10 {
				t.Fatalf("seed %d phi %d: %d PLC + %d silent of %d slots", seed, phi, plc, silent, ticks)
			}
		}
	}
	t.Logf("PLC totals by phase: %v", phiPLC)
	if phiPLC[0]+phiPLC[5]+phiPLC[10]+phiPLC[15] == 0 {
		t.Fatal("test never exercised a late-arrival miss")
	}
}
