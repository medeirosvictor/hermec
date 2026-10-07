package audio

const (
	// targetDepth is the jitter-buffer depth (3 frames, 60ms) a sender is
	// trimmed to while (re)starting; it also bounds a restart burst so a
	// resumed stream never replays an outage as a flood.
	targetDepth = 3
	// startDepth is how many frames must be queued before a sender is heard
	// after joining or after an outage that outlasted the PLC budget.
	// Tunable: raise if audio sounds thin on jittery links.
	startDepth = 2
	// burstSlack is how far above targetDepth a running sender may queue
	// before the oldest frames are dropped back to targetDepth. 2 frames
	// (40ms) tolerates ordinary +-20ms arrival bunching without a drop, but
	// bounds standing latency to targetDepth+slack (100ms) after a stall
	// burst or sender clock drift.
	burstSlack = 2
	// maxFrames is the ring capacity; depth never exceeds
	// targetDepth+burstSlack, so this is only a hard safety bound.
	maxFrames = 10
	// plcBudget is how many consecutive concealment frames are generated
	// for an empty buffer before falling back to silence.
	plcBudget = 3
)

type jitterAction int

const (
	actSilence jitterAction = iota // nothing to play this slot
	actPlay                        // pop and play the oldest queued frame
	actPLC                         // play one Opus-concealed frame
)

func (a jitterAction) String() string {
	return [...]string{"silence", "play", "plc"}[a]
}

// jitterState is the pure depth / PLC-budget state machine for one sender.
// It tracks only counts (no audio, no devices, no clock). Arrive is called
// per received frame, Next once per 20ms output slot. There is no
// unprime/re-prime cycle: once started, an empty buffer is bridged with up
// to plcBudget concealment frames and playback resumes the moment real
// audio is back; only after the budget is spent does it wait for
// startDepth frames again.
type jitterState struct {
	depth   int
	started bool
	plcUsed int
}

// Arrive registers one received frame and returns how many of the oldest
// queued frames must be dropped to respect the cap. A real arrival resets
// the PLC budget.
func (j *jitterState) Arrive() (drop int) {
	j.depth++
	j.plcUsed = 0
	switch {
	case !j.started && j.depth > targetDepth:
		drop = j.depth - targetDepth
		j.depth = targetDepth
	case j.started && j.depth > targetDepth+burstSlack:
		// Burst after a short stall (or a fast sender clock): trim back to
		// target so latency is not left standing at the old backlog.
		drop = j.depth - targetDepth
		j.depth = targetDepth
	}
	return drop
}

// Next decides what the next 20ms output slot plays.
func (j *jitterState) Next() jitterAction {
	if !j.started {
		if j.depth < startDepth {
			return actSilence
		}
		j.started = true
	}
	if j.depth > 0 {
		j.depth--
		return actPlay
	}
	if j.plcUsed < plcBudget {
		j.plcUsed++
		return actPLC
	}
	j.started = false // outage outlasted the budget: refill before resuming
	return actSilence
}
