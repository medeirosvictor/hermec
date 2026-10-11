//go:build windows

package ui

import "testing"

// When the subclass cannot be installed the runtime flag drops, the bar is
// gone and scenes get the whole window. (The Win32 callbacks themselves cannot
// be unit-tested; they are pure atomics plus state.TitleZonePhysical, which is.)
func TestFallbackFlagRemovesBar(t *testing.T) {
	defer customBar.Store(customBar.Load())
	g := &game{}
	customBar.Store(true)
	if g.barH() != titleBarH {
		t.Fatalf("barH = %v with the custom bar on", g.barH())
	}
	customBar.Store(false)
	if g.barH() != 0 {
		t.Fatalf("barH = %v after fallback, want 0", g.barH())
	}
}
