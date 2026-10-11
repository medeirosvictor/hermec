//go:build windows

package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// hasCustomBar reports whether Hermec draws its own title bar.
func hasCustomBar() bool { return true }

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procFindWindow  = user32.NewProc("FindWindowW")
	procRelCapture  = user32.NewProc("ReleaseCapture")
	procPostMessage = user32.NewProc("PostMessageW")

	hwndVal uintptr
)

// hermecHWND finds the Hermec window by its title; cached once found.
func hermecHWND() uintptr {
	if hwndVal != 0 {
		return hwndVal
	}
	title, err := windows.UTF16PtrFromString("Hermec")
	if err != nil {
		return 0
	}
	hwndVal, _, _ = procFindWindow.Call(0, uintptr(unsafe.Pointer(title)))
	return hwndVal
}

const wmNCLButtonDown = 0xA1

// nativeDrag hands a left press to Windows as a non-client press with hit-test
// code ht (HTCAPTION to move, HTLEFT.. to resize), so native drag, Aero snap
// and edge resize run. Best-effort: every failure is ignored.
func nativeDrag(ht uintptr) {
	if ht == 0 {
		return
	}
	hwnd := hermecHWND()
	if hwnd == 0 {
		return
	}
	procRelCapture.Call()
	// Posted, not sent: Update runs off the window's thread, and a
	// SendMessageW from it deadlocks the window (observed: "not responding").
	procPostMessage.Call(hwnd, wmNCLButtonDown, ht, 0)
}
