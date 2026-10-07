//go:build windows

package ui

import (
	"log"
	"os/exec"
)

// openURL opens url in the default browser.
func openURL(url string) {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		log.Printf("open %s: %v", url, err)
	}
}
