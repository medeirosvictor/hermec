//go:build !windows

package ui

// osKeyDown has no system-wide key state outside Windows: push-to-talk reads
// Ebiten input and so works only while the window has focus.
func osKeyDown(string) (down, ok bool) { return false, false }
