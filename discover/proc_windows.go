//go:build windows

package discover

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process creation flag.
const createNoWindow = 0x08000000

// hideConsole stops a child process from opening a console window when this
// process has no console of its own (e.g. a double-clicked release exe).
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
