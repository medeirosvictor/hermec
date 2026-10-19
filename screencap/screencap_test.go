package screencap

import "testing"

func TestMonitorName(t *testing.T) {
	got := MonitorName(0, 2560, 1440)
	want := "screen 1 (2560×1440)"
	if got != want {
		t.Fatalf("MonitorName = %q, want %q", got, want)
	}
	if got := MonitorName(2, 1920, 1080); got != "screen 3 (1920×1080)" {
		t.Fatalf("MonitorName(2) = %q", got)
	}
}
