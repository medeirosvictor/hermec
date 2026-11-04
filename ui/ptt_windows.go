//go:build windows

package ui

import "golang.org/x/sys/windows"

var procGetAsyncKeyState = windows.NewLazySystemDLL("user32.dll").NewProc("GetAsyncKeyState")

// pttVK are the virtual-key codes of the push-to-talk keys. The grave key is
// VK_OEM_3, which is the key left of 1 on a US layout.
var pttVK = map[string]uintptr{
	"rctrl":    0xA3, // VK_RCONTROL
	"grave":    0xC0, // VK_OEM_3
	"capslock": 0x14, // VK_CAPITAL
	"f13":      0x7C, // VK_F13
	"mouse4":   0x05, // VK_XBUTTON1
}

// osKeyDown reads the physical key state system-wide with GetAsyncKeyState, so
// push-to-talk works while another window has focus. No hook is installed and
// nothing but the one chosen key is ever read.
func osKeyDown(id string) (down, ok bool) {
	vk, found := pttVK[id]
	if !found {
		return false, false
	}
	r, _, _ := procGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0, true
}
