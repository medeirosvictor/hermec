//go:build windows

package screencap

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether this platform has a capture backend.
const Supported = true

// DPI: coordinates from EnumDisplayMonitors/GetMonitorInfo and the pixels
// BitBlt copies are physical only when the process is DPI aware. The GUI
// (ebiten) makes the process per-monitor DPI aware; this package never sets
// awareness itself (tests opt in inside their own helper).

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procGetCursorInfo       = user32.NewProc("GetCursorInfo")
	procGetIconInfo         = user32.NewProc("GetIconInfo")
	procDrawIconEx          = user32.NewProc("DrawIconEx")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

const (
	srccopy        = 0x00CC0020
	captureblt     = 0x40000000
	cursorShowing  = 0x1
	diNormal       = 0x3
	biRGB          = 0
	dibRGBColors   = 0
	monitorPrimary = 0x1
)

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type point struct{ X, Y int32 }

type cursorInfo struct {
	Size    uint32
	Flags   uint32
	Cursor  uintptr
	ScreenP point
}

type iconInfo struct {
	IsIcon   int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// enumMonitors collects rects from the EnumDisplayMonitors callback. The
// callback is created once (syscall callbacks are never freed) and the
// results are guarded by enumMu.
var (
	enumMu   sync.Mutex
	enumOut  []monitorRect
	enumCB   = syscall.NewCallback(enumProc)
	errNoDIB = errors.New("screencap: CreateDIBSection failed")
)

type monitorRect struct {
	r       rect
	primary bool
}

func enumProc(hmon, hdc, lprc, lparam uintptr) uintptr {
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r != 0 {
		enumOut = append(enumOut, monitorRect{mi.Monitor, mi.Flags&monitorPrimary != 0})
	}
	return 1
}

type gdiSource struct {
	mu    sync.Mutex
	memDC uintptr
	dib   uintptr
	old   uintptr
	bits  unsafe.Pointer
	w, h  int
}

// New returns a GDI-backed Source.
func New() Source { return &gdiSource{} }

func (s *gdiSource) Monitors() ([]Monitor, error) {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = enumOut[:0]
	if r, _, err := procEnumDisplayMonitors.Call(0, 0, enumCB, 0); r == 0 {
		return nil, fmt.Errorf("screencap: EnumDisplayMonitors: %w", err)
	}
	rs := append([]monitorRect(nil), enumOut...)
	// Primary first, so "screen 1" is the main display; others keep order.
	for i, m := range rs {
		if m.primary && i > 0 {
			copy(rs[1:i+1], rs[:i])
			rs[0] = m
			break
		}
	}
	out := make([]Monitor, len(rs))
	for i, m := range rs {
		w, h := int(m.r.Right-m.r.Left), int(m.r.Bottom-m.r.Top)
		out[i] = Monitor{Index: i, Name: MonitorName(i, w, h), X: int(m.r.Left), Y: int(m.r.Top), W: w, H: h}
	}
	return out, nil
}

// ensure (re)creates the memory DC and top-down 32bpp DIB for w x h.
func (s *gdiSource) ensure(screenDC uintptr, w, h int) error {
	if s.memDC != 0 && s.w == w && s.h == h {
		return nil
	}
	s.release()
	dc, _, _ := procCreateCompatibleDC.Call(screenDC)
	if dc == 0 {
		return errors.New("screencap: CreateCompatibleDC failed")
	}
	bi := bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(w), Height: -int32(h),
		Planes: 1, BitCount: 32, Compression: biRGB,
	}
	var bits unsafe.Pointer
	dib, _, _ := procCreateDIBSection.Call(screenDC, uintptr(unsafe.Pointer(&bi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		procDeleteDC.Call(dc)
		return errNoDIB
	}
	old, _, _ := procSelectObject.Call(dc, dib)
	s.memDC, s.dib, s.old, s.bits, s.w, s.h = dc, dib, old, bits, w, h
	return nil
}

func (s *gdiSource) release() {
	if s.memDC != 0 {
		procSelectObject.Call(s.memDC, s.old)
		procDeleteObject.Call(s.dib)
		procDeleteDC.Call(s.memDC)
	}
	s.memDC, s.dib, s.old, s.bits, s.w, s.h = 0, 0, 0, nil, 0, 0
}

func (s *gdiSource) Capture(m Monitor, dst *BGRA) error {
	if m.W <= 0 || m.H <= 0 {
		return fmt.Errorf("screencap: invalid monitor size %dx%d", m.W, m.H)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// A window DC belongs to the thread that got it: keep this goroutine on
	// one OS thread from GetDC to ReleaseDC (deferred first, so it runs last).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return errors.New("screencap: GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	if err := s.ensure(screenDC, m.W, m.H); err != nil {
		return err
	}
	if r, _, err := procBitBlt.Call(s.memDC, 0, 0, uintptr(m.W), uintptr(m.H), screenDC,
		uintptr(int32(m.X)), uintptr(int32(m.Y)), srccopy|captureblt); r == 0 {
		return fmt.Errorf("screencap: BitBlt: %w", err)
	}
	s.drawCursor(m)

	n := m.W * m.H * 4
	if cap(dst.Pix) < n {
		dst.Pix = make([]byte, n)
	}
	dst.Pix = dst.Pix[:n]
	copy(dst.Pix, unsafe.Slice((*byte)(s.bits), n))
	dst.W, dst.H, dst.Stride = m.W, m.H, m.W*4
	return nil
}

// drawCursor paints the mouse cursor into the DIB if it is visible and on m.
func (s *gdiSource) drawCursor(m Monitor) {
	ci := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 || ci.Flags&cursorShowing == 0 || ci.Cursor == 0 {
		return
	}
	cx, cy := int(ci.ScreenP.X)-m.X, int(ci.ScreenP.Y)-m.Y
	if cx < 0 || cy < 0 || cx >= m.W || cy >= m.H {
		return
	}
	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(ci.Cursor, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return
	}
	if ii.Mask != 0 {
		procDeleteObject.Call(ii.Mask)
	}
	if ii.Color != 0 {
		procDeleteObject.Call(ii.Color)
	}
	procDrawIconEx.Call(s.memDC, uintptr(int32(cx-int(ii.XHotspot))), uintptr(int32(cy-int(ii.YHotspot))),
		ci.Cursor, 0, 0, 0, 0, diNormal)
}

func (s *gdiSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.release()
	return nil
}
