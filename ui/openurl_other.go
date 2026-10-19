//go:build !windows

package ui

import (
	"log"
	"os/exec"
	"runtime"
)

// openURL opens a URL or folder with the platform opener (open, xdg-open),
// started without waiting.
func openURL(target string) {
	name, args := openCommand(runtime.GOOS, target)
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		log.Printf("open %s: %v", target, err)
		return
	}
	go func() { _ = cmd.Wait() }()
}
