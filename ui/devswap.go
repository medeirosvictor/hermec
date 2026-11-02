package ui

import "strings"

// deviceSwapper is a call source or sink that can move to another audio
// device without ending the stream (audio.capture and audio.playback).
type deviceSwapper interface{ SwapDevice(name string) error }

// devSwapResult is the outcome of moving the running call onto new devices.
// src and sink identify the call it was started for.
type devSwapResult struct {
	src, sink     any
	in, out       string // the devices asked for
	inErr, outErr error
}

// swapDevices moves the running call onto the input and output devices chosen
// in Settings, in the background (opening a device can take a while). It does
// nothing without a settled call or when the call is already on them; a swap
// requested while one is in flight runs right after it.
func (g *game) swapDevices() {
	if g.vc.src == nil && g.vc.sink == nil {
		return
	}
	if g.vc.swapping {
		g.vc.swapAgain = true
		return
	}
	in, out := g.set.InputDevice, g.set.OutputDevice
	if in == g.vc.inDev && out == g.vc.outDev {
		return
	}
	g.vc.swapping = true
	src, sink, ch := g.vc.src, g.vc.sink, g.swapCh
	curIn, curOut := g.vc.inDev, g.vc.outDev
	go func() {
		res := devSwapResult{src: src, sink: sink, in: in, out: out}
		if in != curIn {
			res.inErr = swapOne(src, in)
		}
		if out != curOut {
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

// applyDevSwap records a finished swap. A device that failed to open stays on
// the previous one: Settings goes back to it (unless the user has already
// moved on), the details are logged and the status says so.
func (g *game) applyDevSwap(r devSwapResult) {
	if r.src != g.vc.src || r.sink != g.vc.sink { // the call it was for is gone
		return
	}
	g.vc.swapping = false
	var notes []string
	if g.vc.inDev != r.in {
		if r.inErr == nil {
			g.vc.inDev = r.in
			notes = append(notes, "microphone switched")
		} else {
			g.logf("switch microphone to %q failed: %v", r.in, r.inErr)
			notes = append(notes, "couldn't switch microphone; keeping the previous one")
			if g.set.InputDevice == r.in {
				g.set.InputDevice = g.vc.inDev
			}
		}
	}
	if g.vc.outDev != r.out {
		if r.outErr == nil {
			g.vc.outDev = r.out
			notes = append(notes, "output switched")
		} else {
			g.logf("switch output to %q failed: %v", r.out, r.outErr)
			notes = append(notes, "couldn't switch output; keeping the previous one")
			if g.set.OutputDevice == r.out {
				g.set.OutputDevice = g.vc.outDev
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
