//go:build windows

package ui

import (
	"fmt"
	"math"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/medeirosvictor/hermec/ui/state"
)

// The window keeps its native overlapped style (caption, thick frame, min/max
// boxes), so Aero snap, edge resize, maximize, Win+arrows and double-click
// maximize stay native. A window subclass takes over only the non-client
// area: WM_NCCALCSIZE makes the client cover the whole window, and
// WM_NCHITTEST reports our title bar as the caption and our borders as the
// resize edges. The callbacks touch only atomics and pure functions.

var (
	customBar atomic.Bool    // true while the custom frame is in effect
	scaleBits atomic.Uint64  // DPI scale factor (float64 bits)
	hitZone   atomic.Int32   // last zone reported by the hit test (state.Zone)
	hitSeen   atomic.Bool    // the hit test has run at least once
	prevProc  atomic.Uintptr // the window procedure we chain to
)

func init() {
	customBar.Store(true) // optimistic until installBar fails
	scaleBits.Store(math.Float64bits(1))
}

// hasCustomBar reports whether Hermec draws its own title bar. It is a
// runtime flag: installBar clears it when the subclass cannot be installed,
// leaving the native frame alone.
func hasCustomBar() bool { return customBar.Load() }

// setBarScale records the DPI scale for the hit test.
func setBarScale(f float64) {
	if f > 0 {
		scaleBits.Store(math.Float64bits(f))
	}
}

// ncZone is the zone the window's hit test last saw under the mouse.
func ncZone() (state.Zone, bool) {
	if !hitSeen.Load() {
		return state.ZoneNone, false
	}
	return state.Zone(hitZone.Load()), true
}

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	dwmapi          = windows.NewLazySystemDLL("dwmapi.dll")
	procEnumWindows = user32.NewProc("EnumWindows")
	procGetWinPID   = user32.NewProc("GetWindowThreadProcessId")
	procGetClass    = user32.NewProc("GetClassNameW")
	procGetCurPID   = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentProcessId")
	procWindowDpi   = user32.NewProc("GetDpiForWindow")
	procMetricsDpi  = user32.NewProc("GetSystemMetricsForDpi")
	procScreenToCl  = user32.NewProc("ScreenToClient")
	procGetClientRc = user32.NewProc("GetClientRect")
	procIsZoomed    = user32.NewProc("IsZoomed")
	procSetWindowPo = user32.NewProc("SetWindowPos")
	procSysMetrics  = user32.NewProc("GetSystemMetrics")
	procGetLongPtr  = user32.NewProc("GetWindowLongPtrW")
	procSetLongPtr  = user32.NewProc("SetWindowLongPtrW")
	procCallWndProc = user32.NewProc("CallWindowProcW")
	procDwmExtend   = dwmapi.NewProc("DwmExtendFrameIntoClientArea")

	subclassCB = syscall.NewCallback(subclassProc)
)

const (
	wmNCCalcSize = 0x0083
	wmNCHitTest  = 0x0084

	htClient = 1

	gwlpWndProc = ^uintptr(3)  // GWLP_WNDPROC = -4
	gwlStyle    = ^uintptr(15) // GWL_STYLE = -16
	wsCaption   = 0x00C00000   // WS_BORDER | WS_DLGFRAME

	smCXFrame        = 32
	smCYFrame        = 33
	smCXPaddedBorder = 92

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020
	// Async: Update runs off the window's thread, and a synchronous
	// SetWindowPos sends messages to it that deadlock against the frame sync.
	swpAsync = 0x4000
)

type winRect struct{ left, top, right, bottom int32 }
type winPoint struct{ x, y int32 }

// glfwClass is the window class GLFW registers (_GLFW_WNDCLASSNAME in
// Ebiten's internal/glfw); its hidden helper window shares it.
const glfwClass = "GLFW30"

var enumCB = syscall.NewCallback(enumProc)

// enumState is filled by enumProc; installBar runs once, on one goroutine.
var enumState struct {
	pid       uint32
	byClass   uintptr // our process, class GLFW30, has a caption (decorated window)
	byCaption uintptr // our process, any class, has a caption
}

func enumProc(hwnd, _ uintptr) uintptr {
	var pid uint32
	procGetWinPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != enumState.pid {
		return 1 // someone else's window: keep going
	}
	// Neither GetWindowTextW (it SENDS WM_GETTEXT to own-process windows, which
	// would deadlock against the window thread) nor any other sending call is
	// used here: only the class name and the style word, which do not send.
	style, _, _ := procGetLongPtr.Call(hwnd, gwlStyle)
	if style&wsCaption != wsCaption {
		return 1 // GLFW's hidden helper window has no caption
	}
	if enumState.byCaption == 0 {
		enumState.byCaption = hwnd
	}
	var buf [64]uint16
	n, _, _ := procGetClass.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if windows.UTF16ToString(buf[:n]) == glfwClass && enumState.byClass == 0 {
		enumState.byClass = hwnd
	}
	return 1
}

