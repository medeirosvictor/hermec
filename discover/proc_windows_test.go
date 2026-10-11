//go:build windows

package discover

import (
	"os/exec"
	"testing"
)

func TestHideConsoleSetsNoWindow(t *testing.T) {
	cmd := exec.Command("tailscale", "status")
	hideConsole(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr not set")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow = false")
	}
	if cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Errorf("CreationFlags = %#x, missing CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}
