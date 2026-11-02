package ui

import (
	"errors"
	"strings"
	"time"

	"github.com/medeirosvictor/hermec/audio"
)

// swapDebounce is how long after the last device change in Settings the call
// is moved, so cycling through devices does not open each one.
const swapDebounce = 600 * time.Millisecond

// deviceSwapper is a call source or sink that can move to another audio
// device without ending the stream (audio.capture and audio.playback).
type deviceSwapper interface{ SwapDevice(name string) error }

// devSwapResult is the outcome of moving the running call onto new devices.
// src and sink identify the call it was started for.
type devSwapResult struct {
	src, sink       any
	in, out         string // the devices asked for
	swapIn, swapOut bool   // which sides were attempted
	inErr, outErr   error
}

// queueSwap schedules swapDevices once the device settings stop changing.
func (g *game) queueSwap() {
	if g.vc.src != nil || g.vc.sink != nil {
		g.vc.swapAt = time.Now().Add(swapDebounce)
	}
}

// stepSwap runs a scheduled swap that has come due.
func (g *game) stepSwap(now time.Time) {
	if !g.vc.swapAt.IsZero() && !now.Before(g.vc.swapAt) {
		g.flushSwap()
	}
}

// flushSwap runs a scheduled swap now (Settings closing).
func (g *game) flushSwap() {
	if g.vc.swapAt.IsZero() {
		return
	}
	g.vc.swapAt = time.Time{}
	g.swapDevices()
}

// swapDevices moves the running call onto the input and output devices chosen
// in Settings, in the background (opening a device can take a while). Each
// side is compared with the device last asked for, so only a side that was
// changed moves. It does nothing without a settled call or when the call is
// already on them; a swap requested while one is in flight runs right after it.
func (g *game) swapDevices() {
	if g.vc.src == nil && g.vc.sink == nil {
		return
	}
	if g.vc.swapping {
		g.vc.swapAgain = true
		return
	}
	in, out := g.set.InputDevice, g.set.OutputDevice
	swapIn, swapOut := in != g.vc.reqIn, out != g.vc.reqOut
	if !swapIn && !swapOut {
		return
	}
	g.vc.swapping = true
	src, sink, ch := g.vc.src, g.vc.sink, g.swapCh
	go func() {
		res := devSwapResult{src: src, sink: sink, in: in, out: out, swapIn: swapIn, swapOut: swapOut}
		if swapIn {
			res.inErr = swapOne(src, in)
		}
		if swapOut {
			res.outErr = swapOne(sink, out)
		}
		ch <- res
	}()
}

// swapOne moves v to the named device; something that cannot swap is left alone.
func swapOne(v any, name string) error {
	if s, ok := v.(deviceSwapper); ok {
		return s.SwapDevice(name)
	}
	return nil
}

// swapNote is the status for a device that failed to switch.
func swapNote(what string, err error) string {
	if errors.Is(err, audio.ErrDeviceLost) {
		return "audio device stopped; leave and rejoin the call"
	}
	return "couldn't switch " + what + "; keeping the previous one"
}

// applyDevSwap records a finished swap. A side that failed to open stays on
// the previous device: its Settings entry goes back to the name asked for
// before (unless the user has already moved on), the details are logged and
// the status says so. The other side is never touched.
func (g *game) applyDevSwap(r devSwapResult) {
	if r.src != g.vc.src || r.sink != g.vc.sink { // the call it was for is gone
		return
	}
	g.vc.swapping = false
	var notes []string
	if r.swapIn {
		if r.inErr == nil {
			g.vc.reqIn, g.vc.inDev = r.in, r.in
			notes = append(notes, "microphone switched")
		} else {
			g.logf("switch microphone to %q failed: %v", r.in, r.inErr)
			notes = append(notes, swapNote("microphone", r.inErr))
			if g.set.InputDevice == r.in {
				g.set.InputDevice = g.vc.reqIn
			}
		}
	}
	if r.swapOut {
		if r.outErr == nil {
			g.vc.reqOut, g.vc.outDev = r.out, r.out
			notes = append(notes, "output switched")
		} else {
			g.logf("switch output to %q failed: %v", r.out, r.outErr)
			notes = append(notes, swapNote("output", r.outErr))
			if g.set.OutputDevice == r.out {
				g.set.OutputDevice = g.vc.reqOut
			}
		}
	}
	if s := g.callSess(); s != nil && len(notes) > 0 {
		s.st.Status = strings.Join(notes, "; ")
	}
	g.persistSettings()
	if g.vc.swapAgain {
		g.vc.swapAgain = false
		g.swapDevices()
	}
}