// findOwnWindow finds the Hermec window among this process's top-level
// windows only (another program's window titled "hermec", e.g. an Explorer
// folder, can never match, since the title is not used at all). It prefers the
// GLFW class, then falls back to any captioned window of our own process.
func findOwnWindow() (hwnd uintptr, method string) {
	pid, _, _ := procGetCurPID.Call()
	enumState.pid, enumState.byClass, enumState.byCaption = uint32(pid), 0, 0
	procEnumWindows.Call(enumCB, 0)
	switch {
	case enumState.byClass != 0:
		return enumState.byClass, "EnumWindows pid+class " + glfwClass
	case enumState.byCaption != 0:
		return enumState.byCaption, "EnumWindows pid+caption"
	}
	return 0, "EnumWindows found none"
}

// sysMetric is GetSystemMetrics for the window's DPI where the OS has
// GetSystemMetricsForDpi (Windows 10 1607+), else the system-wide value.
func sysMetric(idx, hwnd uintptr) int32 {
	if procMetricsDpi.Find() == nil && procWindowDpi.Find() == nil {
		if dpi, _, _ := procWindowDpi.Call(hwnd); dpi != 0 {
			r, _, _ := procMetricsDpi.Call(idx, dpi)
			return int32(r)
		}
	}
	r, _, _ := procSysMetrics.Call(idx)
	return int32(r)
}

// installBar subclasses the Hermec window and applies the frame change. On any
// failure it leaves the native frame alone and clears the custom-bar flag, so
// the user never gets a frameless window that cannot be moved. info is a short
// diagnostic for the verbose log.
func installBar() (ok bool, info string) {
	hwnd, method := findOwnWindow()
	if hwnd == 0 {
		customBar.Store(false)
		return false, method
	}
	// SetWindowSubclass only works from the window's own thread; Update runs
	// elsewhere, so chain the window procedure directly (allowed within the
	// process from any thread). The callback then runs on the window's thread.
	prev, _, _ := procGetLongPtr.Call(hwnd, gwlpWndProc)
	if prev == 0 {
		customBar.Store(false)
		return false, method + ": GetWindowLongPtrW failed"
	}
	prevProc.Store(prev) // before the swap: the callback may run at once
	if old, _, _ := procSetLongPtr.Call(hwnd, gwlpWndProc, subclassCB); old == 0 {
		customBar.Store(false)
		return false, method + ": SetWindowLongPtrW failed"
	}
	margins := [4]int32{0, 0, 1, 0} // keeps the drop shadow; best-effort
	procDwmExtend.Call(hwnd, uintptr(unsafe.Pointer(&margins)))
	r, _, _ := procSetWindowPo.Call(hwnd, 0, 0, 0, 0, 0,
		swpFrameChanged|swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpAsync)
	return true, fmt.Sprintf("hwnd %#x via %s; subclassed; SetWindowPos(FRAMECHANGED, async) = %d", hwnd, method, r)
}

// subclassProc is the window procedure hook. It runs on the window's thread
// and must not touch game state, Ebiten, or any lock the game holds.
func subclassProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmNCCalcSize:
		if wParam != 0 {
			if z, _, _ := procIsZoomed.Call(hwnd); z != 0 {
				// Maximized windows overhang the screen by the frame width;
				// inset so the content is not clipped off-screen.
				p := (*winRect)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam))) // lParam is a pointer to NCCALCSIZE_PARAMS; rgrc[0] is first
				pad := sysMetric(smCXPaddedBorder, hwnd)
				fx := sysMetric(smCXFrame, hwnd) + pad
				fy := sysMetric(smCYFrame, hwnd) + pad
				p.left += fx
				p.right -= fx
				p.top += fy
				p.bottom -= fy
			}
			return 0 // client area = whole window
		}
	case wmNCHitTest:
		pt := winPoint{int32(int16(lParam & 0xFFFF)), int32(int16((lParam >> 16) & 0xFFFF))}
		procScreenToCl.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
		var cl winRect
		procGetClientRc.Call(hwnd, uintptr(unsafe.Pointer(&cl)))
		zoomed, _, _ := procIsZoomed.Call(hwnd)
		scale := math.Float64frombits(scaleBits.Load())
		z := state.TitleZonePhysical(float64(pt.x), float64(pt.y),
			float64(cl.right-cl.left), float64(cl.bottom-cl.top), scale, zoomed != 0)
		hitZone.Store(int32(z))
		hitSeen.Store(true)
		if ht := state.HTFor(z); ht != 0 {
			return ht
		}
		return htClient // buttons and the scene: Ebiten gets the clicks
	}
	r, _, _ := procCallWndProc.Call(prevProc.Load(), hwnd, msg, wParam, lParam)
	return r
}
