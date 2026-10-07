//go:build !windows

package ui

import "log"

// openURL only logs the URL outside Windows.
func openURL(url string) { log.Printf("update available: %s", url) }
