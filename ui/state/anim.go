package state

import "math"

// Approach eases cur toward target with time constant tau (seconds) over a
// step of dt seconds: target + (cur-target)*exp(-dt/tau). It never overshoots.
// A non-positive tau jumps straight to target; a non-positive dt changes nothing.
func Approach(cur, target, dt, tau float64) float64 {
	if dt <= 0 {
		return cur
	}
	if tau <= 0 {
		return target
	}
	return target + (cur-target)*math.Exp(-dt/tau)
}

// Envelope follows a level with a fast attack and a slow release, so brief
// dropouts and pickup are visible instead of snapping.
type Envelope struct {
	Attack, Release float64 // time constants in seconds
	v               float64
}

// Step moves the envelope toward level (tau = Attack when rising, Release when
// falling) and returns the new value.
func (e *Envelope) Step(level, dt float64) float64 {
	tau := e.Release
	if level > e.v {
		tau = e.Attack
	}
	e.v = Approach(e.v, level, dt, tau)
	return e.v
}

// Value is the current envelope value.
func (e *Envelope) Value() float64 { return e.v }
