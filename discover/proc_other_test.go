//go:build !windows

package discover

import (
	"os/exec"
	"testing"
)

func TestHideConsoleNoopOffWindows(t *testing.T) {
	cmd := exec.Command("tailscale", "status")
	hideConsole(cmd)
	if cmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr = %+v, want nil", cmd.SysProcAttr)
	}
}
