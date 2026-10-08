package audio

import (
	"log"
	"os"
	"sync/atomic"
	"time"
)

// Live-call diagnostics: set HERMEC_AUDIO_DEBUG=1 before launching and the
// client logs one line per second of playback jitter and capture stats to
// stderr. Costs one nil check per audio event when the variable is unset.
var dbg *audioDebug

func init() {
	if os.Getenv("HERMEC_AUDIO_DEBUG") != "" {
		dbg = &audioDebug{}
	}
}

type audioDebug struct {
	arrivals atomic.Int64 // playback: frames handed to the mixer
	clumped  atomic.Int64 // arrival gap < 5ms (burst delivery)
	stalled  atomic.Int64 // arrival gap > 40ms (late delivery)
	trims    atomic.Int64 // Arrive had to drop (burst over slack)
	dropped  atomic.Int64 // frames discarded by those trims
	played   atomic.Int64 // output slots served with a real frame
	plc      atomic.Int64 // output slots concealed
	silence  atomic.Int64 // output slots with nothing to play (incl. pre-start)
	maxDepth atomic.Int64 // deepest sender queue seen this interval
	capDrops atomic.Int64 // capture: frames lost to ring overflow / hard cap
	capSkips atomic.Int64 // capture: frames skipped when resuming after a pause
}

func (d *audioDebug) depth(v int) {
	for {
		cur := d.maxDepth.Load()
		if int64(v) <= cur || d.maxDepth.CompareAndSwap(cur, int64(v)) {
			return
		}
	}
}

// run logs and resets the counters once per second until stop closes.
func (d *audioDebug) run(stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			log.Printf("audio dbg: arr=%d clump=%d stall=%d trims=%d dropped=%d | play=%d plc=%d silent=%d maxdepth=%d | cap ring=%d pause=%d",
				d.arrivals.Swap(0), d.clumped.Swap(0), d.stalled.Swap(0),
				d.trims.Swap(0), d.dropped.Swap(0),
				d.played.Swap(0), d.plc.Swap(0), d.silence.Swap(0),
				d.maxDepth.Swap(0),
				d.capDrops.Swap(0), d.capSkips.Swap(0))
		}
	}
}
