//go:build windows

package screencap

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The GUI (ebiten) makes the process per-monitor DPI aware; a plain test
// process is not, so opt in here only, to get physical pixels.
func init() {
	p := windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext")
	if p.Find() == nil {
		const perMonitorAwareV2 = ^uintptr(3) // (DPI_AWARENESS_CONTEXT)-4
		p.Call(perMonitorAwareV2)
	}
}

func firstMonitor(t *testing.T) (Source, Monitor) {
	t.Helper()
	src := New()
	mons, err := src.Monitors()
	if err != nil || len(mons) == 0 {
		src.Close()
		t.Skipf("no desktop available (monitors=%d err=%v)", len(mons), err)
	}
	for _, m := range mons {
		t.Logf("monitor: %+v", m)
	}
	return src, mons[0]
}

func TestCaptureMonitorSize(t *testing.T) {
	src, m := firstMonitor(t)
	defer src.Close()
	var dst BGRA
	if err := src.Capture(m, &dst); err != nil {
		t.Skipf("capture unavailable: %v", err)
	}
	if dst.W != m.W || dst.H != m.H {
		t.Fatalf("dst %dx%d, monitor %dx%d", dst.W, dst.H, m.W, m.H)
	}
	if dst.Stride != m.W*4 || len(dst.Pix) < dst.Stride*dst.H {
		t.Fatalf("stride %d len %d", dst.Stride, len(dst.Pix))
	}
	nz := false
	for _, b := range dst.Pix {
		if b != 0 {
			nz = true
			break
		}
	}
	if !nz {
		t.Fatal("captured frame is all zero")
	}
}

func TestCaptureReusesBuffer(t *testing.T) {
	src, m := firstMonitor(t)
	defer src.Close()
	var dst BGRA
	if err := src.Capture(m, &dst); err != nil {
		t.Skipf("capture unavailable: %v", err)
	}
	p := &dst.Pix[0]
	if err := src.Capture(m, &dst); err != nil {
		t.Fatal(err)
	}
	if p != &dst.Pix[0] {
		t.Fatal("Pix was reallocated between captures")
	}
}

func TestCaptureTiming(t *testing.T) {
	src, m := firstMonitor(t)
	defer src.Close()
	var dst BGRA
	if err := src.Capture(m, &dst); err != nil {
		t.Skipf("capture unavailable: %v", err)
	}
	const n = 30
	start := time.Now()
	for i := 0; i < n; i++ {
		if err := src.Capture(m, &dst); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("capture %dx%d: %.2f ms/frame", m.W, m.H, float64(time.Since(start).Microseconds())/1000/n)
}
