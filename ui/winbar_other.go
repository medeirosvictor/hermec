//go:build !windows

package ui

import "github.com/medeirosvictor/hermec/ui/state"

// hasCustomBar is false outside Windows: the OS draws the window frame.
func hasCustomBar() bool { return false }

// installBar is a no-op outside Windows.
func installBar() (bool, string) { return false, "" }

// setBarScale is a no-op outside Windows.
func setBarScale(float64) {}

// ncZone has no native hit test outside Windows.
func ncZone() (state.Zone, bool) { return state.ZoneNone, false }
