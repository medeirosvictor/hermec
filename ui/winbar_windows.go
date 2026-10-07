//go:build windows

package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// darkTitleBar asks DWM for a dark title bar on the Hermec window.
// Best-effort: every failure is ignored.
func darkTitleBar() {
	title, err := windows.UTF16PtrFromString("Hermec")
	if err != nil {
		return
	}
	fw := windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowW")
	hwnd, _, _ := fw.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd == 0 {
		return
	}
	dwm := windows.NewLazySystemDLL("dwmapi.dll")
	proc := dwm.NewProc("DwmSetWindowAttribute")
	if proc.Find() != nil {
		return
	}
	on := int32(1)
	// 20 = DWMWA_USE_IMMERSIVE_DARK_MODE; 19 on pre-20H1 Windows 10 builds.
	for _, attr := range []uintptr{20, 19} {
		r, _, _ := proc.Call(hwnd, attr, uintptr(unsafe.Pointer(&on)), unsafe.Sizeof(on))
		if r == 0 {
			return
		}
	}
}
