//go:build !windows

package discover

import "os/exec"

// hideConsole is a no-op off Windows.
func hideConsole(*exec.Cmd) {}
