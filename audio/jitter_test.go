package audio

import "testing"

// run drives j with one 20ms pull per tick; arrivals[i] frames arrive
// before pull i.
func run(j *jitterState, arrivals []int) []jitterAction {
	var out []jitterAction
	for _, n := range arrivals {
		for k := 0; k < n; k++ {
			j.Arrive()
		}
		out = append(out, j.Next())
	}
	return out
}

func count(as []jitterAction, a jitterAction) int {
	n := 0
	for _, x := range as {
		if x == a {
			n++
		}
	}
	return n
}

func rep(v, n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func TestJitterStartsAtDepthTwo(t *testing.T) {
	var j jitterState
	j.Arrive()
	if a := j.Next(); a != actSilence {
		t.Fatalf("depth 1 should still buffer, got %v", a)
	}
	j.Arrive()
	if a := j.Next(); a != actPlay {
		t.Fatalf("depth 2 should start, got %v", a)
	}
	if j.depth != 1 {
		t.Fatalf("depth = %d", j.depth)
	}
}

// RF3: steady flow, one frame late by one tick (arrives together with the
// next): exactly one PLC frame, no silence once started, depth recovers.
func TestJitterRF3SingleGapOnePLC(t *testing.T) {
	var j jitterState
	// Steady flow at minimal cushion (the 1-frame spare is already spent).
	arr := []int{1, 1, 0, 1, 1, 1, 1, 1}
	arr = append(arr, 0) // the missing frame: empty buffer at pull time
	arr = append(arr, 2) // it arrives late, together with the current one
	arr = append(arr, rep(1, 10)...)
	acts := run(&j, arr)
	if acts[0] != actSilence {
		t.Fatal("first tick should be buffering silence")
	}
	if got := count(acts[1:], actSilence); got != 0 {
		t.Fatalf("silence periods after start: %d (%v)", got, acts)
	}
	if got := count(acts, actPLC); got != 1 {
		t.Fatalf("PLC count = %d, want 1 (%v)", got, acts)
	}
	if j.depth < 1 {
		t.Fatalf("depth did not recover: %d", j.depth)
	}
}

// A gap with cushion available needs no PLC at all.
func TestJitterCushionAbsorbsGap(t *testing.T) {
	var j jitterState
	acts := run(&j, []int{3, 1, 1, 0, 1, 1, 1})
	if count(acts, actPLC) != 0 || count(acts, actSilence) != 0 {
		t.Fatalf("unexpected PLC/silence: %v", acts)
	}
}

// RF4: 500ms (25 ticks) outage: at most 3 PLC frames then silence; resume
// recovers cleanly: excess burst dropped, nothing queued beyond target.
func TestJitterRF4OutageBudgetAndRecovery(t *testing.T) {
	var j jitterState
	run(&j, []int{1, 1, 0, 1, 1, 1, 1}) // steady at minimal cushion
	outage := run(&j, rep(0, 25))
	if got := count(outage, actPLC); got != plcBudget {
		t.Fatalf("PLC during outage = %d, want %d", got, plcBudget)
	}
	for i, a := range outage[:plcBudget] {
		if a != actPLC {
			t.Fatalf("tick %d = %v, want PLC first", i, a)
		}
	}
	for _, a := range outage[plcBudget:] {
		if a != actSilence {
			t.Fatalf("after budget want silence, got %v", a)
		}
	}
	for i := 0; i < 25; i++ { // whole 500ms backlog arrives at once
		j.Arrive()
	}
	if j.depth > targetDepth {
		t.Fatalf("burst not trimmed: depth %d > target %d", j.depth, targetDepth)
	}
	if a := j.Next(); a != actPlay {
		t.Fatalf("resume should play, got %v", a)
	}
	acts := run(&j, rep(1, 20))
	if count(acts, actSilence)+count(acts, actPLC) != 0 {
		t.Fatalf("not clean after resume: %v", acts)
	}
}

func TestJitterPLCBudgetResetByArrival(t *testing.T) {
	var j jitterState
	run(&j, []int{2, 0, 1, 1}) // steady at minimal cushion
	a := run(&j, []int{0, 0, 1, 0, 0})
	want := []jitterAction{actPLC, actPLC, actPlay, actPLC, actPLC}
	for i := range want {
		if a[i] != want[i] {
			t.Fatalf("tick %d: got %v want %v (%v)", i, a[i], want[i], a)
		}
	}
}

func TestJitterAfterExhaustionNeedsRefill(t *testing.T) {
	var j jitterState
	run(&j, []int{2, 1})
	run(&j, rep(0, 6)) // budget gone
	j.Arrive()
	if a := j.Next(); a != actSilence {
		t.Fatalf("single frame after outage should re-buffer, got %v", a)
	}
	j.Arrive()
	if a := j.Next(); a != actPlay {
		t.Fatalf("got %v", a)
	}
}

func TestJitterCapDropsOldest(t *testing.T) {
	var j jitterState
	j.Arrive()
	j.Arrive()
	j.Next() // started
	dropped := 0
	for i := 0; i < 30; i++ {
		dropped += j.Arrive()
	}
	if j.depth > targetDepth+burstSlack || j.depth > maxFrames {
		t.Fatalf("depth %d exceeds bound %d", j.depth, targetDepth+burstSlack)
	}
	if dropped == 0 {
		t.Fatal("expected drops reported")
	}
}
