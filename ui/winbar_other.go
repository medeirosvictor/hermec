//go:build !windows

package ui

// hasCustomBar is false outside Windows: the OS draws the window frame.
func hasCustomBar() bool { return false }

// nativeDrag is a no-op outside Windows.
func nativeDrag(ht uintptr) {}
